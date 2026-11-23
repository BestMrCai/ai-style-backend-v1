package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 255), uint8(y % 255), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// 服务端返回 1024x1280 时，请求体里的图必须已被缩到长边 1280 以内。
func TestVolcGenerateSendsImagesAndDownloads(t *testing.T) {
	var gotPrompt string
	var gotImages int
	var gotModel string

	resultBytes := makeJPEG(t, 64, 80)

	mux := http.NewServeMux()
	var server *httptest.Server

	mux.HandleFunc("/api/v3/images/generations", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}

		var body arkImageRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotPrompt = body.Prompt
		gotImages = len(body.Image)
		gotModel = body.Model

		for _, item := range body.Image {
			if !strings.HasPrefix(item, "data:image/jpeg;base64,") {
				t.Errorf("image should be inline jpeg data url, got %s", shortPrefix(item, 30))
			}
			raw := strings.TrimPrefix(item, "data:image/jpeg;base64,")
			if _, err := base64.StdEncoding.DecodeString(raw); err != nil {
				t.Errorf("payload is not valid base64: %v", err)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"` + server.URL + `/result.jpg"}],"usage":{"total_tokens":123}}`))
	})

	mux.HandleFunc("/result.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(resultBytes)
	})

	server = httptest.NewServer(mux)
	defer server.Close()

	gen := NewVolcGenerator("test-key", "doubao-seedream-4-0-test", server.URL+"/api/v3", "1024x1280", 30*time.Second)

	req := GenerateRequest{
		Portrait:              makeJPEG(t, 3000, 4000),
		References:            [][]byte{makeJPEG(t, 400, 500)},
		DesignType:            "hairstyle",
		StyleName:             "清爽层次碎盖",
		PromptHint:            "short textured crop",
		NegativeHint:          "buzz cut",
		FaceShape:             "鹅蛋脸",
		Hairline:              "圆弧上拱",
		HairlineHeightPercent: 29.1,
		Density:               "浓密",
	}

	res, err := gen.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if gotModel != "doubao-seedream-4-0-test" {
		t.Errorf("model passed through incorrectly: %s", gotModel)
	}
	if gotImages != 2 {
		t.Errorf("expected portrait + 1 reference, got %d", gotImages)
	}
	if !strings.Contains(gotPrompt, "只替换头发") {
		t.Errorf("prompt missing face-lock instruction: %s", gotPrompt)
	}
	if !strings.Contains(gotPrompt, "29%") {
		t.Errorf("prompt missing hairline percent: %s", gotPrompt)
	}
	if !strings.Contains(gotPrompt, "浓密") {
		t.Errorf("prompt missing density: %s", gotPrompt)
	}
	if len(res.ImageData) != len(resultBytes) {
		t.Errorf("result image not downloaded, got %d bytes", len(res.ImageData))
	}
	if res.Suggestion == nil {
		t.Fatal("suggestion should not be nil")
	}
}

// 方舟返回业务错误时要原样暴露 message，方便排查审核拒图。
func TestVolcGenerateSurfacesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"code":"InvalidParameter","message":"prompt rejected"}}`))
	}))
	defer server.Close()

	gen := NewVolcGenerator("k", "m", server.URL, "", 10*time.Second)
	_, err := gen.Generate(context.Background(), GenerateRequest{
		Portrait:   makeJPEG(t, 32, 32),
		DesignType: "hairstyle",
	})
	if err == nil || !strings.Contains(err.Error(), "prompt rejected") {
		t.Fatalf("expected api error to surface, got %v", err)
	}
}

