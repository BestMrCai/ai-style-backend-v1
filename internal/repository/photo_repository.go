package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidPhotoType = errors.New("invalid photo type")

const (
	PhotoTypePortrait  = "portrait"
	PhotoTypeReference = "reference"
)

// PhotoRepository 负责访问 user_photos 表。
type PhotoRepository struct {
	db *pgxpool.Pool
}

// Photo 表示一条照片记录。
type Photo struct {
	ID            int64
	UserID        int64
	StorageKey    string
	PhotoType     string
	ConsentStatus string
}

// 创建 PhotoRepository。
func NewPhotoRepository(db *pgxpool.Pool) *PhotoRepository {
	return &PhotoRepository{db: db}
}

// Create 创建照片记录。
func (r *PhotoRepository) Create(
	ctx context.Context,
	userID int64,
	storageKey string,
	photoType string,
	consentStatus string,
) (int64, error) {
	// 只允许本人照片和参考图片。
	if photoType != PhotoTypePortrait &&
		photoType != PhotoTypeReference {
		return 0, ErrInvalidPhotoType
	}

	var photoID int64

	err := r.db.QueryRow(
		ctx,
		`INSERT INTO user_photos
			(user_id, storage_key, photo_type, consent_status)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		userID,
		storageKey,
		photoType,
		consentStatus,
	).Scan(&photoID)

	if err != nil {
		return 0, err
	}

	return photoID, nil
}

// FindByIDForUser 根据照片 ID 和用户 ID 查询照片。
// 只允许查询已授权且未删除的照片。
func (r *PhotoRepository) FindByIDForUser(
	ctx context.Context,
	userID int64,
	photoID int64,
) (*Photo, error) {
	var photo Photo

	err := r.db.QueryRow(
		ctx,
		`SELECT
			id,
			user_id,
			storage_key,
			photo_type,
			consent_status
		FROM user_photos
	WHERE id = $1
    AND user_id = $2
    AND photo_type = 'portrait'
    AND consent_status = 'granted'
    AND deleted_at IS NULL`,
		photoID,
		userID,
	).Scan(
		&photo.ID,
		&photo.UserID,
		&photo.StorageKey,
		&photo.PhotoType,
		&photo.ConsentStatus,
	)

	if err != nil {
		return nil, err
	}

	return &photo, nil
}

// FindReferencesByTaskID 查询设计任务关联的参考图片。
// 只返回属于任务用户、已授权且未删除的参考图片。
func (r *PhotoRepository) FindReferencesByTaskID(
	ctx context.Context,
	userID int64,
	taskID int64,
) ([]Photo, error) {
	rows, err := r.db.Query(
		ctx,
		`SELECT
			p.id,
			p.user_id,
			p.storage_key,
			p.photo_type,
			p.consent_status
		FROM design_task_photos dtp
		INNER JOIN user_photos p
			ON p.id = dtp.photo_id
		INNER JOIN design_tasks t
			ON t.id = dtp.task_id
		WHERE dtp.task_id = $1
			AND t.user_id = $2
			AND p.user_id = $2
			AND p.photo_type = 'reference'
			AND p.consent_status = 'granted'
			AND p.deleted_at IS NULL
		ORDER BY p.id ASC`,
		taskID,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	photos := make([]Photo, 0)

	for rows.Next() {
		var photo Photo

		err := rows.Scan(
			&photo.ID,
			&photo.UserID,
			&photo.StorageKey,
			&photo.PhotoType,
			&photo.ConsentStatus,
		)
		if err != nil {
			return nil, err
		}

		photos = append(photos, photo)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return photos, nil
}
