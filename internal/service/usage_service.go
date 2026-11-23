package service

import (
	"context"
	"errors"
	"time"

	"ai-style-backend/internal/repository"
)

// ErrInvalidUsageQuery 表示查询参数不合法（例如日期格式错、区间倒置）。
var ErrInvalidUsageQuery = errors.New("invalid usage query")

// UsageService 提供使用记录的查询能力。
//
// 只读：写入由 QuotaService 在扣费时完成，
// 避免"两个地方都能写"导致口径不一致。
type UsageService struct {
	repo *repository.UsageRecordRepository
	loc  *time.Location
}

func NewUsageService(
	repo *repository.UsageRecordRepository,
	loc *time.Location,
) *UsageService {
	if loc == nil {
		loc = time.UTC
	}
	return &UsageService{repo: repo, loc: loc}
}

// UsageItem 是对外返回的一条记录。
type UsageItem struct {
	ID          int64  `json:"id"`
	Feature     string `json:"feature"`
	ChargeType  string `json:"charge_type"`
	CreditsCost int    `json:"credits_cost"`
	AnalysisID  *int64 `json:"analysis_id,omitempty"`
	TaskID      *int64 `json:"task_id,omitempty"`
	QuotaDate   string `json:"quota_date"`
	Refunded    bool   `json:"refunded"`
	CreatedAt   string `json:"created_at"`
}

// UsageSummary 是区间内的汇总，给"我的"页做概览。
type UsageSummary struct {
	Total        int `json:"total"`
	FreeCount    int `json:"free_count"`
	CreditCount  int `json:"credit_count"`
	CreditsSpent int `json:"credits_spent"`
}

// UsageList 是带汇总的列表结果。
type UsageList struct {
	Items   []UsageItem  `json:"items"`
	Total   int          `json:"total"`
	Summary UsageSummary `json:"summary"`
}

// List 按日期区间查询使用记录。
//
// dateFrom / dateTo 形如 2026-11-19，闭区间，空字符串表示不限。
// 日期按业务时区解释，和额度表保持一致。
func (s *UsageService) List(
	ctx context.Context,
	userID int64,
	dateFrom string,
	dateTo string,
	limit int,
	offset int,
) (*UsageList, error) {
	if userID <= 0 {
		return nil, ErrInvalidUsageQuery
	}

	from, err := s.parseDate(dateFrom)
	if err != nil {
		return nil, err
	}
	to, err := s.parseDate(dateTo)
	if err != nil {
		return nil, err
	}

	// 区间倒置大概率是前端把参数传反了，直接报错比返回空列表更好排查。
	if from != nil && to != nil && from.After(*to) {
		return nil, ErrInvalidUsageQuery
	}

	opts := repository.ListOptions{
		DateFrom: from,
		DateTo:   to,
		Limit:    limit,
		Offset:   offset,
	}

	records, total, err := s.repo.List(ctx, userID, opts)
	if err != nil {
		return nil, err
	}

	stats, err := s.repo.Sum(ctx, userID, opts)
	if err != nil {
		return nil, err
	}

	items := make([]UsageItem, 0, len(records))
	for _, r := range records {
		item := UsageItem{
			ID:          r.ID,
			Feature:     r.Feature,
			ChargeType:  r.ChargeType,
			CreditsCost: r.CreditsCost,
			AnalysisID:  r.FaceAnalysisID,
			TaskID:      r.DesignTaskID,
			// 用记录本身的日期而不是 created_at 的本地化时间：
			// 跨时区展示时两者会差一天，用户看到的应该是业务日。
			QuotaDate: r.QuotaDate.Format("2006-01-02"),
			Refunded:  r.RefundedAt != nil,
			CreatedAt: r.CreatedAt.In(s.loc).Format(time.RFC3339),
		}
		items = append(items, item)
	}

	return &UsageList{
		Items: items,
		Total: total,
		Summary: UsageSummary{
			Total:        stats.FreeCount + stats.CreditCount,
			FreeCount:    stats.FreeCount,
			CreditCount:  stats.CreditCount,
			CreditsSpent: stats.CreditsSpent,
		},
	}, nil
}

// parseDate 解析 YYYY-MM-DD，空串返回 nil（表示不限）。
func (s *UsageService) parseDate(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}

	t, err := time.ParseInLocation("2006-01-02", value, s.loc)
	if err != nil {
		return nil, ErrInvalidUsageQuery
	}
	return &t, nil
}
