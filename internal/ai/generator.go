package ai

import "context"

// GenerateRequest 表示一次 AI 设计请求。
type GenerateRequest struct {
	// 本人照片。
	Portrait []byte

	// 参考照片，可以有多张。
	References [][]byte

	// 设计类型：hairstyle、outfit、full_style。
	DesignType string

	// 设计模式：auto、reference、styleset。
	DesignMode string

	// 用户填写的设计需求。
	UserRequirement string

	// 用户选择的具体风格。
	StyleCode string

	// ---- 以下字段用于"贴合用户真实特征"的合成 ----
	//
	// 这些值来自面部分析结果，属于显式条件而不是让模型自行猜测。
	// 本期生成器为 mock，不使用这些字段；接入真实模型时直接读取即可，
	// 不需要修改任务表或接口契约。

	// StyleID / StyleName 是本次选用的发型素材。
	StyleID   string
	StyleName string

	// FaceShape 脸型结论，例如 鹅蛋脸、圆脸。
	FaceShape string

	// Hairline 发际线形态，例如 圆弧上拱、平直。
	Hairline string

	// HairlineHeightPercent 发际线到眉间占脸长的比例（0-100）。
	// 用于把遮罩上边界对齐到实测位置，而不是让模型自由发挥。
	HairlineHeightPercent float64

	// Density 发量档位：稀疏 / 适中 / 浓密。
	Density string

	// Gender 与 AgeGroup 是本次任务的对象属性。
	Gender   string
	AgeGroup string

	// PromptHint / NegativeHint 来自素材库，离线写死，
	// 避免运行时由模型现编提示词导致结果不可控。
	PromptHint   string
	NegativeHint string

	// VolumeWeight 描述该发型对发量的要求，影响合成的蓬松度。
	VolumeWeight float64
}

// GenerateResult 表示 AI 返回的设计结果。
type GenerateResult struct {
	// 设计建议，使用 JSON 格式保存。
	Suggestion map[string]any

	// 生成的设计图片。
	// 当前先定义字段，后续接入图片生成模型时使用。
	ImageData []byte

	// 图片 MIME 类型，例如 image/png。
	ImageContentType string
}

// Generator 定义 AI 设计服务必须实现的方法。
type Generator interface {
	Generate(ctx context.Context, request GenerateRequest) (*GenerateResult, error)
}
