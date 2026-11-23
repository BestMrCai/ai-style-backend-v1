package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"unicode/utf8"

	"ai-style-backend/internal/ai"
	"ai-style-backend/internal/hairstyle"
	"ai-style-backend/internal/repository"
	"ai-style-backend/internal/storage"
)

var ErrInvalidDesignTask = errors.New("invalid design task parameters")

// DesignTaskService 负责设计任务的业务规则。
type DesignTaskService struct {
	taskRepo  *repository.DesignTaskRepository
	photoRepo *repository.PhotoRepository
	photoSvc  *PhotoService
	generator ai.Generator
	// styles 用于校验 style_id 是否真实存在，
	// 防止前端传入任意字符串污染任务表。
	styles *hairstyle.Library
	// analysisRepo 用于把面部分析结果转换成合成条件。
	analysisRepo *repository.FaceAnalysisRepository
	// quota 是每日 AI 生成次数限制，与面部分析的额度分开计。
	quota *QuotaService
}

// NewDesignTaskService 创建设计任务 Service。
func NewDesignTaskService(
	taskRepo *repository.DesignTaskRepository,
	photoRepo *repository.PhotoRepository,
	photoSvc *PhotoService,
	generator ai.Generator,
	styles *hairstyle.Library,
	analysisRepo *repository.FaceAnalysisRepository,
	quota *QuotaService,
) *DesignTaskService {
	return &DesignTaskService{
		taskRepo:     taskRepo,
		photoRepo:    photoRepo,
		photoSvc:     photoSvc,
		generator:    generator,
		styles:       styles,
		analysisRepo: analysisRepo,
		quota:        quota,
	}
}

// Create 创建一个自动模式的设计任务。
// 保留此方法，兼容现有 Handler 和小程序。
func (s *DesignTaskService) Create(
	ctx context.Context,
	userID int64,
	photoID int64,
	designType string,
	styleCode string,
) (int64, error) {
	return s.CreateWithOptions(
		ctx,
		userID,
		photoID,
		designType,
		styleCode,
		"auto",
		"",
		nil,
	)
}

// CreateFromHairstyle 依据面部分析记录创建发型合成任务。
// styleID 必须是发型库中存在的素材；analysisID 必须属于当前用户且已完成。
func (s *DesignTaskService) CreateFromHairstyle(
	ctx context.Context,
	userID int64,
	analysisID int64,
	styleID string,
	mode string,
) (int64, error) {
	if userID <= 0 || analysisID <= 0 || strings.TrimSpace(styleID) == "" {
		return 0, ErrInvalidDesignTask
	}

	// 本期只支持"从推荐列表选择"。
	// reference（上传参考图）走原有的 CreateWithOptions，接口保持不变。
	if mode != "styleset" {
		return 0, ErrInvalidDesignTask
	}

	if s.styles != nil {
		if _, ok := s.styles.Find(styleID); !ok {
			return 0, ErrInvalidDesignTask
		}
	}

	// 先扣费：AI 出图是唯一有真金白银成本的一步。
	// 免费优先，免费用完才扣积分。
	var charge QuotaCharge
	if s.quota != nil {
		var err error
		charge, err = s.quota.ReserveHairstyle(ctx, userID)
		if err != nil {
			return 0, err
		}
	}

	taskID, err := s.taskRepo.CreateHairstyleTask(
		ctx,
		userID,
		analysisID,
		styleID,
		"",
		"",
	)
	if err != nil {
		if s.quota != nil && charge.Records() {
			s.quota.RefundUnrecorded(ctx, userID, charge, repository.QuotaHairstyleDesign)
		}
		return 0, err
	}

	// 任务建好之后才写使用记录，这样记录能带上 task_id 供跳详情。
	if s.quota != nil && charge.Records() {
		s.quota.Record(ctx, userID, charge, repository.QuotaHairstyleDesign, 0, taskID)
	}

	return taskID, nil
}

