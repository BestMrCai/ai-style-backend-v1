package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 每日限额的两个功能维度。
const (
	QuotaFaceAnalysis    = "face_analysis"
	QuotaHairstyleDesign = "hairstyle_design"
)

// ErrQuotaExceeded 表示当天该功能的次数已用完。
var ErrQuotaExceeded = errors.New("daily quota exceeded")

// DailyQuotaRepository 负责每日次数的原子扣减与退回。
type DailyQuotaRepository struct {
	db *pgxpool.Pool
	// loc 决定"一天"从几点开始算。部署在 UTC 机器上时，
	// 必须显式给东八区，否则额度会在北京时间早上 8 点重置。
	loc *time.Location
}

func NewDailyQuotaRepository(db *pgxpool.Pool, loc *time.Location) *DailyQuotaRepository {
	if loc == nil {
		loc = time.UTC
	}
	return &DailyQuotaRepository{db: db, loc: loc}
}

// Today 返回按业务时区计算的当天日期。
func (r *DailyQuotaRepository) Today(now time.Time) time.Time {
	local := now.In(r.loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, r.loc)
}

// Consume 占用一次额度。
//
// 必须是单条语句完成"读-判断-写"：
//   - ON CONFLICT 让当天第一次调用自动建行；
//   - WHERE used < limit 让超额的那次直接不更新，
//     不会出现两个并发请求都读到 used=9 然后都写 10 的经典超卖；
//   - RETURNING 为空即代表额度不够，比先 SELECT 再 UPDATE 少一次往返，
//     也不需要在应用层加锁。
func (r *DailyQuotaRepository) Consume(
	ctx context.Context,
	userID int64,
	feature string,
	limit int,
	now time.Time,
) (int, error) {
	if userID <= 0 || limit <= 0 {
		return 0, ErrQuotaExceeded
	}

	var used int
	err := r.db.QueryRow(ctx, `
		INSERT INTO daily_usage_quotas (user_id, quota_date, feature, used)
		VALUES ($1, $2, $3, 1)
		ON CONFLICT (user_id, quota_date, feature)
		DO UPDATE SET
			used = daily_usage_quotas.used + 1,
			updated_at = NOW()
		WHERE daily_usage_quotas.used < $4
		RETURNING used
	`, userID, r.Today(now), feature, limit).Scan(&used)

	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrQuotaExceeded
	}
	if err != nil {
		return 0, err
	}

	return used, nil
}

// Release 退回一次额度，用于任务创建后立刻失败的情况。
//
// 不退回"分析失败"的额度：用户点了就是消耗了算力，
// 额度按"发起次数"计，和积分扣费口径保持一致。
func (r *DailyQuotaRepository) Release(
	ctx context.Context,
	userID int64,
	feature string,
	now time.Time,
) error {
	if userID <= 0 {
		return nil
	}

	_, err := r.db.Exec(ctx, `
		UPDATE daily_usage_quotas
		SET used = used - 1, updated_at = NOW()
		WHERE user_id = $1
			AND quota_date = $2
			AND feature = $3
			AND used > 0
	`, userID, r.Today(now), feature)
	return err
}

// Remaining 返回当天剩余次数，用于前端展示"今天还能用几次"。
func (r *DailyQuotaRepository) Remaining(
	ctx context.Context,
	userID int64,
	feature string,
	limit int,
	now time.Time,
) (int, error) {
	if userID <= 0 {
		return 0, nil
	}

	var used int
	err := r.db.QueryRow(ctx, `
		SELECT used
		FROM daily_usage_quotas
		WHERE user_id = $1 AND quota_date = $2 AND feature = $3
	`, userID, r.Today(now), feature).Scan(&used)

	if errors.Is(err, pgx.ErrNoRows) {
		return limit, nil
	}
	if err != nil {
		return 0, err
	}

	if used >= limit {
		return 0, nil
	}
	return limit - used, nil
}
