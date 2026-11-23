package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	// image.Decode 依赖注册解码器。jpeg 由上面的 jpeg 包 init 注册，
	// png 必须显式引入，否则用户相册里的 PNG 会在 decode 阶段失败。
	_ "image/png"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"
)

// VolcGenerator 调用火山方舟（Ark）的 Seedream 图像生成接口。
//
// 设计取舍：
//  1. 只用标准库，不引第三方 SDK。方舟的 images/generations 是纯 REST，
//     引入 SDK 反而增加版本漂移与依赖体积。
//  2. 图片以 data URL 内联。MVP 阶段没有对象存储，内联比先传 OSS 简单；
//     代价是请求体变大，因此发送前统一缩到 maxSide 以内。
//  3. 方舟只返回 24 小时有效的临时 URL，必须在同一请求内下载并转存。
type VolcGenerator struct {
	apiKey  string
	model   string
	baseURL string
	size    string
	client  *http.Client
}

// NewVolcGenerator 创建方舟生成器。
// size 形如 1024x1280；留空时用 1024x1280（4:5，适合半身像）。
func NewVolcGenerator(apiKey, model, baseURL, size string, timeout time.Duration) *VolcGenerator {
	if baseURL == "" {
		baseURL = "https://ark.cn-beijing.volces.com/api/v3"
	}
	if size == "" {
		size = "1024x1280"
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &VolcGenerator{
		apiKey:  strings.TrimSpace(apiKey),
		model:   strings.TrimSpace(model),
		baseURL: strings.TrimRight(baseURL, "/"),
		size:    size,
		client:  &http.Client{Timeout: timeout},
	}
}

type arkImageRequest struct {
	Model                     string   `json:"model"`
	Prompt                    string   `json:"prompt"`
	Image                     []string `json:"image,omitempty"`
	Size                      string   `json:"size,omitempty"`
	SequentialImageGeneration string   `json:"sequential_image_generation,omitempty"`
	ResponseFormat            string   `json:"response_format,omitempty"`
	Watermark                 bool     `json:"watermark"`
}

type arkImageResponse struct {
	Data []struct {
		URL string `json:"url"`
	} `json:"data"`
	Usage struct {
		GeneratedImages int `json:"generated_images"`
		OutputTokens    int `json:"output_tokens"`
		TotalTokens     int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Generate 实现 ai.Generator。
func (g *VolcGenerator) Generate(ctx context.Context, req GenerateRequest) (*GenerateResult, error) {
	if len(req.Portrait) == 0 {
		return nil, errors.New("portrait image is required")
	}
	if g.apiKey == "" {
		return nil, errors.New("ARK_API_KEY is not configured")
	}
	if g.model == "" {
		return nil, errors.New("ARK model id is not configured")
	}

	// 发送前统一缩图：手机原图 5MB 内联成 base64 后接近 7MB，
	// 请求体过大时方舟会直接断开连接。
	portrait, err := downscaleJPEG(req.Portrait, 1280)
	if err != nil {
		return nil, fmt.Errorf("prepare portrait: %w", err)
	}

	images := []string{toDataURL(portrait)}
	for _, ref := range req.References {
		if len(ref) == 0 {
			continue
		}
		refData, err := downscaleJPEG(ref, 1280)
		if err != nil {
			// 参考图坏了不该阻断主流程，跳过即可
			log.Printf("volc: skip broken reference: %v", err)
			continue
		}
		images = append(images, toDataURL(refData))
	}

	// 画幅必须跟着原图走：固定输出比例会让模型为了填满画布重新构图，
	// 表现为人物被放大、被拉宽。g.size 降级为兜底默认值。
	size := pickSize(req.Portrait, g.size)

	payload := arkImageRequest{
		Model:  g.model,
		Prompt: buildVolcPrompt(req),
		Image:  images,
		Size:   size,
		// 强制单图：不配置时方舟可能按组图生成长图序列
		SequentialImageGeneration: "disabled",
		ResponseFormat:            "url",
		Watermark:                 false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode ark request: %w", err)
	}

	resp, err := g.post(ctx, body)
	if err != nil {
		// 画幅是按原图算出来的，万一模型不接受这个尺寸，
		// 退回配置里的默认值重试一次，不要让整条任务失败。
		if size != g.size && isSizeError(err) {
			log.Printf("volc: size %s rejected (%v), retry with %s", size, err, g.size)
			payload.Size = g.size
			retryBody, marshalErr := json.Marshal(payload)
			if marshalErr != nil {
				return nil, fmt.Errorf("encode ark request: %w", marshalErr)
			}
			resp, err = g.post(ctx, retryBody)
		}
		if err != nil {
			return nil, err
		}
	}

	if len(resp.Data) == 0 || resp.Data[0].URL == "" {
		return nil, errors.New("ark returned no image")
	}

	imgBytes, err := g.download(ctx, resp.Data[0].URL)
	if err != nil {
		return nil, fmt.Errorf("download ark image: %w", err)
	}

	log.Printf("volc: generated image ok bytes=%d model=%s size=%s tokens=%d",
		len(imgBytes), g.model, size, resp.Usage.TotalTokens)

	return &GenerateResult{
		Suggestion:       buildVolcSuggestion(req),
		ImageData:        imgBytes,
		ImageContentType: http.DetectContentType(imgBytes),
	}, nil
}

// post 发送请求，仅对 429/5xx 重试一次。
// 其余错误直接返回：4xx 多为参数或审核问题，重试只会重复计费。
func (g *VolcGenerator) post(ctx context.Context, body []byte) (*arkImageResponse, error) {
	var lastErr error

	for attempt := 0; attempt < 2; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
			g.baseURL+"/images/generations", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)

		httpResp, err := g.client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("call ark: %w", err)
		}
		raw, readErr := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()

		if readErr != nil {
			return nil, fmt.Errorf("read ark response: %w", readErr)
		}

		if httpResp.StatusCode == http.StatusOK {
			var out arkImageResponse
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, fmt.Errorf("decode ark response: %w", err)
			}
			if out.Error != nil {
				return nil, fmt.Errorf("ark error %s: %s", out.Error.Code, out.Error.Message)
			}
			return &out, nil
		}

		msg := truncate(string(raw), 400)
		lastErr = fmt.Errorf("ark http %d: %s", httpResp.StatusCode, msg)

		retryable := httpResp.StatusCode == http.StatusTooManyRequests || httpResp.StatusCode >= 500
		if !retryable {
			return nil, lastErr
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}

	return nil, lastErr
}

