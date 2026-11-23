// Package hairstyle 负责发型素材库的加载、过滤与打分。
//
// 设计要点：
//  1. 素材库是 data/hairstyles/*.json，运营可直接改，不必重新编译；
//  2. 打分是纯函数，只依赖分析结果，同样输入必得同样输出；
//  3. 年龄段策略独立成 agePolicy，本期关闭的项只是没开，代码路径都在。
package hairstyle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Style 表示一款发型素材。
type Style struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // hair / makeup
	Name   string `json:"name"`
	Desc   string `json:"desc"`
	Cover  string `json:"cover"`
	Gender string `json:"gender"` // male / female

	// FitGender 允许中性款同时适配两个性别。
	FitGender []string `json:"fit_gender"`

	// FitShapes 命中脸型结论时加分。
	FitShapes []string `json:"fit_shapes"`

	// FitHairline 命中发际线形态时加分。
	FitHairline []string `json:"fit_hairline"`

	// AvoidShapes 是明确不建议的脸型：命中即扣分。
	// 加分只能说“这款不错”，减分才能说“这款对你不合适”。
	AvoidShapes []string `json:"avoid_shapes"`

	// AvoidHairline 是明确不建议的发际线形态。
	AvoidHairline []string `json:"avoid_hairline"`

	// FitHairlineLevel 命中发际线高度档位（偏低/适中/偏高）时加分。
	FitHairlineLevel []string `json:"fit_hairline_level"`

	// AvoidHairlineLevel 是与发际线高度相冲的款式。
	AvoidHairlineLevel []string `json:"avoid_hairline_level"`

	// FitDensity 命中发量档位时加分。
	FitDensity []string `json:"fit_density"`

	// FitTone 命中肤色冷暖时加分。
	FitTone []string `json:"fit_tone"`

	// FitAge 可选年龄段，为空表示不限。
	FitAge []string `json:"fit_age"`

	// Requires 表示这款发型需要哪些处理：cut / perm / color。
	// 保留 color、perm 枚举，本期只是策略上不给 u18 推荐。
	Requires []string `json:"requires"`

	// SchoolFriendly 标记是否适合校园场景，u18 优先。
	SchoolFriendly bool `json:"school_friendly"`

	Tags []string `json:"tags"`

	// PromptHint / NegativeHint 供后续图像合成使用，本期不调用，
	// 但必须先落库，否则将来要回头补全素材。
	PromptHint   string `json:"prompt_hint"`
	NegativeHint string `json:"negative_hint"`

	// VolumeWeight 描述这款发型对发量的要求，供合成参数使用。
	VolumeWeight float64 `json:"volume_weight"`
}

// Library 是一组发型素材。
type Library struct {
	items []Style
}

// Load 从目录加载所有 *.json 素材。
// 目录不存在时返回错误，避免静默启动成"没有发型可选"的空系统。
func Load(dir string) (*Library, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read hairstyle dir: %w", err)
	}

	var items []Style
	seen := map[string]struct{}{}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}

		var batch []Style
		if err := json.Unmarshal(raw, &batch); err != nil {
			return nil, fmt.Errorf("parse %s: %w", entry.Name(), err)
		}

		for _, s := range batch {
			if s.ID == "" || s.Name == "" {
				return nil, fmt.Errorf("%s: style missing id or name", entry.Name())
			}
			if _, dup := seen[s.ID]; dup {
				return nil, fmt.Errorf("duplicate style id: %s", s.ID)
			}
			seen[s.ID] = struct{}{}
			items = append(items, s)
		}
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no hairstyle definitions found in %s", dir)
	}

	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return &Library{items: items}, nil
}

// All 返回全部素材（只读副本）。
func (l *Library) All() []Style {
	out := make([]Style, len(l.items))
	copy(out, l.items)
	return out
}

// Find 按 ID 查找素材。
func (l *Library) Find(id string) (Style, bool) {
	for _, s := range l.items {
		if s.ID == id {
			return s, true
		}
	}
	return Style{}, false
}

// AgePolicy 描述某个年龄段允许的染烫范围。
// 本期 u18 关闭染发与烫发推荐，但素材与代码路径全部保留，
// 后续放开只需改配置中的布尔值。
type AgePolicy struct {
	AllowColor bool
	AllowPerm  bool
}

var agePolicies = map[string]AgePolicy{
	"u18":   {AllowColor: false, AllowPerm: false},
	"18_25": {AllowColor: true, AllowPerm: true},
	"26_35": {AllowColor: true, AllowPerm: true},
	"36_45": {AllowColor: true, AllowPerm: true},
	"46_55": {AllowColor: true, AllowPerm: true},
	"56p":   {AllowColor: true, AllowPerm: true},
}

// policyFor 返回年龄段策略；年龄缺失时按成年处理。
func policyFor(ageGroup string) AgePolicy {
	if p, ok := agePolicies[ageGroup]; ok {
		return p
	}
	return AgePolicy{AllowColor: true, AllowPerm: true}
}

// Candidate 是一次推荐请求的输入条件。
type Candidate struct {
	Gender    string
	AgeGroup  string
	FaceShape string
	Hairline  string

	// HairlineLevel 是发际线高度档位（偏低/适中/偏高）。
	// 它比“形态”更能决定能不能露额，因此单独成一个打分项。
	HairlineLevel string

	Density     string
	SkinTone    string
	Unavailable bool
}

