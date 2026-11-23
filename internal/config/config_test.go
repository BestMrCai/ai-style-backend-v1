package config

import (
	"os"
	"strings"
	"testing"
)

// isTruthy 要把运维可能写的各种"真"都认出来。
// 用 strconv.ParseBool 会漏掉 yes/on，那种疏忽会让人以为开关没生效。
func TestIsTruthy(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "True", "yes", "YES", "on", "ON", " true ", "on\n"}
	for _, v := range truthy {
		if !isTruthy(v) {
			t.Errorf("%q should be truthy", v)
		}
	}

	falsy := []string{"", "0", "false", "no", "off", "2", "yes!", "tru"}
	for _, v := range falsy {
		if isTruthy(v) {
			t.Errorf("%q should not be truthy", v)
		}
	}
}

// 开发环境用真实 provider 必须显式放行：这是防误伤真实额度的护栏。
func TestLoadRejectsRealProviderInDevelopment(t *testing.T) {
	setBaseEnv(t)
	os.Setenv("APP_ENV", "development")
	os.Setenv("AI_PROVIDER", "volc")
	os.Setenv("AI_ALLOW_REAL_CALL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when development uses volc without AI_ALLOW_REAL_CALL")
	}
	// 报错必须告诉人怎么解决，否则只会让人去翻代码。
	if !strings.Contains(err.Error(), "AI_ALLOW_REAL_CALL") {
		t.Errorf("error should mention the switch, got: %v", err)
	}
}

// 显式打开开关就放行。
func TestLoadAllowsRealProviderWhenExplicitlyEnabled(t *testing.T) {
	setBaseEnv(t)
	os.Setenv("APP_ENV", "development")
	os.Setenv("AI_PROVIDER", "volc")
	os.Setenv("AI_ALLOW_REAL_CALL", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.AIAllowRealCall {
		t.Error("AIAllowRealCall should be true")
	}
}

// 生产环境不该被这个开关卡住：线上就得跑真模型。
func TestLoadAllowsRealProviderInProduction(t *testing.T) {
	setBaseEnv(t)
	os.Setenv("APP_ENV", "production")
	os.Setenv("AI_PROVIDER", "volc")
	os.Setenv("AI_ALLOW_REAL_CALL", "")

	if _, err := Load(); err != nil {
		t.Fatalf("production should not be blocked: %v", err)
	}
}

// mock 永远可用，不受开关影响：验证业务逻辑不该有任何门槛。
func TestLoadAllowsMockInDevelopment(t *testing.T) {
	setBaseEnv(t)
	os.Setenv("APP_ENV", "development")
	os.Setenv("AI_PROVIDER", "mock")
	os.Setenv("AI_ALLOW_REAL_CALL", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("mock should always be allowed: %v", err)
	}
	if cfg.AIProvider != "mock" {
		t.Errorf("provider = %s, want mock", cfg.AIProvider)
	}
}

// setBaseEnv 把 Load 需要的最小变量填好，避免测试之间互相污染。
func setBaseEnv(t *testing.T) {
	t.Helper()

	// 不读 .env，避免开发者本地的真实配置影响测试结果。
	t.Setenv("APP_ENV", "development")
	t.Setenv("AI_ALLOW_REAL_CALL", "")
	t.Setenv("AUTH_TOKEN_SECRET", strings.Repeat("a", 32))
	t.Setenv("DAILY_FACE_ANALYSIS_LIMIT", "10")
	t.Setenv("DAILY_HAIRSTYLE_LIMIT", "2")
	t.Setenv("CREDIT_COST_FACE_ANALYSIS", "8")
	t.Setenv("CREDIT_COST_HAIRSTYLE", "15")
}