// 401 是配置问题，不该重试；否则会白白多打一次。
func TestVolcGenerateDoesNotRetryOn4xx(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"Unauthorized","message":"bad key"}}`))
	}))
	defer server.Close()

	gen := NewVolcGenerator("k", "m", server.URL, "", 10*time.Second)
	_, err := gen.Generate(context.Background(), GenerateRequest{Portrait: makeJPEG(t, 32, 32)})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("4xx should not be retried, calls=%d", calls)
	}
}

// 429 允许重试一次，但最多一次。
func TestVolcGenerateRetriesOnceOn429(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"RateLimit","message":"slow down"}}`))
	}))
	defer server.Close()

	gen := NewVolcGenerator("k", "m", server.URL, "", 10*time.Second)
	_, err := gen.Generate(context.Background(), GenerateRequest{Portrait: makeJPEG(t, 32, 32)})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 2 {
		t.Errorf("429 should be retried exactly once, calls=%d", calls)
	}
}

// 缺 key 时要立刻失败，不要发出请求。
func TestVolcGenerateRequiresAPIKey(t *testing.T) {
	gen := NewVolcGenerator("", "m", "http://localhost:1", "", time.Second)
	_, err := gen.Generate(context.Background(), GenerateRequest{Portrait: makeJPEG(t, 32, 32)})
	if err == nil || !strings.Contains(err.Error(), "ARK_API_KEY") {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

// 超大图必须被缩到长边 1280 以内，否则请求体过大。
func TestDownscaleJPEGCapsLongSide(t *testing.T) {
	out, err := downscaleJPEG(makeJPEG(t, 3000, 4000), 1280)
	if err != nil {
		t.Fatalf("downscale: %v", err)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode downscaled: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > 1280 || b.Dy() > 1280 {
		t.Errorf("long side not capped: %dx%d", b.Dx(), b.Dy())
	}
}

// 非 JPEG（例如 PNG）也要能处理：手机相册里 PNG 很常见。
func TestDownscaleJPEGAcceptsPNG(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 40, 40))
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	out, err := downscaleJPEG(buf.Bytes(), 1280)
	if err != nil {
		t.Fatalf("downscale png: %v", err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Errorf("output should be jpeg: %v", err)
	}
}

// 画幅必须贴着原图比例：原图 413x626（0.66，接近 2:3）不能被塞进 4:5 画布，
// 否则模型会为了填满画布重新构图，表现为人物被放大、被拉宽。
func TestPickSizeMatchesPortraitAspect(t *testing.T) {
	cases := []struct {
		name string
		w, h int
		want string
	}{
		{"竖构图接近2比3", 413, 626, "832x1248"},
		{"标准3比4自拍", 900, 1200, "864x1152"},
		{"方图", 800, 800, "1024x1024"},
		{"横构图4比3", 1200, 900, "1152x864"},
		{"全屏竖构图", 1080, 1920, "720x1280"},
	}

	for _, c := range cases {
		got := pickSize(makeJPEG(t, c.w, c.h), "1024x1280")
		if got != c.want {
			t.Errorf("%s: pickSize(%dx%d) = %s, want %s", c.name, c.w, c.h, got, c.want)
		}
	}
}

// 解不开的图不能中断生成，退回配置里的默认尺寸。
func TestPickSizeFallsBackOnBrokenImage(t *testing.T) {
	if got := pickSize([]byte("not an image"), "1024x1280"); got != "1024x1280" {
		t.Errorf("expected fallback size, got %s", got)
	}
	if got := pickSize(nil, "1024x1280"); got != "1024x1280" {
		t.Errorf("expected fallback size for nil, got %s", got)
	}
}

// 只有"尺寸不被支持"才值得重试；网络或审核错误重试只会重复计费。
func TestIsSizeError(t *testing.T) {
	if !isSizeError(errors.New("ark http 400: invalid size for model")) {
		t.Error("size related error should be detected")
	}
	if !isSizeError(errors.New("image width exceeds limit")) {
		t.Error("width error should be detected")
	}
	if isSizeError(errors.New("prompt rejected by audit")) {
		t.Error("audit error should not be treated as size error")
	}
	if isSizeError(nil) {
		t.Error("nil should not be a size error")
	}
}

func shortPrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