// Recommendation 是推荐结果。
type Recommendation struct {
	Style
	Score   float64  `json:"score"`
	Reasons []string `json:"reasons"`
}

// Recommend 按性别硬过滤，再按分析结果打分，返回前 limit 条。
// 排序使用分数降序 + ID 升序，保证同分时顺序稳定，
// 用户反复刷新不会看到推荐列表跳来跳去。
func (l *Library) Recommend(c Candidate, limit int) []Recommendation {
	if limit <= 0 {
		limit = 5
	}

	policy := policyFor(c.AgeGroup)
	out := make([]Recommendation, 0, len(l.items))

	for _, s := range l.items {
		if s.Kind != "hair" {
			continue
		}

		if !genderMatches(s, c.Gender) {
			continue
		}

		if !requiresAllowed(s, policy) {
			continue
		}

		score, reasons := scoreStyle(s, c)

		// 被负分命中的素材不能被兜底逻辑抬回来，
		// 否则“明确不建议”就失效了。
		penalized := (c.FaceShape != "" && contains(s.AvoidShapes, c.FaceShape)) ||
			(c.Hairline != "" && contains(s.AvoidHairline, c.Hairline)) ||
			(c.HairlineLevel != "" && contains(s.AvoidHairlineLevel, c.HairlineLevel))

		if !penalized && score <= 0 && len(s.FitShapes) == 0 && len(s.FitHairline) == 0 {
			// 没有任何适配信息的素材降为兜底项，而不是直接丢弃。
			score = 0.5
			reasons = []string{"通用造型参考"}
		}

		out = append(out, Recommendation{
			Style:   s,
			Score:   round2(score),
			Reasons: reasons,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})

	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func genderMatches(s Style, gender string) bool {
	if gender == "" {
		return true
	}
	if len(s.FitGender) > 0 {
		for _, g := range s.FitGender {
			if g == gender {
				return true
			}
		}
		return false
	}
	return s.Gender == gender
}

// requiresAllowed 按年龄段策略过滤染烫类发型。
func requiresAllowed(s Style, policy AgePolicy) bool {
	for _, r := range s.Requires {
		switch r {
		case "color":
			if !policy.AllowColor {
				return false
			}
		case "perm":
			if !policy.AllowPerm {
				return false
			}
		}
	}
	return true
}

// scoreStyle 计算适配分并生成推荐理由。
// 加分：脸型 3.0 > 发际线形态 1.5 > 发际线高度 1.2 > 肤色 0.8 > 发量 0.6。
// 减分：脸型不建议 -2.0，发际线形态不建议 -1.0，发际线高度不建议 -1.2。
func scoreStyle(s Style, c Candidate) (float64, []string) {
	var score float64
	reasons := make([]string, 0, 3)

	if c.FaceShape != "" && contains(s.FitShapes, c.FaceShape) {
		score += 3.0
		reasons = append(reasons, fmt.Sprintf("与%s的轮廓关系比较协调", c.FaceShape))
	}

	// 负分不生成理由：推荐页只展示正向依据，
	// 不把“不适合”写出来刺用户。
	if c.FaceShape != "" && contains(s.AvoidShapes, c.FaceShape) {
		score -= 2.0
	}

	if c.Hairline != "" && contains(s.FitHairline, c.Hairline) {
		score += 1.5
		reasons = append(reasons, fmt.Sprintf("和%s的发际线走向更贴合", c.Hairline))
	}

	if c.Hairline != "" && contains(s.AvoidHairline, c.Hairline) {
		score -= 1.0
	}

	if c.HairlineLevel != "" && contains(s.FitHairlineLevel, c.HairlineLevel) {
		score += 1.2
		reasons = append(reasons, hairlineLevelReason(c.HairlineLevel))
	}

	if c.HairlineLevel != "" && contains(s.AvoidHairlineLevel, c.HairlineLevel) {
		score -= 1.2
	}

	if c.SkinTone != "" && contains(s.FitTone, c.SkinTone) {
		score += 0.8
		reasons = append(reasons, fmt.Sprintf("与你的色彩方向（%s）一致", c.SkinTone))
	}

	if c.Density != "" && contains(s.FitDensity, c.Density) {
		score += 0.6
		reasons = append(reasons, fmt.Sprintf("适合%s的发量基础", c.Density))
	}

	// u18 优先校园场景：这不是加分项，是排序稳定性的来源，
	// 让同一套打分在青少年场景下也能自然把合适的款排前面。
	if c.AgeGroup == "u18" && s.SchoolFriendly {
		score += 0.4
		reasons = append(reasons, "款式不夸张，日常上学也合适")
	}

	return score, reasons
}

// hairlineLevelReason 把发际线高度翻译成一句人话。
// 不直接说“发际线偏高”，避免像在指出缺点。
func hairlineLevelReason(level string) string {
	switch level {
	case "偏低":
		return "额头留白比较充分，这款的分线位置比较合适"
	case "偏高":
		return "这款的额前处理能让上庭比例更协调"
	default:
		return "发际线高度与这款的额前比例比较接近"
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