// CreateWithOptions 创建支持设计模式和参考图片的设计任务。
func (s *DesignTaskService) CreateWithOptions(
	ctx context.Context,
	userID int64,
	photoID int64,
	designType string,
	styleCode string,
	designMode string,
	userRequirement string,
	referencePhotoIDs []int64,
) (int64, error) {
	if userID <= 0 || photoID <= 0 {
		return 0, ErrInvalidDesignTask
	}

	designType = strings.TrimSpace(designType)
	styleCode = strings.TrimSpace(styleCode)
	designMode = strings.TrimSpace(designMode)
	userRequirement = strings.TrimSpace(userRequirement)

	switch designType {
	case "hairstyle", "outfit", "full_style":
		// 与当前数据库约束保持一致。
	default:
		return 0, ErrInvalidDesignTask
	}

	if len(styleCode) > 80 {
		return 0, ErrInvalidDesignTask
	}

	if utf8.RuneCountInString(userRequirement) > 1000 {
		return 0, ErrInvalidDesignTask
	}

	switch designMode {
	case "auto":
		if len(referencePhotoIDs) != 0 {
			return 0, ErrInvalidDesignTask
		}

	case "reference":
		if len(referencePhotoIDs) == 0 {
			return 0, ErrInvalidDesignTask
		}

	default:
		return 0, ErrInvalidDesignTask
	}

	// reference 模式同样会触发出图，计费口径与 styleset 一致：
	// 两者都是「用户主动发起一次 AI 生成」。
	var charge QuotaCharge
	if s.quota != nil {
		var err error
		charge, err = s.quota.ReserveHairstyle(ctx, userID)
		if err != nil {
			return 0, err
		}
	}

	taskID, err := s.taskRepo.CreateWithPhotos(
		ctx,
		userID,
		photoID,
		designType,
		styleCode,
		designMode,
		userRequirement,
		referencePhotoIDs,
	)
	if err != nil {
		if s.quota != nil && charge.Records() {
			s.quota.RefundUnrecorded(ctx, userID, charge, repository.QuotaHairstyleDesign)
		}
		return 0, err
	}

	if s.quota != nil && charge.Records() {
		s.quota.Record(ctx, userID, charge, repository.QuotaHairstyleDesign, 0, taskID)
	}

	return taskID, nil
}

// GetByID 查询当前用户自己的设计任务。
func (s *DesignTaskService) GetByID(
	ctx context.Context,
	userID int64,
	taskID int64,
) (*repository.DesignTask, error) {
	if userID <= 0 || taskID <= 0 {
		return nil, ErrInvalidDesignTask
	}

	return s.taskRepo.FindByIDForUser(ctx, userID, taskID)
}

// ListForUser 返回当前用户的发型合成记录。
func (s *DesignTaskService) ListForUser(
	ctx context.Context,
	userID int64,
	limit int,
) ([]repository.DesignTaskSummary, error) {
	if userID <= 0 {
		return nil, ErrInvalidDesignTask
	}

	return s.taskRepo.ListForUser(ctx, userID, limit)
}

// Delete 删除当前用户的一条发型合成记录。
func (s *DesignTaskService) Delete(
	ctx context.Context,
	userID int64,
	taskID int64,
) error {
	if userID <= 0 || taskID <= 0 {
		return ErrInvalidDesignTask
	}

	keys, err := s.taskRepo.DeleteForUser(ctx, userID, taskID)
	if err != nil {
		return err
	}

	// 记录已经删掉，图片删失败只能留下孤儿文件，不影响用户，
	// 所以只记日志，不把错误抛回去。
	for _, key := range keys {
		if delErr := s.photoSvc.Delete(ctx, key); delErr != nil {
			log.Printf("delete design image %s: %v", key, delErr)
		}
	}

	return nil
}

