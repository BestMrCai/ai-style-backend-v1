package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Env       string
	Port      string
	DBHost    string
	DBPort    string
	DBUser    string
	DBPass    string
	DBName    string
	DBSSLMode string
	DBMax     int32
	DBMin     int32

	WXAppID     string
	WXAppSecret string

	AuthTokenSecret string
	// AI-related configuration
	AIProvider string
	// AIAllowRealCall 是真实出图的显式开关。
	// 开发环境下默认关闭：mock 生成器足以验证扣费、记录、退款等业务逻辑，
	// 真实调用会产生费用，必须由人明确点头才放行。
	AIAllowRealCall  bool
	AIAPIKey         string
	AIModel          string
	AIBaseURL        string
	AITimeoutSeconds int
	// AIImageSize 是生成图尺寸，形如 1024x1280。
	// 尺寸直接影响单张计价，因此单独抽成配置。
	AIImageSize string

	FaceServiceURL     string
	FaceTimeoutSeconds int

	// 每日免费次数上限。两个功能分开计：
	// 脸型分析便宜、AI 出图贵，共用一档会让成本失控。
	DailyFaceAnalysisLimit int
	DailyHairstyleLimit    int

	// 免费次数用完后单次消耗的积分。设为 0 表示该功能不单独收费。
	CreditCostFaceAnalysis int
	CreditCostHairstyle    int

	// Storage 相关：本地磁盘（开发）或七牛云（生产）。
	StorageDriver string
	// LocalStorageRoot 是本地驱动的根目录，key 会拼在它下面。
	LocalStorageRoot string

	QiniuAccessKey string
	QiniuSecretKey string
	QiniuBucket    string
	QiniuDomain    string
	QiniuUploadURL string
	QiniuAPIHost   string
	// QiniuPrivate 控制读图是否带私有空间凭证：auto / true / false。
	// auto 时先直连，401 后自动改用签名并记住。
	QiniuPrivate string
}

