package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"ai-style-backend/internal/face"
	"ai-style-backend/internal/repository"
	"ai-style-backend/internal/storage"
)

var ErrInvalidFaceAnalysis = errors.New("invalid face analysis request")

// FaceAnalysisService 编排面部分析任务。
type FaceAnalysisService struct {
	repo      *repository.FaceAnalysisRepository
	photoRepo *repository.PhotoRepository
	photoSvc  *PhotoService
	client    *face.Client
	// quota 是每日次数限制。放在接入层而不是 Worker：
	// 额度按「用户发起次数」计，用户点了就算一次，
	// 和积分扣费口径一致。
	quota *QuotaService
}

func NewFaceAnalysisService(
	repo *repository.FaceAnalysisRepository,
	photoRepo *repository.PhotoRepository,
	photoSvc *PhotoService,
	client *face.Client,
	quota *QuotaService,
) *FaceAnalysisService {
	return &FaceAnalysisService{
		repo:      repo,
		photoRepo: photoRepo,
		photoSvc:  photoSvc,
		client:    client,
		quota:     quota,
	}
}

// 允许的性别与年龄段取值，与数据库约束保持一致。
var (
	allowedGenders   = map[string]struct{}{"male": {}, "female": {}}
	allowedAgeGroups = map[string]struct{}{
		"u18": {}, "18_25": {}, "26_35": {},
		"36_45": {}, "46_55": {}, "56p": {},
	}
)

// Create 为当前用户的照片创建排队任务。
// gender 必填；ageGroup 选填，空字符串表示用户选择暂不填写。
func (s *FaceAnalysisService) Create(
	ctx context.Context,
	userID int64,
	photoID int64,
	gender string,
	ageGroup string,
) (int64, error) {
	if userID <= 0 || photoID <= 0 {
		return 0, ErrInvalidFaceAnalysis
	}

	if _, ok := allowedGenders[gender]; !ok {
		return 0, ErrInvalidFaceAnalysis
	}

	if ageGroup != "" {
		if _, ok := allowedAgeGroups[ageGroup]; !ok {
			return 0, ErrInvalidFaceAnalysis
		}
	}

	// 先扣费再建任务：顺序反过来会让超额请求也留下一条 queued 记录。
	// 免费优先，免费用完才扣积分，规则收在 QuotaService 里。
	var charge QuotaCharge
	if s.quota != nil {
		var err error
		charge, err = s.quota.ReserveFaceAnalysis(ctx, userID)
		if err != nil {
			return 0, err
		}
	}

	id, err := s.repo.Create(ctx, userID, photoID, gender, ageGroup)
	if err != nil {
		// 任务没建成（照片无效等）：积分要退，日志里也能对上。
		if s.quota != nil && charge.Records() {
			s.quota.RefundUnrecorded(ctx, userID, charge, repository.QuotaFaceAnalysis)
		}
		return 0, err
	}

	// 任务建好之后才写使用记录：这样记录里能带上关联 ID，
	// 用户点记录就能跳到详情。
	if s.quota != nil && charge.Records() {
		s.quota.Record(ctx, userID, charge, repository.QuotaFaceAnalysis, id, 0)
	}

	return id, nil
}

func (s *FaceAnalysisService) GetByID(
	ctx context.Context,
	userID int64,
	analysisID int64,
) (*repository.FaceAnalysis, error) {
	if userID <= 0 || analysisID <= 0 {
		return nil, ErrInvalidFaceAnalysis
	}
	return s.repo.FindByIDForUser(ctx, userID, analysisID)
}

func (s *FaceAnalysisService) List(
	ctx context.Context,
	userID int64,
	limit int,
) ([]repository.FaceAnalysis, error) {
	if userID <= 0 {
		return nil, ErrInvalidFaceAnalysis
	}
	return s.repo.ListForUser(ctx, userID, limit)
}

// Delete 删除当前用户的分析记录，并清理对应标注图。
func (s *FaceAnalysisService) Delete(
	ctx context.Context,
	userID int64,
	analysisID int64,
) error {
	if userID <= 0 || analysisID <= 0 {
		return ErrInvalidFaceAnalysis
	}

	overlayKey, err := s.repo.DeleteForUser(ctx, userID, analysisID)
	if err != nil {
		return err
	}

	if overlayKey != "" {
		if delErr := s.photoSvc.Delete(ctx, overlayKey); delErr != nil {
			log.Printf("delete overlay %s: %v", overlayKey, delErr)
		}
	}

	return nil
}

