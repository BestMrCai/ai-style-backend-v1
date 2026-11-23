package ai

import (
	"context"
	"errors"
)

// MockGenerator 是模拟 AI 实现。
// 它不生成图片，只返回结构化的设计说明，用于在一期跑通全链路：
// 推荐 → 建任务 → 后台处理 → 结果页展示。
// 真实图像合成的接入点是另一个 Generator 实现，接口不变。
type MockGenerator struct{}

// NewMockGenerator 创建模拟 AI 服务。
func NewMockGenerator() *MockGenerator {
	return &MockGenerator{}
}

// Generate 根据请求返回结构化的发型设计建议。
//
// 注意：这里刻意把分析条件（脸型、发际线、发量、性别、年龄段）
// 原样写回结果，这样在接入真实模型之前，
// 就能在结果页确认"条件确实传到了生成层"，而不是中途丢失。
func (g *MockGenerator) Generate(
	ctx context.Context,
	request GenerateRequest,
) (*GenerateResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(request.Portrait) == 0 {
		return nil, errors.New("portrait image is required")
	}

	suggestion := map[string]any{
		"title":       "发型设计方案",
		"design_type": request.DesignType,
		"is_mock":     true,
		"message":     "当前为方案预览，效果图合成能力尚未开启。",
	}

	switch request.DesignType {
	case "hairstyle":
		suggestion["title"] = "发型设计方案"
		suggestion["hairstyle"] = map[string]any{
			"name": request.StyleName,
			"reason": buildReason(request),
			"basis": map[string]any{
				"face_shape":       request.FaceShape,
				"hairline":         request.Hairline,
				"hairline_percent": request.HairlineHeightPercent,
				"density":          request.Density,
				"gender":           request.Gender,
				"age_group":        request.AgeGroup,
			},
			"styling":     "吹干后少量造型产品抓出层次即可，不需要复杂打理。",
			"maintenance": "建议每 4 至 6 周修剪一次，保持轮廓。",
		}

	case "outfit":
		suggestion["title"] = "穿搭设计方案"
		suggestion["outfit"] = map[string]any{
			"style":         "简约休闲",
			"reason":        "整体搭配自然舒适，适合日常穿着。",
			"top":           "简约纯色上衣",
			"bottom":        "合身直筒长裤",
			"shoes":         "简约休闲鞋",
			"color_scheme":  "基础色系搭配",
			"accessories":   "简约日常配饰",
			"matching_tips": "上下装保持色调协调，避免过多复杂元素。",
		}

	case "full_style":
		suggestion["title"] = "整体形象方案"
		suggestion["full_style"] = map[string]any{
			"overall_style": "简约清爽",
			"reason":        "发型与服装保持统一，整体协调自然。",
			"hairstyle": map[string]any{
				"name":   request.StyleName,
				"reason": buildReason(request),
			},
			"outfit": map[string]any{
				"style":  "简约休闲",
				"reason": "与发型保持协调。",
			},
			"color_matching": "建议采用基础色系搭配。",
			"accessories":    "选择简约、低调的日常配饰。",
		}

	default:
		return nil, errors.New("unsupported design type")
	}

	return &GenerateResult{Suggestion: suggestion}, nil
}

// buildReason 依据真实分析条件拼装推荐理由。
// 只用已经拿到的数据，缺什么就不提什么，
// 避免出现"报告说是圆脸、理由里写方脸"这类自相矛盾。
func buildReason(request GenerateRequest) string {
	parts := make([]string, 0, 3)

	if request.FaceShape != "" {
		parts = append(parts, "与"+request.FaceShape+"的轮廓关系比较协调")
	}
	if request.Hairline != "" {
		parts = append(parts, "和"+request.Hairline+"的发际线走向更贴合")
	}
	if request.Density != "" {
		parts = append(parts, "适合"+request.Density+"的发量基础")
	}

	if len(parts) == 0 {
		return "这款整体线条干净，适配度比较广。"
	}

	reason := parts[0]
	for _, p := range parts[1:] {
		reason += "，" + p
	}
	return reason + "。"
}