func (g *VolcGenerator) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download http %d", resp.StatusCode)
	}
	// 单张效果图不应超过 10MB，超过视为异常，避免把磁盘打满
	return io.ReadAll(io.LimitReader(resp.Body, 10<<20))
}

func toDataURL(data []byte) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
}

// allowedSizes 是 Seedream 4.0 明确支持的画幅白名单。
// 只在这些值里挑，不自由计算宽高：非白名单尺寸会被接口直接拒绝。
var allowedSizes = []struct {
	W, H int
}{
	{1024, 1024}, // 1:1
	{864, 1152},  // 3:4
	{1152, 864},  // 4:3
	{832, 1248},  // 2:3
	{1248, 832},  // 3:2
	{1280, 720},  // 16:9
	{720, 1280},  // 9:16
}

// pickSize 按原图宽高比挑一个最接近的受支持画幅。
//
// 为什么必须做：输出比例和输入差太多时，模型为了让主体填满画布会重新构图，
// 用户看到的就是"人物被放大 / 拉变形"。让输出比例贴着原图，
// 模型才有条件保持原始取景、人物大小和位置。
//
// fallback 是配置里的默认尺寸，解码失败时原样返回，保证老行为可用。
func pickSize(portrait []byte, fallback string) string {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(portrait))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return fallback
	}

	target := float64(cfg.Width) / float64(cfg.Height)

	best := allowedSizes[0]
	bestDiff := math.Abs(math.Log(target / (float64(best.W) / float64(best.H))))
	for _, item := range allowedSizes[1:] {
		ratio := float64(item.W) / float64(item.H)
		diff := math.Abs(math.Log(target / ratio))
		if diff < bestDiff {
			best, bestDiff = item, diff
		}
	}

	return fmt.Sprintf("%dx%d", best.W, best.H)
}

