package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const wechatSessionEndpoint = "https://api.weixin.qq.com/sns/jscode2session"

var (
	ErrWeChatConfigMissing = errors.New("wechat configuration is missing")
	ErrLoginCodeEmpty      = errors.New("wechat login code cannot be empty")
	ErrWeChatAPI           = errors.New("wechat API request failed")
)

// WeChatClient 负责调用微信小程序开放接口。
type WeChatClient struct {
	appID      string
	appSecret  string
	httpClient *http.Client
	endpoint   string
}

// NewWeChatClient 创建微信接口客户端。
func NewWeChatClient(appID, appSecret string) *WeChatClient {
	return &WeChatClient{
		appID:     appID,
		appSecret: appSecret,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		endpoint: wechatSessionEndpoint,
	}
}

// sessionResponse 表示微信 jscode2session 接口的响应。
// session_key 只用于服务端后续需要的场景，不返回给小程序。
type sessionResponse struct {
	OpenID  string `json:"openid"`
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// ExchangeCode 使用微信临时登录凭证换取 OpenID。
func (w *WeChatClient) ExchangeCode(
	ctx context.Context,
	code string,
) (string, error) {
	if strings.TrimSpace(w.appID) == "" ||
		strings.TrimSpace(w.appSecret) == "" {
		return "", ErrWeChatConfigMissing
	}

	code = strings.TrimSpace(code)
	if code == "" {
		return "", ErrLoginCodeEmpty
	}

	query := url.Values{}
	query.Set("appid", w.appID)
	query.Set("secret", w.appSecret)
	query.Set("js_code", code)
	query.Set("grant_type", "authorization_code")

	requestURL := w.endpoint + "?" + query.Encode()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		requestURL,
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("create wechat request: %w", err)
	}

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: request could not be completed", ErrWeChatAPI)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"%w: unexpected HTTP status %d",
			ErrWeChatAPI,
			resp.StatusCode,
		)
	}

	var result sessionResponse

	if err := json.NewDecoder(
		io.LimitReader(resp.Body, 1<<20),
	).Decode(&result); err != nil {
		return "", fmt.Errorf("%w: invalid response", ErrWeChatAPI)
	}

	if result.ErrCode != 0 {
		return "", fmt.Errorf(
			"%w: WeChat error code %d",
			ErrWeChatAPI,
			result.ErrCode,
		)
	}

	if strings.TrimSpace(result.OpenID) == "" {
		return "", fmt.Errorf("%w: OpenID is missing", ErrWeChatAPI)
	}

	return result.OpenID, nil
}
