package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// 存储目录约定。所有 key 都以这些前缀开头，换 OSS 时不用改业务代码。
const (
	// DirUserPhoto 是用户上传的原始照片。
	DirUserPhoto = "upload/user"
	// DirFaceAnalysis 面部分析产物：标注图与 AI 合成效果图。
	DirFaceAnalysis = "upload/face_analyses"
)

var (
	// ErrInvalidKey 表示 key 为空、含路径穿越或前缀不在白名单内。
	ErrInvalidKey = errors.New("invalid storage key")
	// ErrObjectNotFound 表示对象不存在。
	ErrObjectNotFound = errors.New("storage object not found")
)

// Storage 是对象存储的最小抽象。
//
// 只保留 Put/Get/Delete 三个动作：
//   - 业务层写图时只关心 key，不关心落到磁盘还是云上；
//   - 读图统一走服务端代理，小程序不需要知道存储域名，
//     也不会有跨域和防盗链配置问题。
//
// 换 OSS 时只要再实现一个 Storage，main.go 里换一行装配即可。
type Storage interface {
	// Put 写入对象。key 由 BuildKey 生成，实现方不做路径推断。
	Put(ctx context.Context, key string, data []byte) error
	// Get 读取对象内容。对象不存在时应返回 ErrObjectNotFound。
	Get(ctx context.Context, key string) ([]byte, error)
	// Delete 删除对象。对象不存在时不应视为错误。
	Delete(ctx context.Context, key string) error
	// Driver 返回驱动名，用于启动日志。
	Driver() string
}

// BuildKey 按「前缀/日期/文件名」生成存储 key。
//
// 日期子目录的意义：单个目录里堆几十万个文件，
// 本地文件系统 ls 和 OSS 控制台都会很难用。
func BuildKey(dir, fileName string, at time.Time) (string, error) {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if !allowedDir(dir) {
		return "", fmt.Errorf("%w: dir %q is not allowed", ErrInvalidKey, dir)
	}

	fileName = strings.TrimSpace(fileName)
	if err := validateSegment(fileName); err != nil {
		return "", err
	}

	key := path.Join(dir, at.Format("20060102"), fileName)
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	return key, nil
}

// NewFileName 生成随机文件名，保留调用方给的扩展名。
// 不使用用户提供的文件名，避免路径穿越和中文编码问题。
func NewFileName(ext string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate file name: %w", err)
	}

	ext = strings.TrimSpace(ext)
	if ext == "" {
		ext = ".jpg"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return hex.EncodeToString(raw[:]) + ext, nil
}

// ValidateKey 校验 key 合法性。
//
// 历史数据里还有 uploads/xxx.jpg 这种平铺 key，
// 所以这里放宽到「upload/ 或 uploads/ 开头」，其余一律拒绝。
func ValidateKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" || strings.Contains(key, "\\") {
		return ErrInvalidKey
	}

	clean := path.Clean(key)
	if clean != key {
		return ErrInvalidKey
	}
	if strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return ErrInvalidKey
	}

	if !strings.HasPrefix(clean, "upload/") && !strings.HasPrefix(clean, "uploads/") {
		return fmt.Errorf("%w: unexpected prefix", ErrInvalidKey)
	}

	for _, segment := range strings.Split(clean, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ErrInvalidKey
		}
	}

	return nil
}

func allowedDir(dir string) bool {
	switch dir {
	case DirUserPhoto, DirFaceAnalysis:
		return true
	default:
		return false
	}
}

func validateSegment(name string) error {
	if name == "" || name == "." || name == ".." {
		return ErrInvalidKey
	}
	if strings.ContainsAny(name, "/\\") {
		return ErrInvalidKey
	}
	return nil
}