// userFacingMessage 把内部错误翻译成用户能看懂的话。
//
// error_message 会直接显示在小程序上，把
// "read photo: qiniu: domain returned 401" 这种原文播出去
// 既泄露实现细节，用户也不知道该怎么办。
// 完整错误仍然打在日志里，排查不受影响。
func userFacingMessage(err error) string {
	if err == nil {
		return ""
	}

	if errors.Is(err, storage.ErrObjectNotFound) {
		return "照片读取失败，请重新上传后再试"
	}
	if errors.Is(err, face.ErrUnavailable) {
		return "分析服务暂时不可用，请稍后重试"
	}

	raw := strings.ToLower(err.Error())
	switch {
	case strings.Contains(raw, "401"), strings.Contains(raw, "403"),
		strings.Contains(raw, "unauthorized"), strings.Contains(raw, "bucket may be private"):
		return "照片读取失败，请稍后重试"
	case strings.Contains(raw, "timeout"), strings.Contains(raw, "deadline exceeded"):
		return "分析超时，请稍后重试"
	case strings.Contains(raw, "no face"), strings.Contains(raw, "face not detected"):
		return "没有检测到清晰的人脸，请换一张正面照片"
	case strings.Contains(raw, "photo"):
		return "照片读取失败，请重新上传后再试"
	}

	// 兜底：不透出原始技术细节。
	return "这次分析没有完成，请稍后重试"
}

// ProcessNext 领取并处理一条排队任务。
func (s *FaceAnalysisService) ProcessNext(ctx context.Context) (bool, error) {
	task, err := s.repo.ClaimNext(ctx)
	if err != nil {
		return false, err
	}
	if task == nil {
		return false, nil
	}

	if err := s.process(ctx, task); err != nil {
		log.Printf("face analysis %d failed: %v", task.ID, err)

		code := "FACE_ANALYSIS_FAILED"
		if errors.Is(err, face.ErrUnavailable) {
			code = "FACE_SERVICE_UNAVAILABLE"
		}

		message := userFacingMessage(err)
		if len(message) > 500 {
			message = message[:500]
		}
		if failErr := s.repo.Fail(ctx, task.ID, code, message); failErr != nil {
			log.Printf("face analysis %d mark failed: %v", task.ID, failErr)
		}
		// 分析失败：扣过的积分要退，免费次数不退。
		if s.quota != nil {
			s.quota.RefundByFaceAnalysis(ctx, task.UserID, task.ID)
		}
		return true, err
	}

	return true, nil
}

func (s *FaceAnalysisService) process(ctx context.Context, task *repository.FaceAnalysis) error {
	photo, err := s.photoRepo.FindByIDForUser(ctx, task.UserID, task.PhotoID)
	if err != nil {
		return fmt.Errorf("load photo: %w", err)
	}

	data, err := s.photoSvc.Read(ctx, photo.StorageKey)
	if err != nil {
		return fmt.Errorf("read photo: %w", err)
	}

	result, err := s.client.Analyze(ctx, data)
	if err != nil {
		return err
	}

	overlayKey := ""
	if len(result.Overlay) > 0 {
		key, saveErr := s.saveOverlay(ctx, result.Overlay)
		if saveErr != nil {
			return fmt.Errorf("save overlay: %w", saveErr)
		}
		overlayKey = key
	}

	report := withServerFiles(result.Report, task.ID, overlayKey)

	return s.repo.Complete(ctx, task.ID, report, overlayKey)
}

// saveOverlay 把 Python 返回的标注图存进对象存储。
//
// 与用户照片分开目录：标注图是服务端产物，
// 和「用户上传」在生命周期和权限上都不一样。
func (s *FaceAnalysisService) saveOverlay(ctx context.Context, data []byte) (string, error) {
	name, err := storage.NewFileName(".jpg")
	if err != nil {
		return "", err
	}

	key, err := storage.BuildKey(storage.DirFaceAnalysis, name, time.Now())
	if err != nil {
		return "", err
	}

	if err := s.photoSvc.Storage().Put(ctx, key, data); err != nil {
		return "", err
	}

	return key, nil
}

// withServerFiles 把 Python 返回的临时文件地址替换成本服务的可访问路径。
func withServerFiles(report json.RawMessage, analysisID int64, overlayKey string) json.RawMessage {
	var doc map[string]any
	if err := json.Unmarshal(report, &doc); err != nil {
		return report
	}

	if overlayKey == "" {
		delete(doc, "files")
		return mustJSON(doc, report)
	}

	doc["files"] = map[string]any{
		"overlay": "/api/v1/face-analyses/" + strconv.FormatInt(analysisID, 10) + "/overlay",
	}
	return mustJSON(doc, report)
}

func mustJSON(doc map[string]any, fallback json.RawMessage) json.RawMessage {
	data, err := json.Marshal(doc)
	if err != nil {
		return fallback
	}
	return data
}

// UpdateProfile 补填某次分析的性别与年龄段。
// 用于用户在报告页修改性别，或上传时跳过后再补充。
func (s *FaceAnalysisService) UpdateProfile(
	ctx context.Context,
	userID int64,
	analysisID int64,
	gender string,
	ageGroup string,
) error {
	if userID <= 0 || analysisID <= 0 {
		return ErrInvalidFaceAnalysis
	}

	if _, ok := allowedGenders[gender]; !ok {
		return ErrInvalidFaceAnalysis
	}

	if ageGroup != "" {
		if _, ok := allowedAgeGroups[ageGroup]; !ok {
			return ErrInvalidFaceAnalysis
		}
	}

	return s.repo.UpdateProfile(ctx, userID, analysisID, gender, ageGroup)
}
