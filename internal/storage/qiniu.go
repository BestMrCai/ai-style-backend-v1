package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// 下载凭证有效期。读图是即时操作，2 小时足够，
// 也限制了 URL 被转发后的可用窗口。
const privateURLTTL = 2 * time.Hour

// privateMode 的三种取值。
const (
	modeAuto    int32 = 0
	modePrivate int32 = 1
	modePublic  int32 = 2
)

// errUnauthorized 只用于内部区分「需要凭证」，不暴露给上层。
var errUnauthorized = errors.New("qiniu: unauthorized")

// QiniuStorage 使用七牛云对象存储。
//
// 没有引入官方 SDK：只用到「上传凭证签名 + 表单上传 + 管理接口删除」
// 三个动作，标准库足够，也避免为一个依赖牵进整棵 vendor 树。
//
// 换阿里云 OSS / 腾讯云 COS 时，只需再写一个实现 Storage 的结构体，
// 业务层和 handler 一行都不用动。
type QiniuStorage struct {
	accessKey string
	secretKey string
	bucket    string
	// domain 是绑定的访问域名，形如 https://cdn.example.com（不带结尾斜杠）。
	domain string
	// uploadURL 是上传入口。不同存储区域的上传域名不同，
	// 华东 up.qiniup.com、华北 up-z1.qiniup.com 等，因此做成可配置。
	uploadURL string
	// apiHost 是管理接口域名，用于删除对象。
	apiHost string
	// privateMode 决定下载是否带凭证：
	// 0 = 自动（先直连，401 后改用签名并记住结果）
	// 1 = 私有空间，始终签名
	// 2 = 公有空间，始终直连
	privateMode int32
	client      *http.Client
}

// QiniuOptions 是创建七牛存储所需的参数。
type QiniuOptions struct {
	AccessKey string
	SecretKey string
	Bucket    string
	Domain    string
	UploadURL string
	APIHost   string
	// Private 控制下载是否带私有空间凭证，取值 auto / true / false。
	// 留空按 auto：先直连，收到 401 再改用签名并记住，
	// 这样公有空间不会多一次请求，私有空间也只是第一次多一跳。
	Private string
	Timeout time.Duration
}

