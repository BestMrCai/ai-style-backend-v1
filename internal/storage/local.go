package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// LocalStorage 把对象存到本地磁盘，用于开发环境或没有 OSS 的场景。
//
// 目录结构直接沿用 key：<root>/upload/user/20261118/xxx.jpg
type LocalStorage struct {
	root string
}

// NewLocalStorage 创建本地存储。root 为空表示当前工作目录。
func NewLocalStorage(root string) *LocalStorage {
	if root == "" {
		root = "."
	}
	return &LocalStorage{root: root}
}

// Driver 实现 Storage。
func (s *LocalStorage) Driver() string { return "local" }

// Put 实现 Storage。
func (s *LocalStorage) Put(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateKey(key); err != nil {
		return err
	}

	full := s.path(key)

	// 每个对象都在自己的日期目录下，MkdirAll 负责建整条链路。
	if err := os.MkdirAll(filepath.Dir(full), 0750); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	if err := os.WriteFile(full, data, 0600); err != nil {
		return fmt.Errorf("write object: %w", err)
	}
	return nil
}

// Get 实现 Storage。
func (s *LocalStorage) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateKey(key); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(s.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	return data, nil
}

// Delete 实现 Storage。文件不存在时静默成功。
func (s *LocalStorage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateKey(key); err != nil {
		return err
	}

	err := os.Remove(s.path(key))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete object: %w", err)
	}
	// 顺手清理空的日期目录，避免长期运行后留下大量空目录。
	_ = os.Remove(filepath.Dir(s.path(key)))
	return nil
}

func (s *LocalStorage) path(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}