// isSizeError 判断失败是否因为画幅不被接受，
// 只有这种情况才值得用默认尺寸重试一次。
func isSizeError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, kw := range []string{"size", "resolution", "width", "height"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// buildVolcPrompt 把分析条件翻译成生成指令。
//
// 三条原则：
//  1. 先固定"脸不变"，再描述发型：Seedream 是整图重绘，
//     不写死脸就会被顺手改掉。
//  2. 只写确实取到的条件，缺项不补默认值。
//  3. 方舟没有 negative_prompt 参数，负向词只能折进正向描述。
func buildVolcPrompt(req GenerateRequest) string {
	var b strings.Builder

	b.WriteString("保持原图人物的五官、脸型、肤色、表情完全不变，只替换头发部分。")
	// 构图约束必须写死：Seedream 是整图重绘，不写就会被"优化"成标准人像，
	// 用户感知就是人物变大、被拉宽、背景换了。
	b.WriteString("保持原始构图与画幅比例，人物大小、位置、朝向、身体姿势与背景完全不变，")
	b.WriteString("不要放大或缩小人物，不要移动人物，不要改变取景范围。")
	b.WriteString("人物穿着白色圆领上衣，自然光，真实照片质感。")

	if req.PromptHint != "" {
		b.WriteString("新发型：")
		b.WriteString(req.PromptHint)
		b.WriteString("。")
	}

	if req.HairlineHeightPercent > 0 && req.HairlineHeightPercent < 100 {
		fmt.Fprintf(&b, "发际线上边界位于额头约 %.0f%% 处。", req.HairlineHeightPercent)
	}
	if req.Density != "" {
		fmt.Fprintf(&b, "发量观感为%s。", req.Density)
	}

	if req.NegativeHint != "" {
		b.WriteString("避免出现：")
		b.WriteString(req.NegativeHint)
		b.WriteString("。")
	}

	return b.String()
}

// buildVolcSuggestion 与 mock 生成器保持同一结构，
// 前端不需要为不同 provider 写两套渲染逻辑。
func buildVolcSuggestion(req GenerateRequest) map[string]any {
	return map[string]any{
		"title":       "发型设计效果图",
		"design_type": req.DesignType,
		"is_mock":     false,
		"hairstyle": map[string]any{
			"name":   req.StyleName,
			"reason": buildReason(req),
			"basis": map[string]any{
				"face_shape":       req.FaceShape,
				"hairline":         req.Hairline,
				"hairline_percent": req.HairlineHeightPercent,
				"density":          req.Density,
				"gender":           req.Gender,
				"age_group":        req.AgeGroup,
			},
			"styling":     "按该款的走向吹整即可，不需要复杂打理。",
			"maintenance": "建议每 4 至 6 周修剪一次，保持轮廓。",
		},
	}
}

// downscaleJPEG 把图片缩到长边不超过 maxSide，并重新编码为 JPEG。
//
// 用最近邻采样而非双三次：头像缩图对画质要求不高，
// 最近邻没有额外依赖，也足够快。
func downscaleJPEG(data []byte, maxSide int) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("invalid image size")
	}

	longest := w
	if h > longest {
		longest = h
	}

	out := src
	if longest > maxSide {
		ratio := float64(maxSide) / float64(longest)
		nw := int(float64(w) * ratio)
		nh := int(float64(h) * ratio)
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		for y := 0; y < nh; y++ {
			sy := b.Min.Y + y*h/nh
			for x := 0; x < nw; x++ {
				sx := b.Min.X + x*w/nw
				dst.Set(x, y, src.At(sx, sy))
			}
		}
		out = dst
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 88}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