// NewQiniuStorage 创建七牛云存储。
func NewQiniuStorage(opt QiniuOptions) (*QiniuStorage, error) {
	if strings.TrimSpace(opt.AccessKey) == "" {
		return nil, errors.New("qiniu: access key is required")
	}
	if strings.TrimSpace(opt.SecretKey) == "" {
		return nil, errors.New("qiniu: secret key is required")
	}
	if strings.TrimSpace(opt.Bucket) == "" {
		return nil, errors.New("qiniu: bucket is required")
	}
	if strings.TrimSpace(opt.Domain) == "" {
		return nil, errors.New("qiniu: domain is required")
	}

	uploadURL := strings.TrimRight(strings.TrimSpace(opt.UploadURL), "/")
	if uploadURL == "" {
		uploadURL = "https://up.qiniup.com"
	}
	apiHost := strings.TrimRight(strings.TrimSpace(opt.APIHost), "/")
	if apiHost == "" {
		apiHost = "https://rs.qiniuapi.com"
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	privateMode := int32(modeAuto)
	switch strings.ToLower(strings.TrimSpace(opt.Private)) {
	case "true", "1", "yes", "private":
		privateMode = modePrivate
	case "false", "0", "no", "public":
		privateMode = modePublic
	}

	// 七牛控制台拷出来的域名常常不带协议头。
	// 少了它会拼出 "xxx.com/upload/..." 这种非法 URL，
	// 报错是 "unsupported protocol scheme"，很难联想到是配置问题。
	domain := strings.TrimRight(strings.TrimSpace(opt.Domain), "/")
	if !strings.Contains(domain, "://") {
		domain = "https://" + domain
	}

	return &QiniuStorage{
		accessKey:   strings.TrimSpace(opt.AccessKey),
		secretKey:   strings.TrimSpace(opt.SecretKey),
		bucket:      strings.TrimSpace(opt.Bucket),
		domain:      domain,
		uploadURL:   uploadURL,
		apiHost:     apiHost,
		privateMode: privateMode,
		client:      &http.Client{Timeout: timeout},
	}, nil
}

// Driver 实现 Storage。
func (s *QiniuStorage) Driver() string { return "qiniu" }

// Put 表单上传到七牛。
func (s *QiniuStorage) Put(ctx context.Context, key string, data []byte) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	token := s.uploadToken(key)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("token", token); err != nil {
		return fmt.Errorf("qiniu: write token field: %w", err)
	}
	if err := writer.WriteField("key", key); err != nil {
		return fmt.Errorf("qiniu: write key field: %w", err)
	}

	part, err := writer.CreateFormFile("file", fileNameOf(key))
	if err != nil {
		return fmt.Errorf("qiniu: create file part: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("qiniu: write file part: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("qiniu: close multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.uploadURL, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("qiniu: upload: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qiniu: upload http %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	// 七牛上传成功返回 {"key":"...","hash":"..."}；
	// 若指定了 returnBody 才有别的字段。这里只要 200 且无 error 即可。
	var out struct {
		Error string `json:"error"`
		Key   string `json:"key"`
	}
	if err := json.Unmarshal(raw, &out); err == nil && out.Error != "" {
		return fmt.Errorf("qiniu: upload rejected: %s", out.Error)
	}

	return nil
}

// Get 通过绑定域名下载对象。
//
// 私有空间的公开域名不带凭证会返回 401，因此这里按模式处理：
// 自动模式下先直连，收到 401 再带签名重试一次并记住结果，
// 后续请求直接走签名，不会每次都多一跳。
func (s *QiniuStorage) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	mode := atomic.LoadInt32(&s.privateMode)

	// 已知是私有空间，直接带签名。
	if mode == modePrivate {
		return s.download(ctx, key, true)
	}

	data, err := s.download(ctx, key, false)
	if err == nil {
		return data, nil
	}

	// 不是 401 的错误直接抛出，不浪费一次请求。
	if !errors.Is(err, errUnauthorized) {
		return nil, err
	}

	// 自动模式：探测到私有空间，记住后重试。
	if mode == modeAuto {
		atomic.StoreInt32(&s.privateMode, modePrivate)
	}

	return s.download(ctx, key, true)
}

// download 执行一次下载。signed 决定是否附加私有空间凭证。
func (s *QiniuStorage) download(ctx context.Context, key string, signed bool) ([]byte, error) {
	// key 里可能有中文或空格，逐段转义后再拼 URL。
	target := s.domain + "/" + escapeKey(key)

	if signed {
		var err error
		target, err = s.signedURL(key)
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qiniu: download: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		// 继续下面的读取逻辑。

	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrObjectNotFound

	case resp.StatusCode == http.StatusUnauthorized:
		if signed {
			// 已带签名仍 401：多为 AK/SK 不匹配或域名绑定错误。
			return nil, fmt.Errorf("qiniu: download rejected with signed url, check access key and domain binding")
		}
		return nil, errUnauthorized

	default:
		return nil, fmt.Errorf("qiniu: download http %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("qiniu: read response: %w", err)
	}
	if len(data) == 0 {
		return nil, ErrObjectNotFound
	}
	return data, nil
}

// privateToken 生成私有空间下载凭证。
//
// 签名原文是**完整 URL**（含协议、域名、路径与 ?e=），不是相对路径。
// 这一点很容易搞错：官方文档示例里写的是 "<path>?e=<deadline>"，
// 但实际校验以请求的完整 URL 为准；用相对路径签会得到
// "download token auth failed"。
func (s *QiniuStorage) signedURL(key string) (string, error) {
	deadline := strconv.FormatInt(time.Now().Add(privateURLTTL).Unix(), 10)
	path := "/" + escapeKey(key)

	// 先拼出不含 token 的完整 URL，签名基于它。
	base := s.domain + path + "?e=" + deadline

	mac := hmac.New(sha1.New, []byte(s.secretKey))
	mac.Write([]byte(base))
	sign := base64.URLEncoding.EncodeToString(mac.Sum(nil))

	return base + "&token=" + s.accessKey + ":" + sign, nil
}

// Delete 调用七牛管理接口删除对象。
//
// 管理接口要求 QBox 签名：对「方法 + 路径 + 空 Content-Type + 空 body」
// 做 HMAC-SHA1，不是上传凭证那种 putPolicy 签名，两者不能混用。
func (s *QiniuStorage) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	entryURI := s.bucket + ":" + key
	encodedEntry := base64.URLEncoding.EncodeToString([]byte(entryURI))
	path := "/delete/" + encodedEntry

	mac := hmac.New(sha1.New, []byte(s.secretKey))
	mac.Write([]byte("POST " + path + "\n"))
	mac.Write([]byte(s.apiHost + "\n"))
	mac.Write([]byte("Content-Type: application/x-www-form-urlencoded\n\n"))
	sign := base64.URLEncoding.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiHost+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "QBox "+s.accessKey+":"+sign)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("qiniu: delete: %w", err)
	}
	defer resp.Body.Close()

	// 删除不存在的对象，七牛会返回 612，业务上视为成功。
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return nil
	}

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
	if strings.Contains(string(raw), "no such file") || strings.Contains(string(raw), "612") {
		return nil
	}
	return fmt.Errorf("qiniu: delete http %d: %s", resp.StatusCode, truncate(string(raw), 200))
}

// uploadToken 生成上传凭证。
//
// 格式：<AccessKey>:<EncodedSign>:<EncodedPutPolicy>
// 其中 Sign = HMAC-SHA1(SecretKey, EncodedPutPolicy)，
// 两个 base64 都必须是 URL-safe 且带 padding。
func (s *QiniuStorage) uploadToken(key string) string {
	policy := map[string]any{
		// scope 限定 bucket + key，凭证只能写这一个对象，
		// 泄露也无法覆盖别人的文件。
		"scope": s.bucket + ":" + key,
		// 一小时后过期：上传是同步动作，不需要更长的窗口。
		"deadline": time.Now().Add(time.Hour).Unix(),
	}

	policyJSON, _ := json.Marshal(policy)
	encodedPolicy := base64.URLEncoding.EncodeToString(policyJSON)

	mac := hmac.New(sha1.New, []byte(s.secretKey))
	mac.Write([]byte(encodedPolicy))
	encodedSign := base64.URLEncoding.EncodeToString(mac.Sum(nil))

	return s.accessKey + ":" + encodedSign + ":" + encodedPolicy
}

func fileNameOf(key string) string {
	if idx := strings.LastIndex(key, "/"); idx >= 0 {
		return key[idx+1:]
	}
	return key
}

func escapeKey(key string) string {
	segments := strings.Split(key, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