// ProcessNextTask 领取并处理一条待处理任务。
// 返回值：
// processed = true，表示成功领取了一条任务。
// processed = false，表示当前没有待处理任务。
func (s *DesignTaskService) ProcessNextTask(
	ctx context.Context,
) (bool, error) {
	// 1. 从数据库领取一条任务。
	task, err := s.taskRepo.ClaimNextTask(ctx)
	if err != nil {
		return false, err
	}

	// 当前没有排队任务。
	if task == nil {
		return false, nil
	}

	// 2. 读取任务对应的本人照片和参考照片。
	portraitData, referenceData, err := s.readTaskPhotos(ctx, task)
	if err != nil {
		_ = s.taskRepo.FailTask(
			ctx,
			task.ID,
			"PHOTO_READ_FAILED",
			"failed to read task photos",
		)
		if s.quota != nil {
			s.quota.RefundByDesignTask(ctx, task.UserID, task.ID)
		}
		return true, err
	}

	// 暂时只验证照片读取结果。
	// 后续接入 AI 模型时，会将这两类图片传给 AI。
	log.Printf(
		"Task %d photos loaded: portrait=%d bytes, references=%d",
		task.ID,
		len(portraitData),
		len(referenceData),
	)
	// 3. 调用 AI Generator 生成设计建议。
	styleCode := ""
	if task.StyleCode != nil {
		styleCode = *task.StyleCode
	}

	req := ai.GenerateRequest{
		Portrait:        portraitData,
		References:      referenceData,
		DesignType:      task.DesignType,
		DesignMode:      task.DesignMode,
		UserRequirement: valueOrEmpty(task.UserRequirement),
		StyleCode:       styleCode,
	}

	// 把分析结果转换成显式合成条件。
	// 关键：脸型/发际线/发量必须作为参数传下去，而不是指望模型"看懂"报告。
	s.enrichFromAnalysis(ctx, task, &req)

	result, err := s.generator.Generate(ctx, req)
	if err != nil {
		_ = s.taskRepo.FailTask(
			ctx,
			task.ID,
			"AI_GENERATION_FAILED",
			"failed to generate design",
		)
		// 出图失败：扣过的积分要退，免费次数不退。
		if s.quota != nil {
			s.quota.RefundByDesignTask(ctx, task.UserID, task.ID)
		}
		return true, err
	}

	if result == nil || result.Suggestion == nil {
		_ = s.taskRepo.FailTask(
			ctx,
			task.ID,
			"AI_GENERATION_EMPTY",
			"AI generator returned an empty result",
		)
		return true, errors.New("AI generator returned an empty result")
	}

	suggestion := result.Suggestion
	suggestion["task_id"] = task.ID

	// 4. 将 AI 建议转换为 JSON 字节。
	suggestionJSON, err := json.Marshal(suggestion)
	if err != nil {
		_ = s.taskRepo.FailTask(
			ctx,
			task.ID,
			"RESULT_ENCODE_FAILED",
			"failed to encode design suggestion",
		)
		return true, err
	}

	// 5. 先把效果图落盘。
	//    落盘失败不阻断任务：文字建议仍然有价值，
	//    不该因为磁盘或权限问题把一次成功的生成判死。
	imageKey := ""
	if len(result.ImageData) > 0 {
		key, _, saveErr := s.photoSvc.Save(
			ctx,
			bytes.NewReader(result.ImageData),
			storage.DirFaceAnalysis,
		)
		if saveErr != nil {
			log.Printf("task %d: save generated image: %v", task.ID, saveErr)
			suggestion["image_save_failed"] = true
			if reencoded, mErr := json.Marshal(suggestion); mErr == nil {
				suggestionJSON = reencoded
			}
		} else {
			imageKey = key
		}
	}

	// 6. 保存结果，并将任务更新为 succeeded。
	if err := s.taskRepo.CompleteTaskWithImage(
		ctx,
		task.ID,
		suggestionJSON,
		imageKey,
	); err != nil {
		return true, err
	}

	return true, nil
}

// readTaskPhotos 读取任务对应的本人照片和参考照片。
func (s *DesignTaskService) readTaskPhotos(
	ctx context.Context,
	task *repository.DesignTask,
) ([]byte, [][]byte, error) {
	// 1. 查询本人照片记录。
	portrait, err := s.photoRepo.FindByIDForUser(
		ctx,
		task.UserID,
		task.PhotoID,
	)
	if err != nil {
		return nil, nil, err
	}

	// 2. 读取本人照片文件。
	portraitData, err := s.photoSvc.Read(ctx, portrait.StorageKey)
	if err != nil {
		return nil, nil, err
	}

	// 3. 自动模式不需要读取参考照片。
	if task.DesignMode == "auto" {
		return portraitData, nil, nil
	}

	// 4. 查询参考照片记录。
	references, err := s.photoRepo.FindReferencesByTaskID(
		ctx,
		task.UserID,
		task.ID,
	)
	if err != nil {
		return nil, nil, err
	}

	// 5. 检查参考照片数量是否与任务记录一致。
	if len(references) != len(task.ReferencePhotoIDs) {
		return nil, nil, errors.New("reference photos are unavailable")
	}

	// 6. 逐张读取参考照片。
	referenceData := make([][]byte, 0, len(references))

	for _, reference := range references {
		data, err := s.photoSvc.Read(ctx, reference.StorageKey)
		if err != nil {
			return nil, nil, err
		}

		referenceData = append(referenceData, data)
	}

	return portraitData, referenceData, nil
}

