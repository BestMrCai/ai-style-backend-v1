package face

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"strings"
	"time"
)

// ErrUnavailable 表示面部检测服务不可用。
var ErrUnavailable = errors.New("face server unavailable")

// Result 是一次面部检测的产物。
type Result struct {
	// Report 是 Python 服务返回的完整报告 JSON，原样保存。
	Report json.RawMessage
	// Overlay 是已经绘制好标注的 JPEG。
	Overlay []byte
}

// Client 调用 Python face-server 的 HTTP 接口。
type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.baseURL != ""
}

// Health 检查 Python 服务与检测模型状态。
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	if !c.Enabled() {
		return nil, ErrUnavailable
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: health status %d", ErrUnavailable, resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("%w: invalid health response", ErrUnavailable)
	}
	return body, nil
}

// Analyze 把原图交给 Python 服务分析，并取回标注图。
func (c *Client) Analyze(ctx context.Context, image []byte) (*Result, error) {
	if !c.Enabled() {
		return nil, ErrUnavailable
	}
	if len(image) == 0 {
		return nil, errors.New("empty image")
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="photo.jpg"`)
	header.Set("Content-Type", "image/jpeg")

	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(image); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/api/analyze",
		&buf,
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read response", ErrUnavailable)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"%w: analyze status %d: %s",
			ErrUnavailable,
			resp.StatusCode,
			truncate(string(body), 300),
		)
	}

	var report struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Files struct {
			Overlay string `json:"overlay"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, fmt.Errorf("%w: invalid json", ErrUnavailable)
	}
	if !report.OK {
		msg := report.Error
		if msg == "" {
			msg = "analysis failed"
		}
		return nil, errors.New(msg)
	}

	result := &Result{Report: json.RawMessage(body)}

	if report.Files.Overlay != "" {
		overlay, err := c.fetchOverlay(ctx, report.Files.Overlay)
		if err != nil {
			return nil, err
		}
		result.Overlay = overlay
	}

	return result, nil
}

func (c *Client) fetchOverlay(ctx context.Context, ref string) ([]byte, error) {
	url := ref
	if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
		url = c.baseURL + "/" + strings.TrimLeft(path.Clean("/"+ref), "/")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch overlay", ErrUnavailable)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: overlay status %d", ErrUnavailable, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read overlay", ErrUnavailable)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty overlay", ErrUnavailable)
	}
	return data, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
