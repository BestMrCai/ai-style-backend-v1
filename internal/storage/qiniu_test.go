package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 私有空间：首次直连 401，自动改用签名重试，并把状态记住。
func TestQiniuGetAutoDetectsPrivateBucket(t *testing.T) {
	var unsignedCalls, signedCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") == "" {
			atomic.AddInt32(&unsignedCalls, 1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt32(&signedCalls, 1)
		_, _ = w.Write([]byte("image-bytes"))
	}))
	defer server.Close()

	store, err := NewQiniuStorage(QiniuOptions{
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "bucket",
		Domain:    server.URL,
	})
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	key := "upload/user/20261118/a.jpg"

	got, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	if string(got) != "image-bytes" {
		t.Errorf("unexpected payload: %s", got)
	}
	if unsignedCalls != 1 || signedCalls != 1 {
		t.Errorf("expected 1 unsigned + 1 signed, got %d/%d", unsignedCalls, signedCalls)
	}

	// 第二次：已经记住是私有空间，直接用签名，不再探测。
	unsignedCalls, signedCalls = 0, 0
	if _, err := store.Get(context.Background(), key); err != nil {
		t.Fatalf("second get: %v", err)
	}
	if unsignedCalls != 0 {
		t.Errorf("should not probe again, unsigned calls=%d", unsignedCalls)
	}
	if signedCalls != 1 {
		t.Errorf("expected 1 signed call, got %d", signedCalls)
	}
}

// 公有空间：一次直连成功，不应出现签名请求。
func TestQiniuGetPublicBucketSingleCall(t *testing.T) {
	var calls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Query().Get("token") != "" {
			t.Error("public bucket should not be signed")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	store, err := NewQiniuStorage(QiniuOptions{
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "bucket",
		Domain:    server.URL,
		Private:   "false",
	})
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	if _, err := store.Get(context.Background(), "upload/user/20261118/a.jpg"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 call, got %d", calls)
	}
}

// 下载凭证必须按「完整 URL 含 ?e=」签名。
// 用相对路径签会得到 "download token auth failed"，这是实测踩过的坑。
func TestQiniuPrivateTokenSignature(t *testing.T) {
	store, err := NewQiniuStorage(QiniuOptions{
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "bucket",
		Domain:    "https://cdn.example.com",
		Private:   "true",
	})
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	key := "upload/user/20261118/a b.jpg"
	target, err := store.signedURL(key)
	if err != nil {
		t.Fatalf("signed url: %v", err)
	}

	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	values := parsed.Query()
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}

	deadline := values.Get("e")
	if deadline == "" {
		t.Fatal("missing e param")
	}
	if values.Get("token") == "" {
		t.Fatal("missing token param")
	}

	// 独立重算：签名原文是不含 token 的完整 URL。
	unsigned := store.domain + "/" + escapeKey(key) + "?e=" + deadline
	mac := hmac.New(sha1.New, []byte("sk"))
	mac.Write([]byte(unsigned))
	want := base64.URLEncoding.EncodeToString(mac.Sum(nil))

	if values.Get("token") != "ak:"+want {
		t.Errorf("signature mismatch:\n got %s\nwant ak:%s", values.Get("token"), want)
	}

	dl := mustInt64(t, deadline)
	diff := time.Until(time.Unix(dl, 0))
	if diff < 110*time.Minute || diff > 130*time.Minute {
		t.Errorf("unexpected ttl: %v", diff)
	}
}

// 带签名仍 401 时要有明确提示，而不是继续重试。
func TestQiniuSignedStill401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	store, err := NewQiniuStorage(QiniuOptions{
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "b",
		Domain:    server.URL,
		Private:   "true",
	})
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	_, err = store.Get(context.Background(), "upload/user/20261118/a.jpg")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "check access key") {
		t.Errorf("unhelpful error: %v", err)
	}
}

// 404 必须映射成 ErrObjectNotFound，上层据此返回 404 而不是 500。
func TestQiniuGetNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	store, err := NewQiniuStorage(QiniuOptions{
		AccessKey: "ak", SecretKey: "sk", Bucket: "b", Domain: server.URL,
	})
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}

	_, err = store.Get(context.Background(), "upload/user/20261118/a.jpg")
	if err != ErrObjectNotFound {
		t.Errorf("expected ErrObjectNotFound, got %v", err)
	}
}

func TestQiniuDomainAutoScheme(t *testing.T) {
	store, err := NewQiniuStorage(QiniuOptions{
		AccessKey: "ak", SecretKey: "sk", Bucket: "b", Domain: "cdn.example.com",
	})
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	if store.domain != "https://cdn.example.com" {
		t.Errorf("scheme not added: %s", store.domain)
	}
}

func mustInt64(t *testing.T, s string) int64 {
	t.Helper()
	var v int64
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("not a number: %s", s)
		}
		v = v*10 + int64(c-'0')
	}
	return v
}
