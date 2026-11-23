package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

// key 必须是「前缀/日期/文件名」，日期目录不能省。
func TestBuildKeyUsesDateSubDir(t *testing.T) {
	at := time.Date(2026, 11, 18, 10, 30, 0, 0, time.UTC)

	key, err := BuildKey(DirUserPhoto, "abc123.jpg", at)
	if err != nil {
		t.Fatalf("build key: %v", err)
	}

	want := "upload/user/20261118/abc123.jpg"
	if key != want {
		t.Errorf("got %q, want %q", key, want)
	}
}

func TestBuildKeyForFaceAnalysisDir(t *testing.T) {
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	key, err := BuildKey(DirFaceAnalysis, "x.jpg", at)
	if err != nil {
		t.Fatalf("build key: %v", err)
	}
	if key != "upload/face_analyses/20260102/x.jpg" {
		t.Errorf("unexpected key: %s", key)
	}
}

// 只允许两个业务目录，防止误写到别处。
func TestBuildKeyRejectsUnknownDir(t *testing.T) {
	_, err := BuildKey("upload/other", "x.jpg", time.Now())
	if err == nil {
		t.Fatal("expected error for unknown dir")
	}
}

// 文件名不允许带路径分隔符，否则可以跳出日期目录。
func TestBuildKeyRejectsPathInFileName(t *testing.T) {
	for _, name := range []string{"../x.jpg", "a/b.jpg", "..", ""} {
		if _, err := BuildKey(DirUserPhoto, name, time.Now()); err == nil {
			t.Errorf("expected error for file name %q", name)
		}
	}
}

func TestValidateKeyAcceptsLegacyFlatKey(t *testing.T) {
	// 历史数据是 uploads/xxx.jpg，不能因为迁移直接读不到。
	if err := ValidateKey("uploads/1a2b3c.jpg"); err != nil {
		t.Errorf("legacy key should be accepted: %v", err)
	}
}

func TestValidateKeyRejectsTraversal(t *testing.T) {
	bad := []string{
		"upload/../etc/passwd",
		"upload/user/../../x.jpg",
		"/upload/user/x.jpg",
		"upload\\user\\x.jpg",
		"other/x.jpg",
		"",
	}
	for _, key := range bad {
		if err := ValidateKey(key); err == nil {
			t.Errorf("expected error for key %q", key)
		}
	}
}

func TestNewFileNameKeepsExtension(t *testing.T) {
	name, err := NewFileName("png")
	if err != nil {
		t.Fatalf("new file name: %v", err)
	}
	if !strings.HasSuffix(name, ".png") {
		t.Errorf("extension lost: %s", name)
	}
	if len(strings.TrimSuffix(name, ".png")) != 32 {
		t.Errorf("unexpected random part length: %s", name)
	}
}

func TestLocalStorageRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalStorage(dir)
	ctx := context.Background()

	key, err := BuildKey(DirUserPhoto, "a.jpg", time.Now())
	if err != nil {
		t.Fatalf("build key: %v", err)
	}

	payload := []byte("hello-image")
	if err := store.Put(ctx, key, payload); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload mismatch: %q", got)
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := store.Get(ctx, key); err != ErrObjectNotFound {
		t.Errorf("expected ErrObjectNotFound after delete, got %v", err)
	}
}

// 删除不存在的对象不能报错：清理逻辑不该因为重复删除而失败。
func TestLocalStorageDeleteMissingIsNoop(t *testing.T) {
	store := NewLocalStorage(t.TempDir())
	key, _ := BuildKey(DirFaceAnalysis, "missing.jpg", time.Now())

	if err := store.Delete(context.Background(), key); err != nil {
		t.Errorf("delete missing should be noop, got %v", err)
	}
}

func TestQiniuUploadTokenFormat(t *testing.T) {
	store, err := NewQiniuStorage(QiniuOptions{
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "bucket",
		Domain:    "https://cdn.example.com",
	})
	if err != nil {
		t.Fatalf("new qiniu storage: %v", err)
	}

	token := store.uploadToken("upload/user/20261118/x.jpg")

	// 格式必须是 ak:sign:policy，三段都不能为空。
	parts := strings.Split(token, ":")
	if len(parts) != 3 {
		t.Fatalf("token should have 3 parts, got %d: %s", len(parts), token)
	}
	if parts[0] != "ak" {
		t.Errorf("access key mismatch: %s", parts[0])
	}
	for i, part := range parts[1:] {
		if part == "" {
			t.Errorf("token part %d is empty", i+1)
		}
	}
}

func TestQiniuRequiresConfig(t *testing.T) {
	cases := []QiniuOptions{
		{SecretKey: "sk", Bucket: "b", Domain: "https://d.com"},
		{AccessKey: "ak", Bucket: "b", Domain: "https://d.com"},
		{AccessKey: "ak", SecretKey: "sk", Domain: "https://d.com"},
		{AccessKey: "ak", SecretKey: "sk", Bucket: "b"},
	}
	for i, opt := range cases {
		if _, err := NewQiniuStorage(opt); err == nil {
			t.Errorf("case %d should fail", i)
		}
	}
}