// valueOrEmpty 将可选字符串指针转换为空字符串或实际值。
func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// enrichFromAnalysis 把面部分析结果与发型素材转换成显式合成条件。
//
// 这一步是"合成"与"硬 P"的分界：模型拿到的是具体的脸型、发际线高度、
// 发量档位和遮罩相关参数，而不是一句"帮我换个发型"。
// 任何一项取不到就留空，不做默认填充——猜测出来的值比没有值更危险。
func (s *DesignTaskService) enrichFromAnalysis(
	ctx context.Context,
	task *repository.DesignTask,
	req *ai.GenerateRequest,
) {
	if task.Gender != nil {
		req.Gender = *task.Gender
	}
	if task.AgeGroup != nil {
		req.AgeGroup = *task.AgeGroup
	}

	if task.StyleID != nil && s.styles != nil {
		if style, ok := s.styles.Find(*task.StyleID); ok {
			req.StyleID = style.ID
			req.StyleName = style.Name
			req.PromptHint = style.PromptHint
			req.NegativeHint = style.NegativeHint
			req.VolumeWeight = style.VolumeWeight
		}
	}
	// 旧任务没有 style_id 时保留空值，不做事后补齐：
	// 任务快照的意义就是"当时选了什么"，补填会破坏可追溯性。

	if task.AnalysisID == nil || s.analysisRepo == nil {
		return
	}

	analysis, err := s.analysisRepo.FindByIDForUser(ctx, task.UserID, *task.AnalysisID)
	if err != nil || len(analysis.ResultJSON) == 0 {
		log.Printf("task %d: analysis %d unavailable: %v", task.ID, *task.AnalysisID, err)
		return
	}

	var report struct {
		Findings struct {
			FaceShape struct {
				Value string `json:"value"`
			} `json:"face_shape"`
		} `json:"findings"`
		Hair struct {
			HairlineShape string `json:"hairline_shape"`
			Available     bool   `json:"available"`
		} `json:"hair"`
	}

	if err := json.Unmarshal(analysis.ResultJSON, &report); err != nil {
		log.Printf("task %d: parse analysis %d: %v", task.ID, *task.AnalysisID, err)
		return
	}

	req.FaceShape = report.Findings.FaceShape.Value

	if report.Hair.Available && report.Hair.HairlineShape != "照片中暂无法判断" {
		req.Hairline = report.Hair.HairlineShape
	}

	// 发际线高度用「上庭比例」表达，这是报告里已有的实测值。
	req.HairlineHeightPercent = upperThirdPercent(analysis.ResultJSON)

	// 发量档位：报告里目前只有间接证据，取不到就留空，
	// 由生成器自行决定是否使用默认蓬松度。
	req.Density = densityFrom(analysis.ResultJSON)
}

// upperThirdPercent 从报告的 metrics 中读取上庭比例。
// 该指标是"发际线到眉间 / 脸长"，正是合成时对齐遮罩上边界需要的值。
func upperThirdPercent(reportJSON []byte) float64 {
	var doc struct {
		Metrics []struct {
			Spec struct {
				ID string `json:"id"`
			} `json:"spec"`
			Value *float64 `json:"value"`
		} `json:"metrics"`
	}

	if err := json.Unmarshal(reportJSON, &doc); err != nil {
		return 0
	}

	for _, m := range doc.Metrics {
		if m.Spec.ID == "upper_third" && m.Value != nil {
			return *m.Value
		}
	}
	return 0
}

// densityFrom 读取发量档位。
// 报告结构里还没有独立字段时返回空字符串，不做任何猜测。
func densityFrom(reportJSON []byte) string {
	var doc struct {
		Hair struct {
			Density string `json:"density"`
		} `json:"hair"`
	}
	if err := json.Unmarshal(reportJSON, &doc); err != nil {
		return ""
	}
	return doc.Hair.Density
}
