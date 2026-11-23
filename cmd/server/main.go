package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ai-style-backend/internal/ai"
	"ai-style-backend/internal/config"
	"ai-style-backend/internal/face"
	"ai-style-backend/internal/hairstyle"
	"ai-style-backend/internal/handler"
	"ai-style-backend/internal/repository"
	"ai-style-backend/internal/service"
	"ai-style-backend/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL())
	if err != nil {
		log.Fatalf("create database pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("connect to PostgreSQL: %v", err)
	}
	log.Println("PostgreSQL connection established")

	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	handler.RegisterDesignTypeRoutes(router)

	// 初始化用户仓库和用户服务。
	userRepo := repository.NewUserRepository(pool)
	userService := service.NewUserService(userRepo)
	// 初始化微信客户端和登录服务。
	wechatClient := service.NewWeChatClient(
		cfg.WXAppID,
		cfg.WXAppSecret,
	)
	loginService := service.NewLoginService(
		wechatClient,
		userService,
	)

	// 注册微信登录路由。
	handler.RegisterAuthRoutes(router, loginService, cfg.AuthTokenSecret)
	// 初始化对象存储。本地磁盘用于开发，七牛云用于线上；
	// 业务层只依赖 storage.Storage，换云厂商时改动只在这一处。
	var store storage.Storage

	switch cfg.StorageDriver {
	case "local":
		store = storage.NewLocalStorage(cfg.LocalStorageRoot)
		log.Printf("storage: local (%s)", cfg.LocalStorageRoot)

	case "qiniu":
		qiniu, err := storage.NewQiniuStorage(storage.QiniuOptions{
			AccessKey: cfg.QiniuAccessKey,
			SecretKey: cfg.QiniuSecretKey,
			Bucket:    cfg.QiniuBucket,
			Domain:    cfg.QiniuDomain,
			UploadURL: cfg.QiniuUploadURL,
			APIHost:   cfg.QiniuAPIHost,
			Private:   cfg.QiniuPrivate,
		})
		if err != nil {
			log.Fatalf("init qiniu storage: %v", err)
		}
		store = qiniu
		log.Printf("storage: qiniu (bucket=%s, domain=%s)", cfg.QiniuBucket, cfg.QiniuDomain)

	default:
		log.Fatalf("unsupported storage driver: %s", cfg.StorageDriver)
	}

	// 初始化照片存储服务和数据库仓库。
	photoService := service.NewPhotoService(store)
	photoRepo := repository.NewPhotoRepository(pool)

	// 注册照片上传接口。
	handler.RegisterPhotoRoutes(
		router,
		photoService,
		photoRepo,
		cfg.AuthTokenSecret,
	)
	taskRepo := repository.NewDesignTaskRepository(pool)
	faceAnalysisRepo := repository.NewFaceAnalysisRepository(pool)

	// 每日次数限制：按业务时区（东八区）划分自然日。
	// 数据库容器默认 UTC，如果直接用 CURRENT_DATE，
	// 北京时间早上 8 点之前会算到前一天去。
	quotaLoc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		quotaLoc = time.FixedZone("CST", 8*60*60)
	}
	quotaRepo := repository.NewDailyQuotaRepository(pool, quotaLoc)
	creditRepo := repository.NewCreditRepository(pool)
	usageRepo := repository.NewUsageRecordRepository(pool)
	quotaService := service.NewQuotaService(
		quotaRepo,
		creditRepo,
		usageRepo,
		cfg.DailyFaceAnalysisLimit,
		cfg.DailyHairstyleLimit,
		cfg.CreditCostFaceAnalysis,
		cfg.CreditCostHairstyle,
	)
	log.Printf("daily quota: face_analysis=%d(免费)/%d积分, hairstyle_design=%d(免费)/%d积分",
		cfg.DailyFaceAnalysisLimit, cfg.CreditCostFaceAnalysis,
		cfg.DailyHairstyleLimit, cfg.CreditCostHairstyle)

	// 创建 AI 模拟生成器。
	// 后续接入真实 AI 模型时，再替换这里。
	// 根据配置选择 AI 生成器。
	var generator ai.Generator

	switch cfg.AIProvider {
	case "mock":
		generator = ai.NewMockGenerator()
		log.Println("AI generator: mock")

	case "volc":
		// 火山方舟 Ark / Seedream。超时按配置传入，
		// 生图比文本模型慢，默认值已在 .env 里调大。
		generator = ai.NewVolcGenerator(
			cfg.AIAPIKey,
			cfg.AIModel,
			cfg.AIBaseURL,
			cfg.AIImageSize,
			time.Duration(cfg.AITimeoutSeconds)*time.Second,
		)
		// 启动时把"会不会花钱"说清楚，日志翻出来一眼能确认。
		log.Printf("AI generator: volc (%s, size=%s) —— 真实调用会产生费用", cfg.AIModel, cfg.AIImageSize)

		// 缺 key 或 model 时任务会全部失败，启动时明确提示一次，
		// 避免用户在小程序里只看到"合成失败"却不知道原因。
		if cfg.AIAPIKey == "" || cfg.AIModel == "" {
			log.Println("WARN: AI_API_KEY / AI_MODEL 未配置，发型合成任务会直接失败")
		}

	default:
		log.Fatalf("unsupported AI provider: %s", cfg.AIProvider)
	}

	// 加载发型素材库。素材在 data/hairstyles/*.json，运营可直接修改。
	styleLibrary, err := hairstyle.Load("data/hairstyles")
	if err != nil {
		log.Fatalf("load hairstyle library: %v", err)
	}
	log.Printf("hairstyle library loaded: %d styles", len(styleLibrary.All()))

	taskService := service.NewDesignTaskService(
		taskRepo,
		photoRepo,
		photoService,
		generator,
		styleLibrary,
		faceAnalysisRepo,
		quotaService,
	)

	// 启动后台设计任务 Worker
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		log.Println("Design task worker started")

		for {
			select {
			case <-workerCtx.Done():
				log.Println("Design task worker stopped")
				return

			case <-ticker.C:
				processed, err := taskService.ProcessNextTask(workerCtx)
				if err != nil {
					log.Printf("Design task worker error: %v", err)
					continue
				}

				if processed {
					log.Println("Design task processed successfully")
				}
			}
		}
	}()

	handler.RegisterDesignTaskRoutes(
		router,
		taskService,
		quotaService,
		cfg.AuthTokenSecret,
	)

	// 当天剩余次数查询。
	handler.RegisterQuotaRoutes(router, quotaService, cfg.AuthTokenSecret)

	// 使用记录：免费与扣积分都会留一条。
	usageService := service.NewUsageService(usageRepo, quotaLoc)
	handler.RegisterUsageRoutes(router, usageService, cfg.AuthTokenSecret)

	// 设计效果图读取（小程序 <image> 需要用 ?token= 传令牌）。
	handler.RegisterDesignTaskFileRoutes(
		router,
		taskService,
		photoService,
		cfg.AuthTokenSecret,
	)

	// 发型推荐：按面部分析结果 + 性别 + 年龄段筛选并打分。
	hairstyleService := service.NewHairstyleService(
		faceAnalysisRepo,
		styleLibrary,
	)

	handler.RegisterHairstyleRoutes(
		router,
		hairstyleService,
		taskService,
		quotaService,
		cfg.AuthTokenSecret,
	)

	// 面部分析：任务入库后由后台 Worker 调用 Python face-server。
	faceClient := face.NewClient(
		cfg.FaceServiceURL,
		time.Duration(cfg.FaceTimeoutSeconds)*time.Second,
	)
	faceAnalysisService := service.NewFaceAnalysisService(
		faceAnalysisRepo,
		photoRepo,
		photoService,
		faceClient,
		quotaService,
	)

	handler.RegisterFaceAnalysisRoutes(
		router,
		faceAnalysisService,
		quotaService,
		cfg.AuthTokenSecret,
	)
	handler.RegisterFaceAnalysisFileRoutes(
		router,
		faceAnalysisService,
		photoService,
		photoRepo,
		cfg.AuthTokenSecret,
	)

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		log.Println("Face analysis worker started")

		for {
			select {
			case <-workerCtx.Done():
				log.Println("Face analysis worker stopped")
				return

			case <-ticker.C:
				processed, err := faceAnalysisService.ProcessNext(workerCtx)
				if err != nil {
					log.Printf("Face analysis worker error: %v", err)
					continue
				}
				if processed {
					log.Println("Face analysis task processed")
				}
			}
		}
	}()
	if cfg.Env != "production" {
		handler.RegisterDevRoutes(router, pool)
	}
	router.GET("/health", func(c *gin.Context) {
		faceStatus := "unavailable"
		if faceClient.Enabled() {
			faceStatus = "configured"
		}
		c.JSON(http.StatusOK, gin.H{
			"status":       "ok",
			"service":      "ai-style-backend",
			"face_service": faceStatus,
		})
	})

	router.GET("/ready", func(c *gin.Context) {
		pingCtx, pingCancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer pingCancel()
		if err := pool.Ping(pingCtx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status":   "not_ready",
				"database": "unavailable",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":   "ready",
			"database": "connected",
		})
	})

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("HTTP server listening on http://localhost:%s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server: %v", err)
		}
	}()

	stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-stopCtx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	log.Println("Server stopped")
}
