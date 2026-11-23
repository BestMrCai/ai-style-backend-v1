package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"
	"time"

	"ai-style-backend/internal/storage"
)

var (
	ErrPhotoEmpty       = errors.New("photo file is empty")
	ErrPhotoTooLarge    = errors.New("photo file is too large")
	ErrPhotoTypeInvalid = errors.New("photo must be a valid JPEG or PNG image")
	ErrPhotoDimensions  = errors.New("photo dimensions are too large")
)

const (
	MaxPhotoSize   int64 = 5 * 1024 * 1024 // 文件最大 5 MB
	MaxPhotoWidth        = 10000
	MaxPhotoHeight       = 10000
	MaxPhotoPixels int64 = 40_000_000 // 最多 4000 万像素
)

// PhotoService 负责校验并保存图片。
//
// 具体落到本地磁盘还是云存储由 storage.Storage 决定，
// 这一层只做「文件合法性校验 + key 生成」。
type PhotoService struct {
	store storage.Storage
}

// NewPhotoService 创建图片服务。
func NewPhotoService(store storage.Storage) *PhotoService {
	return &PhotoService{store: store}
}

// timeNow 抽成变量，测试时可以固定时间以断言日期目录。
var timeNow = time.Now

// Storage 暴露底层实现，供需要直读的对象使用（例如后台 Worker）。
func (s *PhotoService) Storage() storage.Storage {
	return s.store
}

// Save 校验并保存图片，返回存储 key 和字节数。
//
// dir 取 storage.DirUserPhoto 或 storage.DirFaceAnalysis，
// 实际路径形如 upload/user/20261118/<random>.jpg。
func (s *PhotoService) Save(ctx context.Context, src io.Reader, dir string) (string, int64, error) {
	if src == nil {
		return "", 0, ErrPhotoEmpty
	}

	// 最多读取 5 MB + 1 字节，以便识别超大文件。
	data, err := io.ReadAll(io.LimitReader(src, MaxPhotoSize+1))
	if err != nil {
		return "", 0, fmt.Errorf("read photo: %w", err)
	}

	size := int64(len(data))
	if size == 0 {
		return "", 0, ErrPhotoEmpty
	}
	if size > MaxPhotoSize {
		return "", 0, ErrPhotoTooLarge
	}

	ext, err := validateImage(data)
	if err != nil {
		return "", 0, err
	}

	fileName, err := storage.NewFileName(ext)
	if err != nil {
		return "", 0, err
	}

	key, err := storage.BuildKey(dir, fileName, timeNow())
	if err != nil {
		return "", 0, err
	}

	if err := s.store.Put(ctx, key, data); err != nil {
		return "", 0, fmt.Errorf("save photo: %w", err)
	}

	return key, size, nil
}

// Delete 删除 Save 保存的图片。对象不存在时返回 nil。
func (s *PhotoService) Delete(ctx context.Context, storageKey string) error {
	if err := storage.ValidateKey(storageKey); err != nil {
		return err
	}
	return s.store.Delete(ctx, storageKey)
}

// Read 根据 storage_key 读取图片文件。
func (s *PhotoService) Read(ctx context.Context, storageKey string) ([]byte, error) {
	if err := storage.ValidateKey(storageKey); err != nil {
		return nil, err
	}

	data, err := s.store.Get(ctx, storageKey)
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return nil, ErrPhotoEmpty
	}
	// 用户上传的大图在入口已经拦过；这里再兜一层，
	// 防止外部写入了异常大对象后被整个读进内存。
	if int64(len(data)) > MaxPhotoSize*4 {
		return nil, ErrPhotoTooLarge
	}

	return data, nil
}

// validateImage 校验文件确实是 JPEG/PNG，并返回推荐扩展名。
//
// 只看文件头不够：伪造 Content-Type 很容易，
// 所以再用 image.DecodeConfig 确认能真正解析出宽高。
func validateImage(data []byte) (string, error) {
	contentType := http.DetectContentType(data)

	var expectedExt string
	switch contentType {
	case "image/jpeg":
		expectedExt = ".jpg"
	case "image/png":
		expectedExt = ".png"
	default:
		return "", ErrPhotoTypeInvalid
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", ErrPhotoTypeInvalid
	}

	if (expectedExt == ".jpg" && format != "jpeg") ||
		(expectedExt == ".png" && format != "png") {
		return "", ErrPhotoTypeInvalid
	}

	if config.Width <= 0 || config.Height <= 0 ||
		config.Width > MaxPhotoWidth ||
		config.Height > MaxPhotoHeight ||
		int64(config.Width)*int64(config.Height) > MaxPhotoPixels {
		return "", ErrPhotoDimensions
	}

	return strings.TrimPrefix(expectedExt, "."), nil
}
