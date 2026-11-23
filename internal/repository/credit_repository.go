package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInsufficientCredits 表示积分余额不足。
var ErrInsufficientCredits = errors.New("insufficient credits")

// CreditRepository 负责积分余额与流水。
//
// 两条不变式：
//  1. 余额只能通过 credit_transactions 变动，不直接改 user_credits.balance；
//     否则账对不上。
//  2. 扣减必须在一个事务里完成"校验余额 + 更新余额 + 写流水"，
//     靠 WHERE balance >= cost 让并发请求排队，而不是先查再改。
type CreditRepository struct {
	db *pgxpool.Pool
}

func NewCreditRepository(db *pgxpool.Pool) *CreditRepository {
	return &CreditRepository{db: db}
}

// Balance 返回当前余额，没有账户记录时视为 0。
func (r *CreditRepository) Balance(ctx context.Context, userID int64) (int, error) {
	if userID <= 0 {
		return 0, ErrInsufficientCredits
	}

	var balance int
	err := r.db.QueryRow(ctx, `
		SELECT balance FROM user_credits WHERE user_id = $1
	`, userID).Scan(&balance)

	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return balance, nil
}

// Spend 扣减积分并写一条 consume 流水。
//
// idempotencyKey 由调用方给出（例如 "design_task:123"），
// 重复提交时唯一索引会拦下来，不会重复扣费。
func (r *CreditRepository) Spend(
	ctx context.Context,
	userID int64,
	amount int,
	idempotencyKey string,
) error {
	if userID <= 0 || amount <= 0 || idempotencyKey == "" {
		return ErrInsufficientCredits
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 余额不足时 RowsAffected = 0，不需要先 SELECT 再判断。
	tag, err := tx.Exec(ctx, `
		UPDATE user_credits
		SET balance = balance - $2, updated_at = NOW()
		WHERE user_id = $1 AND balance >= $2
	`, userID, amount)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInsufficientCredits
	}

	if err := writeCreditTx(ctx, tx, userID, -amount, "consume", idempotencyKey); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// Refund 退回积分并写一条 refund 流水。
//
// 用 refund:<原幂等键> 作为新键：原键已被 consume 占用，
// 直接复用会撞唯一索引。这样既能防重复退款，也留有追溯线索。
func (r *CreditRepository) Refund(
	ctx context.Context,
	userID int64,
	amount int,
	originKey string,
) error {
	if userID <= 0 || amount <= 0 || originKey == "" {
		return nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 没有余额记录时先建一行，避免老用户退款失败。
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_credits (user_id, balance)
		VALUES ($1, 0)
		ON CONFLICT (user_id) DO NOTHING
	`, userID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE user_credits
		SET balance = balance + $2, updated_at = NOW()
		WHERE user_id = $1
	`, userID, amount); err != nil {
		return err
	}

	if err := writeCreditTx(ctx, tx, userID, amount, "refund", "refund:"+originKey); err != nil {
		// 已经退过就当作成功：重复退款请求不该报错给用户。
		if isUniqueViolation(err) {
			return nil
		}
		return err
	}

	return tx.Commit(ctx)
}

// Grant 送积分（注册礼、看广告奖励等）。目前没有调用方，
// 接口先留好，等积分中心上线直接接。
func (r *CreditRepository) Grant(
	ctx context.Context,
	userID int64,
	amount int,
	transactionType string,
	idempotencyKey string,
) error {
	if userID <= 0 || amount <= 0 || idempotencyKey == "" {
		return nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_credits (user_id, balance)
		VALUES ($1, 0)
		ON CONFLICT (user_id) DO NOTHING
	`, userID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE user_credits
		SET balance = balance + $2, updated_at = NOW()
		WHERE user_id = $1
	`, userID, amount); err != nil {
		return err
	}

	if err := writeCreditTx(ctx, tx, userID, amount, transactionType, idempotencyKey); err != nil {
		if isUniqueViolation(err) {
			return nil
		}
		return err
	}

	return tx.Commit(ctx)
}

func writeCreditTx(
	ctx context.Context,
	tx pgx.Tx,
	userID int64,
	amount int,
	transactionType string,
	idempotencyKey string,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO credit_transactions (
			user_id, amount, transaction_type, idempotency_key
		)
		VALUES ($1, $2, $3, $4)
	`, userID, amount, transactionType, idempotencyKey)
	return err
}

// isUniqueViolation 判断是否撞了唯一约束（重复幂等键）。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// 不引 pq/pgconn，避免为一条判断多一个直接依赖；
	// 错误串里 23505 是 PostgreSQL 的 unique_violation。
	return containsCode(err.Error(), "23505")
}

func containsCode(s, code string) bool {
	for i := 0; i+len(code) <= len(s); i++ {
		if s[i:i+len(code)] == code {
			return true
		}
	}
	return false
}
