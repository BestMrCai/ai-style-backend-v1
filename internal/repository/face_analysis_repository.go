package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrFaceAnalysisNotFound 表示分析任务不存在或不属于当前用户。
var ErrFaceAnalysisNotFound = errors.New("face analysis not found")

// ErrFaceAnalysisNotProcessing 表示任务不在 processing 状态。
var ErrFaceAnalysisNotProcessing = errors.New("face analysis is not processing")

// FaceAnalysis 保存一条面部分析任务。
type FaceAnalysis struct {
	ID      int64
	UserID  int64
	PhotoID int64
	Status  string
	// Gender 与 AgeGroup 描述「这一次分析」的对象，
	// 而不是账号本身：同一个人可能帮家人朋友分析不同的照片。
	Gender       *string
	AgeGroup     *string
	ResultJSON   []byte
	OverlayKey   *string
	ErrorCode    *string
	ErrorMessage *string
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

// FaceAnalysisRepository 负责访问 face_analyses 表。
type FaceAnalysisRepository struct {
	db *pgxpool.Pool
}

func NewFaceAnalysisRepository(db *pgxpool.Pool) *FaceAnalysisRepository {
	return &FaceAnalysisRepository{db: db}
}

// Create 验证照片归属后创建一条 queued 任务。
func (r *FaceAnalysisRepository) Create(
	ctx context.Context,
	userID int64,
	photoID int64,
	gender string,
	ageGroup string,
) (int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var verifiedID int64
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM user_photos
		WHERE id = $1
			AND user_id = $2
			AND photo_type = 'portrait'
			AND consent_status = 'granted'
			AND deleted_at IS NULL
		FOR SHARE
	`, photoID, userID).Scan(&verifiedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrPhotoUnavailable
	}
	if err != nil {
		return 0, err
	}

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO face_analyses (user_id, photo_id, gender, age_group)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''))
		RETURNING id
	`, userID, photoID, gender, ageGroup).Scan(&id)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return id, nil
}

const faceAnalysisColumns = `
	id, user_id, photo_id, status,
	gender, age_group,
	result_json, overlay_key,
	error_code, error_message,
	created_at, started_at, finished_at
`

func scanFaceAnalysis(row pgx.Row) (*FaceAnalysis, error) {
	var f FaceAnalysis
	err := row.Scan(
		&f.ID,
		&f.UserID,
		&f.PhotoID,
		&f.Status,
		&f.Gender,
		&f.AgeGroup,
		&f.ResultJSON,
		&f.OverlayKey,
		&f.ErrorCode,
		&f.ErrorMessage,
		&f.CreatedAt,
		&f.StartedAt,
		&f.FinishedAt,
	)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// FindByIDForUser 查询当前用户自己的分析任务。
func (r *FaceAnalysisRepository) FindByIDForUser(
	ctx context.Context,
	userID int64,
	analysisID int64,
) (*FaceAnalysis, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+faceAnalysisColumns+`
		FROM face_analyses
		WHERE id = $1 AND user_id = $2
	`, analysisID, userID)

	f, err := scanFaceAnalysis(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrFaceAnalysisNotFound
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

// ListForUser 查询当前用户最近的分析任务。
func (r *FaceAnalysisRepository) ListForUser(
	ctx context.Context,
	userID int64,
	limit int,
) ([]FaceAnalysis, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	rows, err := r.db.Query(ctx, `
		SELECT `+faceAnalysisColumns+`
		FROM face_analyses
		WHERE user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]FaceAnalysis, 0, limit)
	for rows.Next() {
		f, err := scanFaceAnalysis(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// ClaimNext 原子领取一条排队任务。
func (r *FaceAnalysisRepository) ClaimNext(
	ctx context.Context,
) (*FaceAnalysis, error) {
	row := r.db.QueryRow(ctx, `
		WITH next_task AS (
			SELECT id
			FROM face_analyses
			WHERE status = 'queued'
			ORDER BY created_at ASC, id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE face_analyses AS f
		SET
			status = 'processing',
			started_at = NOW(),
			error_code = NULL,
			error_message = NULL
		FROM next_task
		WHERE f.id = next_task.id
		RETURNING `+prefixedColumns("f")+`
	`)

	f, err := scanFaceAnalysis(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

func prefixedColumns(alias string) string {
	return alias + `.id, ` + alias + `.user_id, ` + alias + `.photo_id, ` + alias + `.status, ` +
		alias + `.gender, ` + alias + `.age_group, ` +
		alias + `.result_json, ` + alias + `.overlay_key, ` + alias + `.error_code, ` +
		alias + `.error_message, ` + alias + `.created_at, ` + alias + `.started_at, ` +
		alias + `.finished_at`
}

// Complete 保存分析结果并将任务更新为 succeeded。
func (r *FaceAnalysisRepository) Complete(
	ctx context.Context,
	analysisID int64,
	resultJSON []byte,
	overlayKey string,
) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE face_analyses
		SET
			status = 'succeeded',
			result_json = $2::jsonb,
			overlay_key = NULLIF($3, ''),
			finished_at = NOW(),
			error_code = NULL,
			error_message = NULL
		WHERE id = $1 AND status = 'processing'
	`, analysisID, string(resultJSON), overlayKey)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrFaceAnalysisNotProcessing
	}
	return nil
}

// Fail 将处理中的任务更新为 failed。
func (r *FaceAnalysisRepository) Fail(
	ctx context.Context,
	analysisID int64,
	errorCode string,
	errorMessage string,
) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE face_analyses
		SET
			status = 'failed',
			finished_at = NOW(),
			error_code = $2,
			error_message = $3
		WHERE id = $1 AND status = 'processing'
	`, analysisID, errorCode, errorMessage)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrFaceAnalysisNotProcessing
	}
	return nil
}

// DeleteForUser 删除当前用户自己的分析记录，并返回标注图存储键。
// 只允许删除自己的任务；不存在时返回 ErrFaceAnalysisNotFound。
func (r *FaceAnalysisRepository) DeleteForUser(
	ctx context.Context,
	userID int64,
	analysisID int64,
) (string, error) {
	var overlayKey *string

	err := r.db.QueryRow(ctx, `
		DELETE FROM face_analyses
		WHERE id = $1 AND user_id = $2
		RETURNING overlay_key
	`, analysisID, userID).Scan(&overlayKey)

	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrFaceAnalysisNotFound
	}
	if err != nil {
		return "", err
	}

	if overlayKey == nil {
		return "", nil
	}
	return *overlayKey, nil
}



// UpdateProfile 更新某次分析记录的性别与年龄段。
// 允许在分析完成后补填，避免用户上传时跳过导致记录不可用。
func (r *FaceAnalysisRepository) UpdateProfile(
	ctx context.Context,
	userID int64,
	analysisID int64,
	gender string,
	ageGroup string,
) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE face_analyses
		SET gender = NULLIF($3, ''), age_group = NULLIF($4, '')
		WHERE id = $1 AND user_id = $2
	`, analysisID, userID, gender, ageGroup)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrFaceAnalysisNotFound
	}
	return nil
}
