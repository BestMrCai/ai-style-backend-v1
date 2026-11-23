package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ai-style-backend/internal/hairstyle"
	"ai-style-backend/internal/repository"
)

var ErrAnalysisNotReady = errors.New("analysis result is not ready")

// HairstyleService 把面部分析结果翻译成发型推荐。
type HairstyleService struct {
	analysisRepo *repository.FaceAnalysisRepository
	library      *hairstyle.Library
}

func NewHairstyleService(
	analysisRepo *repository.FaceAnalysisRepository,
	library *hairstyle.Library,
) *HairstyleService {
	return &HairstyleService{
		analysisRepo: analysisRepo,
		library:      library,
	}
}

// RecommendationItem 是对外返回的推荐条目。
type RecommendationItem struct {
	StyleID     string   `json:"style_id"`
	Name        string   `json:"name"`
	Desc        string   `json:"desc"`
	CoverURL    string   `json:"cover_url"`
	Tags        []string `json:"tags"`
	Score       float64  `json:"score"`
	Reasons     []string `json:"reasons"`
	Requires    []string `json:"requires"`
	Gender      string   `json:"gender"`
	SchoolReady bool     `json:"school_friendly"`
}

// Recommend 按分析记录生成推荐列表。
// 分析未完成或缺少性别时返回明确错误，不猜测默认值。
func (s *HairstyleService) Recommend(
	ctx context.Context,
	userID int64,
	analysisID int64,
	limit int,
) ([]RecommendationItem, *hairstyle.Candidate, error) {
	analysis, err := s.analysisRepo.FindByIDForUser(ctx, userID, analysisID)
	if err != nil {
		return nil, nil, err
	}

	if analysis.Status != "succeeded" || len(analysis.ResultJSON) == 0 {
		return nil, nil, ErrAnalysisNotReady
	}

	if analysis.Gender == nil || *analysis.Gender == "" {
		// 老数据没有性别：让前端补选，而不是默认按某一性别推荐。
		return nil, nil, ErrAnalysisNotReady
	}

	candidate := candidateFrom(analysis)

	items := s.library.Recommend(candidate, limit)
	out := make([]RecommendationItem, 0, len(items))
	for _, item := range items {
		tags := item.Tags
		if tags == nil {
			tags = []string{}
		}
		reasons := item.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		requires := item.Requires
		if requires == nil {
			requires = []string{}
		}

		out = append(out, RecommendationItem{
			StyleID:     item.ID,
			Name:        item.Name,
			Desc:        item.Desc,
			CoverURL:    s.coverURL(item.ID),
			Tags:        tags,
			Score:       item.Score,
			Reasons:     reasons,
			Requires:    requires,
			Gender:      item.Gender,
			SchoolReady: item.SchoolFriendly,
		})
	}

	return out, &candidate, nil
}

// coverURL 生成封面访问地址。
// 封面不在小程序主包里（主包有 2MB 限制），统一走后端静态文件接口。
func (s *HairstyleService) coverURL(styleID string) string {
	if styleID == "" {
		return ""
	}
	return "/api/v1/hairstyles/" + styleID + "/cover"
}

// candidateFrom 从分析结果中抽取打分所需字段。
// 只读取报告中已有的字段，缺失就是空字符串，由打分逻辑自然降级。
func candidateFrom(analysis *repository.FaceAnalysis) hairstyle.Candidate {
	c := hairstyle.Candidate{}

	if analysis.Gender != nil {
		c.Gender = *analysis.Gender
	}
	if analysis.AgeGroup != nil {
		c.AgeGroup = *analysis.AgeGroup
	}

	var report struct {
		Findings struct {
			FaceShape struct {
				Value string `json:"value"`
			} `json:"face_shape"`
		} `json:"findings"`
		Hair struct {
			HairlineShape string `json:"hairline_shape"`
			HairlineLevel string `json:"hairline_height_level"`
			Density       string `json:"density"`
			Available     bool   `json:"available"`
		} `json:"hair"`
		Color struct {
			Tone      string         `json:"tone"`
			Available bool           `json:"available"`
			HairTone  string         `json:"hair_tone"`
			Skin      map[string]any `json:"skin"`
		} `json:"color"`
	}

	if err := json.Unmarshal(analysis.ResultJSON, &report); err == nil {
		c.FaceShape = report.Findings.FaceShape.Value
		// "照片中暂无法判断" 不是可用的发际线形态，不能参与打分。
		if report.Hair.Available && report.Hair.HairlineShape != "照片中暂无法判断" {
			c.Hairline = report.Hair.HairlineShape
		}
		// 高度档位与形态相互独立：形态常因刘海取不到，
		// 但高度只要上庭比例有值就能用。
		c.HairlineLevel = report.Hair.HairlineLevel
		// 发量三档：取不到就是空串，打分自然降级。
		c.Density = report.Hair.Density
		if report.Color.Available {
			c.SkinTone = report.Color.Tone
		}
	}

	return c
}

// Describe 生成一句可直接展示的推荐摘要，用于结果页标题区。
func Describe(c hairstyle.Candidate) string {
	if c.FaceShape == "" {
		return "按性别与日常场景为你挑选了这几款"
	}
	if c.Hairline != "" {
		return fmt.Sprintf("结合%s与%s，这几款的轮廓关系更协调", c.FaceShape, c.Hairline)
	}
	return fmt.Sprintf("结合%s的轮廓，这几款更协调", c.FaceShape)
}

// StyleName 返回某款发型的展示名，用于兼容中文文件名的封面图。
// 找不到时返回空字符串，由调用方决定回落策略。
func (s *HairstyleService) StyleName(styleID string) string {
	if s.library == nil || styleID == "" {
		return ""
	}

	style, ok := s.library.Find(styleID)
	if !ok {
		return ""
	}
	return style.Name
}
