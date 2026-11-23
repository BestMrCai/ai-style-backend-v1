package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DesignTaskRepository 负责访问 design_tasks 表。
type DesignTaskRepository struct {
	db *pgxpool.Pool
}

// NewDesignTaskRepository 创建设计任务 Repository。
func NewDesignTaskRepository(db *pgxpool.Pool) *DesignTaskRepository {
	return &DesignTaskRepository{db: db}
}

var (
	// ErrPhotoUnavailable 表示照片不存在或当前用户无权使用该照片。
	ErrPhotoUnavailable = errors.New("photo unavailable or not authorized")

	// ErrDesignTaskNotFound 表示任务不存在或不属于当前用户。
	ErrDesignTaskNotFound = errors.New("design task not found")

	// ErrDesignTaskNotProcessing 表示任务不存在或当前状态不允许完成。
	ErrDesignTaskNotProcessing = errors.New("design task is not processing")

	// ErrInvalidDesignPhotos 表示设计模式和参考图片参数不匹配。
	ErrInvalidDesignPhotos = errors.New("invalid design photos")

	// ErrDesignTaskNotReady 表示对应的分析记录尚未完成，不能开始合成。
	ErrDesignTaskNotReady = errors.New("source analysis is not ready")
)

// CreateIfPhotoAllowed 保留旧接口，兼容原有自动推荐任务。
// 旧调用默认使用 auto 模式，不需要参考图片。
func (r *DesignTaskRepository) CreateIfPhotoAllowed(
	ctx context.Context,
	userID int64,
	photoID int64,
	designType string,
	styleCode string,
) (int64, error) {
	return r.CreateWithPhotos(
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

// CreateWithPhotos 在同一个事务中验证照片、创建任务和保存参考图关联。
func (r *DesignTaskRepository) CreateWithPhotos(
	ctx context.Context,
	userID int64,
	photoID int64,
	designType string,
	styleCode string,
	designMode string,
	userRequirement string,
	referencePhotoIDs []int64,
) (int64, error) {
	// 1. 检查设计模式与参考图片参数。
	switch designMode {
	case "auto":
		if len(referencePhotoIDs) != 0 {
			return 0, ErrInvalidDesignPhotos
		}
	case "reference":
		if len(referencePhotoIDs) == 0 {
			return 0, ErrInvalidDesignPhotos
		}
	default:
		return 0, ErrInvalidDesignPhotos
	}

	// 2. 防止重复的参考图片 ID。
	seen := make(map[int64]struct{}, len(referencePhotoIDs))
	for _, id := range referencePhotoIDs {
		if id <= 0 {
			return 0, ErrInvalidDesignPhotos
		}
		if _, exists := seen[id]; exists {
			return 0, ErrInvalidDesignPhotos
		}
		seen[id] = struct{}{}
	}

	// 3. 开启数据库事务。
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// 4. 验证本人照片。
	var verifiedPhotoID int64

	err = tx.QueryRow(ctx, `
		SELECT id
		FROM user_photos
		WHERE id = $1
			AND user_id = $2
			AND photo_type = 'portrait'
			AND consent_status = 'granted'
			AND deleted_at IS NULL
		FOR SHARE
	`, photoID, userID).Scan(&verifiedPhotoID)

	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrPhotoUnavailable
	}
	if err != nil {
		return 0, err
	}

	// 5. 验证所有参考图片。
	for _, referencePhotoID := range referencePhotoIDs {
		var verifiedReferenceID int64

		err = tx.QueryRow(ctx, `
			SELECT id
			FROM user_photos
			WHERE id = $1
				AND user_id = $2
				AND photo_type = 'reference'
				AND consent_status = 'granted'
				AND deleted_at IS NULL
			FOR SHARE
		`, referencePhotoID, userID).Scan(&verifiedReferenceID)

		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrPhotoUnavailable
		}
		if err != nil {
			return 0, err
		}
	}

	// 6. 创建设计任务。
	var taskID int64

	err = tx.QueryRow(ctx, `
		INSERT INTO design_tasks (
			user_id,
			photo_id,
			design_type,
			style_code,
			design_mode,
			user_requirement
		)
		VALUES (
			$1,
			$2,
			$3,
			NULLIF($4, ''),
			$5,
			NULLIF($6, '')
		)
		RETURNING id
	`,
		userID,
		photoID,
		designType,
		styleCode,
		designMode,
		userRequirement,
	).Scan(&taskID)

	if err != nil {
		return 0, err
	}

	// 7. 保存参考图片与任务之间的关联。
	for _, referencePhotoID := range referencePhotoIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO design_task_photos (
				task_id,
				photo_id
			)
			VALUES ($1, $2)
		`, taskID, referencePhotoID)

		if err != nil {
			return 0, fmt.Errorf("save reference photo relation: %w", err)
		}
	}

	// 8. 全部成功后提交事务。
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}

	return taskID, nil
}

// DesignTask 保存查询到的任务信息。
type DesignTask struct {
	ID      int64
	PhotoID int64
	UserID  int64
	// AnalysisID / StyleID / Gender / AgeGroup 记录本次任务使用的上下文，
	// 保证用户之后修改分析记录或性别时，历史结果仍然可解释。
	AnalysisID        *int64
	StyleID           *string
	Gender            *string
	AgeGroup          *string
	DesignType        string
	StyleCode         *string
	DesignMode        string
	UserRequirement   *string
	ReferencePhotoIDs []int64
	Status            string
	ErrorCode         *string
	ErrorMessage      *string
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	Results           []DesignResult
}

// DesignResult 保存一条设计结果。
type DesignResult struct {
	ID         int64
	ResultType string
	StorageKey *string
	Suggestion []byte
	CreatedAt  time.Time
}

// FindByIDForUser 查询当前用户自己的任务及其结果。
func (r *DesignTaskRepository) FindByIDForUser(
	ctx context.Context,
	userID int64,
	taskID int64,
) (*DesignTask, error) {
	task := &DesignTask{
		Results: make([]DesignResult, 0),
	}

	err := r.db.QueryRow(ctx, `
		SELECT
			id,
			photo_id,
			analysis_id,
			style_id,
			gender,
			age_group,
			design_type,
			style_code,
			design_mode,
			user_requirement,
			status,
			error_code,
			error_message,
			created_at,
			started_at,
			finished_at
		FROM design_tasks
		WHERE id = $1
			AND user_id = $2
	`, taskID, userID).Scan(
		&task.ID,
		&task.PhotoID,
		&task.AnalysisID,
		&task.StyleID,
		&task.Gender,
		&task.AgeGroup,
		&task.DesignType,
		&task.StyleCode,
		&task.DesignMode,
		&task.UserRequirement,
		&task.Status,
		&task.ErrorCode,
		&task.ErrorMessage,
		&task.CreatedAt,
		&task.StartedAt,
		&task.FinishedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDesignTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	// 查询当前任务关联的参考图片 ID。
	referenceRows, err := r.db.Query(ctx, `
    SELECT photo_id
    FROM design_task_photos
    WHERE task_id = $1
    ORDER BY photo_id ASC
`, taskID)
	if err != nil {
		return nil, err
	}

	for referenceRows.Next() {
		var referencePhotoID int64

		if err := referenceRows.Scan(&referencePhotoID); err != nil {
			referenceRows.Close()
			return nil, err
		}

		task.ReferencePhotoIDs = append(
			task.ReferencePhotoIDs,
			referencePhotoID,
		)
	}

	if err := referenceRows.Err(); err != nil {
		referenceRows.Close()
		return nil, err
	}

	referenceRows.Close()

	rows, err := r.db.Query(ctx, `
		SELECT
			id,
			result_type,
			storage_key,
			suggestion,
			created_at
		FROM design_results
		WHERE task_id = $1
		ORDER BY id ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var result DesignResult

		err := rows.Scan(
			&result.ID,
			&result.ResultType,
			&result.StorageKey,
			&result.Suggestion,
			&result.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		task.Results = append(task.Results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return task, nil
}

// DeleteForUser 删除当前用户的一条设计任务，返回需要一并清理的效果图存储键。
//
// 删除策略：
//   - design_results / design_task_photos 是 ON DELETE CASCADE，跟着任务一起走；
//   - credit_transactions.task_id 是 NO ACTION 外键，直接删会被拦，所以先置空。
//     积分流水是账目，不该因为用户删了记录就消失；
//   - 存储里的效果图不在这里删，交给 Service 在事务提交后处理：
//     事务回滚时不该出现"库里还在、文件没了"的脏数据。
func (r *DesignTaskRepository) DeleteForUser(
	ctx context.Context,
	userID int64,
	taskID int64,
) ([]string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 先锁住任务行，避免和 Worker 的 ClaimNextTask 交叉。
	var lockedID int64
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM design_tasks
		WHERE id = $1 AND user_id = $2
		FOR UPDATE
	`, taskID, userID).Scan(&lockedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDesignTaskNotFound
	}
	if err != nil {
		return nil, err
	}

	// 收集效果图键，事务提交后再删文件。
	rows, err := tx.Query(ctx, `
		SELECT storage_key
		FROM design_results
		WHERE task_id = $1 AND storage_key IS NOT NULL AND storage_key <> ''
	`, taskID)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, 2)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 积分流水要保留，只断开与任务的关联。
	if _, err := tx.Exec(ctx, `
		UPDATE credit_transactions SET task_id = NULL WHERE task_id = $1
	`, taskID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM design_tasks WHERE id = $1`, taskID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return keys, nil
}


// ClaimNextTask 从队列中安全领取一条待处理任务。
// 使用 FOR UPDATE SKIP LOCKED，避免多个 Worker 领取同一条任务。
func (r *DesignTaskRepository) ClaimNextTask(
	ctx context.Context,
) (*DesignTask, error) {
	task := &DesignTask{
		Results: make([]DesignResult, 0),
	}

	err := r.db.QueryRow(ctx, `
		WITH next_task AS (
			SELECT id
			FROM design_tasks
			WHERE status = 'queued'
			ORDER BY created_at ASC, id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE design_tasks AS t
		SET
			status = 'processing',
			started_at = NOW(),
			error_code = NULL,
			error_message = NULL
		FROM next_task
		WHERE t.id = next_task.id
		RETURNING
			t.id,
			t.user_id,
			t.photo_id,
			t.analysis_id,
			t.style_id,
			t.gender,
			t.age_group,
			t.design_type,
			t.style_code,
			t.design_mode,
			t.user_requirement,
			t.status,
			t.error_code,
			t.error_message,
			t.created_at,
			t.started_at,
			t.finished_at
	`).Scan(
		&task.ID,
		&task.UserID,
		&task.PhotoID,
		&task.AnalysisID,
		&task.StyleID,
		&task.Gender,
		&task.AgeGroup,
		&task.DesignType,
		&task.StyleCode,
		&task.DesignMode,
		&task.UserRequirement,
		&task.Status,
		&task.ErrorCode,
		&task.ErrorMessage,
		&task.CreatedAt,
		&task.StartedAt,
		&task.FinishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// 查询当前任务关联的参考图片 ID。
	referenceRows, err := r.db.Query(ctx, `
		SELECT photo_id
		FROM design_task_photos
		WHERE task_id = $1
		ORDER BY photo_id ASC
	`, task.ID)
	if err != nil {
		return nil, err
	}

	for referenceRows.Next() {
		var referencePhotoID int64

		if err := referenceRows.Scan(&referencePhotoID); err != nil {
			referenceRows.Close()
			return nil, err
		}

		task.ReferencePhotoIDs = append(
			task.ReferencePhotoIDs,
			referencePhotoID,
		)
	}

	if err := referenceRows.Err(); err != nil {
		referenceRows.Close()
		return nil, err
	}

	referenceRows.Close()

	return task, nil
}

// DesignTaskSummary 是「我的发型」列表用的轻量结构。
//
// 不带完整 suggestion：列表只需要展示用的少数几个字段，
// 把整份报告塞进列表响应会让首屏变慢。
type DesignTaskSummary struct {
	ID           int64
	AnalysisID   *int64
	StyleID      *string
	StyleName    string
	Status       string
	ErrorMessage *string
	HasImage     bool
	CreatedAt    time.Time
	FinishedAt   *time.Time
}

// ListForUser 返回当前用户的发型合成任务，按创建时间倒序。
//
// 只取 design_type='hairstyle'：本页是「我的发型」，
// 穿搭等类型后续若上线，各自有独立的列表页。
func (r *DesignTaskRepository) ListForUser(
	ctx context.Context,
	userID int64,
	limit int,
) ([]DesignTaskSummary, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// 用 LATERAL 只取最新一条结果：
	// 重新生成会让一个任务有多条结果，列表展示最新那张即可。
	rows, err := r.db.Query(ctx, `
		SELECT
			t.id,
			t.analysis_id,
			t.style_id,
			t.status,
			t.error_message,
			t.created_at,
			t.finished_at,
			r.storage_key,
			r.suggestion -> 'hairstyle' ->> 'name'
		FROM design_tasks AS t
		LEFT JOIN LATERAL (
			SELECT storage_key, suggestion
			FROM design_results
			WHERE task_id = t.id
			ORDER BY id DESC
			LIMIT 1
		) AS r ON TRUE
		WHERE t.user_id = $1
			AND t.design_type = 'hairstyle'
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]DesignTaskSummary, 0, limit)
	for rows.Next() {
		var (
			item       DesignTaskSummary
			storageKey *string
			styleName  *string
		)

		if err := rows.Scan(
			&item.ID,
			&item.AnalysisID,
			&item.StyleID,
			&item.Status,
			&item.ErrorMessage,
			&item.CreatedAt,
			&item.FinishedAt,
			&storageKey,
			&styleName,
		); err != nil {
			return nil, err
		}

		if storageKey != nil && *storageKey != "" {
			item.HasImage = true
		}
		if styleName != nil {
			item.StyleName = *styleName
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

// CompleteTask 保存模拟设计结果，并将任务更新为 succeeded。
// CompleteTask 保留旧签名，仅保存建议文本（mock 生成器走这条路）。
func (r *DesignTaskRepository) CompleteTask(
	ctx context.Context,
	taskID int64,
	suggestion []byte,
) error {
	return r.CompleteTaskWithImage(ctx, taskID, suggestion, "")
}

// CompleteTaskWithImage 与 CompleteTask 同义，但允许同时写入效果图。
//
// storageKey 为空时行为与旧接口一致；
// 非空时写 result_type='image'，让前端能区分"只有文字建议"与"有效果图"。
func (r *DesignTaskRepository) CompleteTaskWithImage(
	ctx context.Context,
	taskID int64,
	suggestion []byte,
	storageKey string,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE design_tasks
		SET
			status = 'succeeded',
			finished_at = NOW(),
			error_code = NULL,
			error_message = NULL
		WHERE id = $1
			AND status = 'processing'
	`, taskID)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return ErrDesignTaskNotProcessing
	}

	resultType := "suggestion"
	var keyParam any
	if storageKey != "" {
		resultType = "image"
		keyParam = storageKey
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO design_results (
			task_id,
			result_type,
			storage_key,
			suggestion
		)
		VALUES ($1, $2, $3, $4::jsonb)
	`, taskID, resultType, keyParam, string(suggestion))
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// FailTask 将正在处理的任务更新为 failed。
func (r *DesignTaskRepository) FailTask(
	ctx context.Context,
	taskID int64,
	errorCode string,
	errorMessage string,
) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE design_tasks
		SET
			status = 'failed',
			finished_at = NOW(),
			error_code = $2,
			error_message = $3
		WHERE id = $1
			AND status = 'processing'
	`, taskID, errorCode, errorMessage)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return ErrDesignTaskNotProcessing
	}

	return nil
}

// CreateHairstyleTask 依据一条已成功的面部分析创建发型合成任务。
//
// 与 CreateWithPhotos 的区别：
//   - 照片来自分析记录，而不是请求参数，避免前端传错；
//   - 性别与年龄段以分析记录为准，并写入任务快照；
//   - design_mode 固定写 styleset（从推荐列表选择）。
func (r *DesignTaskRepository) CreateHairstyleTask(
	ctx context.Context,
	userID int64,
	analysisID int64,
	styleID string,
	gender string,
	ageGroup string,
) (int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var (
		photoID   int64
		status    string
		recordGnd *string
		recordAge *string
	)

	err = tx.QueryRow(ctx, `
		SELECT photo_id, status, gender, age_group
		FROM face_analyses
		WHERE id = $1 AND user_id = $2
		FOR SHARE
	`, analysisID, userID).Scan(&photoID, &status, &recordGnd, &recordAge)

	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrDesignTaskNotFound
	}
	if err != nil {
		return 0, err
	}

	if status != "succeeded" {
		return 0, ErrDesignTaskNotReady
	}

	// 以分析记录为准：分析时的性别才是这张照片的性别。
	if recordGnd != nil && *recordGnd != "" {
		gender = *recordGnd
	}
	if recordAge != nil && *recordAge != "" {
		ageGroup = *recordAge
	}

	var taskID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO design_tasks (
			user_id,
			photo_id,
			analysis_id,
			style_id,
			gender,
			age_group,
			design_type,
			style_code,
			design_mode
		)
		VALUES (
			$1, $2, $3, NULLIF($4, ''),
			NULLIF($5, ''), NULLIF($6, ''),
			'hairstyle', NULLIF($4, ''), 'styleset'
		)
		RETURNING id
	`, userID, photoID, analysisID, styleID, gender, ageGroup).Scan(&taskID)

	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return taskID, nil
}
