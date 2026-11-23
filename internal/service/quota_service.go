package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"ai-style-backend/internal/repository"
)

// 计费相关的错误，Handler 分别映射成不同文案。
var (
	// ErrDailyQuotaExceeded 免费次数用完且该功能不单独收费。
	ErrDailyQuotaExceeded = errors.New("daily quota exceeded")
	// ErrInsufficientCredits 免费次数用完，积分也不够。
	ErrInsufficientCredits = errors.New("insufficient credits")
)

// QuotaCharge 描述一次使用是怎么付费的。
type QuotaCharge struct {
	// Free 为 true 表示走当天的免费次数。
	Free bool
	// Credits 是本次扣掉的积分（Free 时为 0）。
	Credits int
	// Used 为 true 表示确实占用过额度或积分。
	// 调用方据此决定"要不要在失败时退回"以及"要不要写使用记录"。
	Used bool
}

// Records 表示这次消耗需要写一条使用记录（免费也要记）。
func (c QuotaCharge) Records() bool { return c.Used }

// QuotaService 负责「免费次数 + 积分付费」的组合扣费。
//
// 两条产品规则：
//  1. 每个功能每天有免费次数，先用免费额度；
//  2. 免费用完就扣积分，积分不够直接拒绝，不放行。
//
// 额度与积分必须分开记：额度是"每天重置的权益"，积分是"用户的钱"。
// 所以"先试免费 → 再试积分"的顺序收在这里，调用方不用重复实现。
type QuotaService struct {
	repo       *repository.DailyQuotaRepository
	creditRepo *repository.CreditRepository
	usageRepo  *repository.UsageRecordRepository

	faceAnalysisLimit int
	hairstyleLimit    int
	faceAnalysisCost  int
	hairstyleCost     int

	now func() time.Time
}

func NewQuotaService(
	repo *repository.DailyQuotaRepository,
	creditRepo *repository.CreditRepository,
	usageRepo *repository.UsageRecordRepository,
	faceAnalysisLimit int,
	hairstyleLimit int,
	faceAnalysisCost int,
	hairstyleCost int,
) *QuotaService {
	return &QuotaService{
		repo:              repo,
		creditRepo:        creditRepo,
		usageRepo:         usageRepo,
		faceAnalysisLimit: faceAnalysisLimit,
		hairstyleLimit:    hairstyleLimit,
		faceAnalysisCost:  faceAnalysisCost,
		hairstyleCost:     hairstyleCost,
		now:               time.Now,
	}
}

// Limit / Cost 的读取口，供 Handler 拼提示文案。
func (s *QuotaService) FaceAnalysisLimit() int { return s.faceAnalysisLimit }
func (s *QuotaService) HairstyleLimit() int    { return s.hairstyleLimit }
func (s *QuotaService) FaceAnalysisCost() int  { return s.faceAnalysisCost }
func (s *QuotaService) HairstyleCost() int     { return s.hairstyleCost }

// ReserveFaceAnalysis 为一次面部分析占额度或扣积分。
func (s *QuotaService) ReserveFaceAnalysis(ctx context.Context, userID int64) (QuotaCharge, error) {
	return s.reserve(ctx, userID, repository.QuotaFaceAnalysis, s.faceAnalysisLimit, s.faceAnalysisCost)
}

// ReserveHairstyle 为一次 AI 合成占额度或扣积分。
func (s *QuotaService) ReserveHairstyle(ctx context.Context, userID int64) (QuotaCharge, error) {
	return s.reserve(ctx, userID, repository.QuotaHairstyleDesign, s.hairstyleLimit, s.hairstyleCost)
}

// reserve 先尝试免费次数，失败再尝试积分。
//
// 顺序不能反：先扣积分会让还有免费额度的用户白白花钱。
func (s *QuotaService) reserve(
	ctx context.Context,
	userID int64,
	feature string,
	limit int,
	cost int,
) (QuotaCharge, error) {
	if userID <= 0 {
		return QuotaCharge{}, ErrDailyQuotaExceeded
	}

	now := s.now()

	// 第一步：免费额度。
	if limit > 0 {
		_, err := s.repo.Consume(ctx, userID, feature, limit, now)
		if err == nil {
			return QuotaCharge{Free: true, Used: true}, nil
		}
		if !errors.Is(err, repository.ErrQuotaExceeded) {
			return QuotaCharge{}, err
		}
	}

	// 第二步：积分。cost <= 0 表示该功能不单独收费，直接拒绝。
	if cost <= 0 {
		return QuotaCharge{}, ErrDailyQuotaExceeded
	}

	key := s.creditKey(userID, feature, now)
	if err := s.creditRepo.Spend(ctx, userID, cost, key); err != nil {
		if errors.Is(err, repository.ErrInsufficientCredits) {
			return QuotaCharge{}, ErrInsufficientCredits
		}
		return QuotaCharge{}, err
	}

	return QuotaCharge{Free: false, Credits: cost, Used: true}, nil
}