func Load() (Config, error) {
	// A missing .env file is okay when environment variables are supplied directly.
	_ = godotenv.Load()

	maxConns, err := intEnv("DB_MAX_CONNS", 5)
	if err != nil {
		return Config{}, err
	}
	minConns, err := intEnv("DB_MIN_CONNS", 1)
	if err != nil {
		return Config{}, err
	}

	aiTimeout, err := intEnv("AI_TIMEOUT_SECONDS", 60)
	if err != nil {
		return Config{}, err
	}

	if aiTimeout < 1 {
		return Config{}, fmt.Errorf("AI_TIMEOUT_SECONDS must be greater than 0")
	}

	faceTimeout, err := intEnv("FACE_TIMEOUT_SECONDS", 90)
	if err != nil {
		return Config{}, err
	}
	if faceTimeout < 1 {
		return Config{}, fmt.Errorf("FACE_TIMEOUT_SECONDS must be greater than 0")
	}

	if minConns < 0 || maxConns < 1 || minConns > maxConns {
		return Config{}, fmt.Errorf("invalid DB pool sizes: DB_MIN_CONNS must be >= 0 and <= DB_MAX_CONNS, DB_MAX_CONNS must be >= 1")
	}
	storageDriver := getEnv("STORAGE_DRIVER", "local")
	switch storageDriver {
	case "local", "qiniu":
	default:
		return Config{}, fmt.Errorf("unsupported STORAGE_DRIVER: %s", storageDriver)
	}

	appEnv := getEnv("APP_ENV", "development")

	aiProvider := getEnv("AI_PROVIDER", "mock")
	switch aiProvider {
	case "mock", "volc":
		// volc = 火山方舟 Ark / Seedream 图像生成
	default:
		return Config{}, fmt.Errorf("unsupported AI_PROVIDER: %s", aiProvider)
	}

	// 防误伤：开发环境用真实模型会真金白银扣费。
	// 默认拒绝启动，逼调用方显式写 AI_ALLOW_REAL_CALL=true。
	// 生产环境（APP_ENV != development）不做这个限制，否则线上没法跑。
	aiAllowRealCall := isTruthy(getEnv("AI_ALLOW_REAL_CALL", ""))
	if appEnv == "development" && aiProvider != "mock" && !aiAllowRealCall {
		return Config{}, fmt.Errorf(
			"AI_PROVIDER=%s 会在开发环境产生真实调用费用；"+
				"确认要跑真的就设置 AI_ALLOW_REAL_CALL=true，"+
				"或者把 AI_PROVIDER 改成 mock",
			aiProvider,
		)
	}

	faceLimit, err := intEnv("DAILY_FACE_ANALYSIS_LIMIT", 10)
	if err != nil {
		return Config{}, err
	}
	if faceLimit < 0 {
		return Config{}, fmt.Errorf("DAILY_FACE_ANALYSIS_LIMIT must be >= 0")
	}

	hairstyleLimit, err := intEnv("DAILY_HAIRSTYLE_LIMIT", 2)
	if err != nil {
		return Config{}, err
	}
	if hairstyleLimit < 0 {
		return Config{}, fmt.Errorf("DAILY_HAIRSTYLE_LIMIT must be >= 0")
	}

	faceCost, err := intEnv("CREDIT_COST_FACE_ANALYSIS", 8)
	if err != nil {
		return Config{}, err
	}
	if faceCost < 0 {
		return Config{}, fmt.Errorf("CREDIT_COST_FACE_ANALYSIS must be >= 0")
	}

	hairstyleCost, err := intEnv("CREDIT_COST_HAIRSTYLE", 15)
	if err != nil {
		return Config{}, err
	}
	if hairstyleCost < 0 {
		return Config{}, fmt.Errorf("CREDIT_COST_HAIRSTYLE must be >= 0")
	}

	return Config{
		Env:       appEnv,
		Port:      getEnv("APP_PORT", "8080"),
		DBHost:    getEnv("DB_HOST", "localhost"),
		DBPort:    getEnv("DB_PORT", "5432"),
		DBUser:    getEnv("DB_USER", "ai_style"),
		DBPass:    getEnv("DB_PASSWORD", "change_me_local"),
		DBName:    getEnv("DB_NAME", "ai_style_db"),
		DBSSLMode: getEnv("DB_SSLMODE", "disable"),
		DBMax:     maxConns,
		DBMin:     minConns,

		WXAppID:     getEnv("WX_APP_ID", ""),
		WXAppSecret: getEnv("WX_APP_SECRET", ""),

		AuthTokenSecret: getEnv("AUTH_TOKEN_SECRET", ""),

		AIProvider:       aiProvider,
		AIAllowRealCall:  aiAllowRealCall,
		AIAPIKey:         getEnv("AI_API_KEY", ""),
		AIModel:          getEnv("AI_MODEL", ""),
		AIBaseURL:        getEnv("AI_BASE_URL", "https://api.openai.com/v1"),
		AITimeoutSeconds: int(aiTimeout),
		AIImageSize:      getEnv("AI_IMAGE_SIZE", "1024x1280"),

		FaceServiceURL:     strings.TrimRight(getEnv("FACE_SERVICE_URL", "http://localhost:8001"), "/"),
		FaceTimeoutSeconds: int(faceTimeout),

		DailyFaceAnalysisLimit: int(faceLimit),
		DailyHairstyleLimit:    int(hairstyleLimit),
		CreditCostFaceAnalysis: int(faceCost),
		CreditCostHairstyle:    int(hairstyleCost),

		StorageDriver: storageDriver,
		// 默认是工作目录而非 uploads：key 本身已带 upload/ 前缀，
		// 老数据的 uploads/xxx.jpg 也能落到同一层解析成功。
		LocalStorageRoot: getEnv("STORAGE_LOCAL_ROOT", "."),

		QiniuAccessKey: getEnv("QINIU_ACCESS_KEY", ""),
		QiniuSecretKey: getEnv("QINIU_SECRET_KEY", ""),
		QiniuBucket:    getEnv("QINIU_BUCKET", ""),
		QiniuDomain:    getEnv("QINIU_DOMAIN", ""),
		QiniuUploadURL: getEnv("QINIU_UPLOAD_URL", "https://up.qiniup.com"),
		QiniuAPIHost:   getEnv("QINIU_API_HOST", "https://rs.qiniuapi.com"),
		QiniuPrivate:   getEnv("QINIU_PRIVATE", "auto"),
	}, nil
}

func (c Config) DatabaseURL() string {
	// URL-encode special characters in DB credentials before using this in production.
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s&pool_max_conns=%d&pool_min_conns=%d",
		c.DBUser, c.DBPass, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode, c.DBMax, c.DBMin)
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// isTruthy 把常见的"真值"写法统一成 bool。
// 不用 strconv.ParseBool：那个不接受 yes/on，运维容易写错。
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func intEnv(key string, fallback int32) (int32, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return int32(parsed), nil
}
