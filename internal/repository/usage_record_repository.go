package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 使用记录的两种计费方式。
const (
	ChargeFree   = "free"
	ChargeCredit = "credit"
)

// UsageRecord 是一条使用记录。
type UsageRecord struct {
	ID             int64
	Feature        string
	ChargeType     string
	CreditsCost    int
	FaceAnalysisID *int64
	DesignTaskID   *int64
	QuotaDate      time.Time
	RefundedAt     *time.Time
	CreatedAt      time.Time
}

// UsageRecordRepository 负责使用记录的写入与查询。
type UsageRecordRepository struct {
	db *pgxpool.Pool
}

func NewUsageRecordRepository(db *pgxpool.Pool) *UsageRecordRepository {
	return &UsageRecordRepository{db: db}
}

// Create 写入一条使用记录。
//
// 与分析/合成任务同一个事务调用会更好，但那需要把三个 Repository 的
// 事务串起来；这里选择"任务建成之后再记录"，失败也只是少一条记录，
// 不会出现"有记录没任务"的脏数据。
func (r *UsageRecordRepository) Create(
	ctx context.Context,
	userID int64,
	feature string,
	chargeType string,
	creditsCost int,
	faceAnalysisID *int64,
	designTaskID *int64,
	quotaDate time.Time,
) (int64, error) {
	var id int64
	err := r.db.QueryRow(ctx, `
		INSERT INTO usage_records (
			user_id, feature, charge_type, credits_cost,
			face_analysis_id, design_task_id, quota_date
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, userID, feature, chargeType, creditsCost,
		faceAnalysisID, designTaskID, quotaDate).Scan(&id)
	return id, err
}

// FindByDesignTask 按任务反查记录，用于失败退款。
func (r *UsageRecordRepository) FindByDesignTask(
	ctx context.Context,
	taskID int64,
) (*UsageRecord, error) {
	var item UsageRecord
	err := r.db.QueryRow(ctx, `
		SELECT id, feature, charge_type, credits_cost,
		       face_analysis_id, design_task_id, quota_date,
		       refunded_at, created_at
		FROM usage_records
		WHERE design_task_id = $1
	`, taskID).Scan(
		&item.ID, &item.Feature, &item.ChargeType, &item.CreditsCost,
		&item.FaceAnalysisID, &item.DesignTaskID, &item.QuotaDate,
		&item.RefundedAt, &item.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// FindByFaceAnalysis 按分析记录反查，用于失败退款。
func (r *UsageRecordRepository) FindByFaceAnalysis(
	ctx context.Context,
	analysisID int64,
) (*UsageRecord, error) {
	var item UsageRecord
	err := r.db.QueryRow(ctx, `
		SELECT id, feature, charge_type, credits_cost,
		       face_analysis_id, design_task_id, quota_date,
		       refunded_at, created_at
		FROM usage_records
		WHERE face_analysis_id = $1
	`, analysisID).Scan(
		&item.ID, &item.Feature, &item.ChargeType, &item.CreditsCost,
		&item.FaceAnalysisID, &item.DesignTaskID, &item.QuotaDate,
		&item.RefundedAt, &item.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// MarkRefunded 标记已退款。
// 返回 false 表示这条记录已经退过款，调用方不要重复退。
func (r *UsageRecordRepository) MarkRefunded(
	ctx context.Context,
	recordID int64,
) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE usage_records
		SET refunded_at = NOW()
		WHERE id = $1 AND refunded_at IS NULL
	`, recordID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListOptions 是查询参数。
// DateFrom / DateTo 都是闭区间，按业务时区自然日计算。
type ListOptions struct {
	DateFrom *time.Time
	DateTo   *time.Time
	Limit    int
	Offset   int
}

// List 返回当前用户的使用记录，按时间倒序。
func (r *UsageRecordRepository) List(
	ctx context.Context,
	userID int64,
	opts ListOptions,
) ([]UsageRecord, int, error) {
	if opts.Limit <= 0 || opts.Limit > 100 {
		opts.Limit = 20
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}

	// 两个日期都是可选条件，用 $n IS NULL 保持同一份 SQL，
	// 避免拼字符串带来的注入面和索引失效。
	var total int
	if err := r.db.QueryRow(ctx, `
		SELECT count(*)
		FROM usage_records
		WHERE user_id = $1
			AND ($2::date IS NULL OR quota_date >= $2::date)
			AND ($3::date IS NULL OR quota_date <= $3::date)
	`, userID, opts.DateFrom, opts.DateTo).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, feature, charge_type, credits_cost,
		       face_analysis_id, design_task_id, quota_date,
		       refunded_at, created_at
		FROM usage_records
		WHERE user_id = $1
			AND ($2::date IS NULL OR quota_date >= $2::date)
			AND ($3::date IS NULL OR quota_date <= $3::date)
		ORDER BY created_at DESC, id DESC
		LIMIT $4 OFFSET $5
	`, userID, opts.DateFrom, opts.DateTo, opts.Limit, opts.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]UsageRecord, 0, opts.Limit)
	for rows.Next() {
		var item UsageRecord
		if err := rows.Scan(
			&item.ID, &item.Feature, &item.ChargeType, &item.CreditsCost,
			&item.FaceAnalysisID, &item.DesignTaskID, &item.QuotaDate,
			&item.RefundedAt, &item.CreatedAt,
		); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

// Stats 汇总某段时间的消耗，给"我的"页做概览。
type UsageStats struct {
	FreeCount    int
	CreditCount  int
	CreditsSpent int
}

// Sum 统计区间内的使用次数与积分消耗。
func (r *UsageRecordRepository) Sum(
	ctx context.Context,
	userID int64,
	opts ListOptions,
) (UsageStats, error) {
	var stats UsageStats
	err := r.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN charge_type = 'free' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN charge_type = 'credit' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN refunded_at IS NULL THEN credits_cost ELSE 0 END), 0)
		FROM usage_records
		WHERE user_id = $1
			AND ($2::date IS NULL OR quota_date >= $2::date)
			AND ($3::date IS NULL OR quota_date <= $3::date)
	`, userID, opts.DateFrom, opts.DateTo).Scan(
		&stats.FreeCount, &stats.CreditCount, &stats.CreditsSpent)
	return stats, err
}