// Record 写入使用记录，返回记录 ID（0 表示写入失败）。
//
// 拆成独立一步的原因：关联 ID（analysis_id / task_id）要等任务建好才知道，
// 而扣费必须在建任务之前完成，否则超额用户也会留下一条 queued 记录。
func (s *QuotaService) Record(
	ctx context.Context,
	userID int64,
	charge QuotaCharge,
	feature string,
	faceAnalysisID int64,
	designTaskID int64,
) int64 {
	chargeType := repository.ChargeFree
	if !charge.Free {
		chargeType = repository.ChargeCredit
	}

	var analysisRef *int64
	if faceAnalysisID > 0 {
		analysisRef = &faceAnalysisID
	}
	var taskRef *int64
	if designTaskID > 0 {
		taskRef = &designTaskID
	}

	// 日期按业务时区算，和额度表保持一致，否则"今天"会有两套定义。
	quotaDate := s.repo.Today(s.now())

	id, err := s.usageRepo.Create(
		ctx, userID, feature, chargeType, charge.Credits,
		analysisRef, taskRef, quotaDate,
	)
	if err != nil {
		// 记录写失败不该让主流程失败：用户已经扣过费了，
		// 这时候报错更糟。日志留痕便于补录。
		log.Printf("usage record write failed user=%d feature=%s charge=%+v: %v",
			userID, feature, charge, err)
		return 0
	}
	return id
}

// RefundUnrecorded 在"已扣费但任务没建成"时退回消耗。
//
// 这一步还没写使用记录，所以只处理积分，不涉及记录标记。
// 免费次数不退：它是当日权益，退回去等于把"限流"变成"没限流"。
func (s *QuotaService) RefundUnrecorded(ctx context.Context, userID int64, charge QuotaCharge, feature string) {
	if !charge.Used || charge.Free || charge.Credits <= 0 {
		return
	}

	// 用固定前缀 + 功能名做退款键：同一次失败可能被重试，
	// 靠唯一索引把重复退款挡掉。
	origin := fmt.Sprintf("unrecorded:%d:%s:%d", userID, feature, s.now().UnixNano())
	if err := s.creditRepo.Refund(ctx, userID, charge.Credits, origin); err != nil {
		log.Printf("refund unrecorded credits failed user=%d feature=%s: %v", userID, feature, err)
	}
}

// RefundByFaceAnalysis 分析失败时退款，Worker 分支调用。
func (s *QuotaService) RefundByFaceAnalysis(ctx context.Context, userID int64, analysisID int64) {
	if userID <= 0 || analysisID <= 0 {
		return
	}

	record, err := s.usageRepo.FindByFaceAnalysis(ctx, analysisID)
	if err != nil || record == nil {
		return
	}
	s.refundRecord(ctx, userID, record)
}

// RefundByDesignTask 合成失败时退款，Worker 分支调用。
func (s *QuotaService) RefundByDesignTask(ctx context.Context, userID int64, taskID int64) {
	if userID <= 0 || taskID <= 0 {
		return
	}

	record, err := s.usageRepo.FindByDesignTask(ctx, taskID)
	if err != nil || record == nil {
		return
	}
	s.refundRecord(ctx, userID, record)
}

// refundRecord 退回一条已记录的使用。
//
// 顺序：先标记、再退钱。反过来会出现"退了钱但标记失败"，
// 重试时就会重复退款。
func (s *QuotaService) refundRecord(ctx context.Context, userID int64, record *repository.UsageRecord) {
	ok, err := s.usageRepo.MarkRefunded(ctx, record.ID)
	if err != nil || !ok {
		// 已经退过，或标记失败：都不该继续退钱
		return
	}

	if record.ChargeType != repository.ChargeCredit || record.CreditsCost <= 0 {
		// 免费的那次只标记，不动积分
		return
	}

	origin := fmt.Sprintf("design_task:%d", derefInt64(record.DesignTaskID))
	if record.DesignTaskID == nil {
		origin = fmt.Sprintf("face_analysis:%d", derefInt64(record.FaceAnalysisID))
	}

	if err := s.creditRepo.Refund(ctx, userID, record.CreditsCost, origin); err != nil {
		log.Printf("refund credits failed user=%d record=%d: %v", userID, record.ID, err)
	}
}

// Remaining 返回当天的免费剩余次数与积分余额，供小程序展示。
func (s *QuotaService) Remaining(ctx context.Context, userID int64) (QuotaSnapshot, error) {
	face, err := s.repo.Remaining(ctx, userID, repository.QuotaFaceAnalysis, s.faceAnalysisLimit, s.now())
	if err != nil {
		return QuotaSnapshot{}, err
	}
	hairstyle, err := s.repo.Remaining(ctx, userID, repository.QuotaHairstyleDesign, s.hairstyleLimit, s.now())
	if err != nil {
		return QuotaSnapshot{}, err
	}
	balance, err := s.creditRepo.Balance(ctx, userID)
	if err != nil {
		return QuotaSnapshot{}, err
	}

	return QuotaSnapshot{
		FaceFreeRemaining:      face,
		FaceFreeLimit:          s.faceAnalysisLimit,
		FaceCreditCost:         s.faceAnalysisCost,
		HairstyleFreeRemaining: hairstyle,
		HairstyleFreeLimit:     s.hairstyleLimit,
		HairstyleCreditCost:    s.hairstyleCost,
		CreditBalance:          balance,
	}, nil
}

// QuotaSnapshot 是给前端的额度快照。
type QuotaSnapshot struct {
	FaceFreeRemaining      int
	FaceFreeLimit          int
	FaceCreditCost         int
	HairstyleFreeRemaining int
	HairstyleFreeLimit     int
	HairstyleCreditCost    int
	CreditBalance          int
}

// creditKey 生成扣费幂等键：带上纳秒时间戳，
// 保证不同次调用不会撞键，同一次重试又能被唯一索引拦住。
func (s *QuotaService) creditKey(userID int64, feature string, now time.Time) string {
	return fmt.Sprintf("usage:%d:%s:%d", userID, feature, now.UnixNano())
}

func derefInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
