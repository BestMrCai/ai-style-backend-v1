package handler

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ai-style-backend/internal/auth"
	"ai-style-backend/internal/repository"
	"ai-style-backend/internal/service"

	"github.com/gin-gonic/gin"
)

// HairstyleHandler 提供发型推荐接口。
type HairstyleHandler struct {
	svc         *service.HairstyleService
	taskService *service.DesignTaskService
	quota       *service.QuotaService
	tokenSecret string
}

func RegisterHairstyleRoutes(
	router gin.IRouter,
	svc *service.HairstyleService,
	taskService *service.DesignTaskService,
	quota *service.QuotaService,
	tokenSecret string,
) {
	h := &HairstyleHandler{
		svc:         svc,
		taskService: taskService,
		quota:       quota,
		tokenSecret: tokenSecret,
	}

	router.GET("/api/v1/hairstyles/recommendations", h.Recommend)
	router.POST("/api/v1/hairstyles/tasks", h.CreateTask)

	// 封面图不需要鉴权：这是素材库的公开示意图，不含用户数据。
	// 封面按 style_id 查找，同时兼容用中文发型名作为文件名。

	router.GET("/api/v1/hairstyles/:id/cover", func(c *gin.Context) {
		fmt.Println("serveHairstyleCover")
		serveHairstyleCover(c, svc)
	})
}

// coverSearchDirs 是封面图的查找顺序，先找到先用。
//
// uploads/rec 放在第一位：运营把图片直接丢进这个目录就能生效，
// 不需要重新编译，也不需要改 data/ 下的素材文件。
// data/hairstyles/covers 作为兜底，保证没有上传素材时列表也不会空白。
var coverSearchDirs = []string{
	filepath.Join("data", "hairstyles", "covers"),
	filepath.Join("uploads", "rec"),
}

// coverExtensions 兼容常见后缀，运营不用为了适配而改名转格式。
var coverExtensions = []string{".jpg", ".jpeg", ".png", ".webp"}

// serveHairstyleCover 按 style_id 查找封面并返回。
//
// 除 style_id 外，也接受该款发型的中文名作为文件名：
// 运营常常按"空气刘海短发.png"这样保存，强制改名容易出错。
// 两种命名同时支持，任何一处命中即可。
func serveHairstyleCover(c *gin.Context, svc *service.HairstyleService) {
	// filepath.Base 挡住 ../ 之类的路径穿越。
	id := filepath.Base(c.Param("id"))
	if id == "" || id == "." || id == ".." {
		c.Status(http.StatusNotFound)
		return
	}

	// 候选文件名：先 style_id，再中文名。
	names := []string{id}
	fmt.Println(names)
	if svc != nil {
		if name := svc.StyleName(id); name != "" && name != id {
			names = append(names, name)
		}
	}
	fmt.Println(names)

	// 目录优先于文件名：uploads/rec 里任何命名方式都应压过
	// data/hairstyles/covers 里的占位图，否则运营放的新图永远不生效。
	for _, dir := range coverSearchDirs {
		for _, name := range names {
			for _, ext := range coverExtensions {
				path := filepath.Join(dir, name+ext)
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}

				contentType := http.DetectContentType(data)
				if !strings.HasPrefix(contentType, "image/") {
					continue
				}

				c.Header("Cache-Control", "public, max-age=86400")
				c.Data(http.StatusOK, contentType, data)
				return
			}
		}
	}

	c.Status(http.StatusNotFound)
}

func (h *HairstyleHandler) userID(c *gin.Context) (int64, bool) {
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	parts := strings.Fields(authHeader)

	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    40101,
			"message": "missing or invalid authorization token",
		})
		return 0, false
	}

	userID, err := auth.Verify(parts[1], h.tokenSecret)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    40101,
			"message": "invalid or expired token",
		})
		return 0, false
	}
	return userID, true
}

// Recommend 根据某次面部分析返回发型推荐列表。
func (h *HairstyleHandler) Recommend(c *gin.Context) {
	userID, ok := h.userID(c)
	if !ok {
		return
	}

	analysisID, err := strconv.ParseInt(c.Query("analysis_id"), 10, 64)
	if err != nil || analysisID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40004,
			"message": "invalid analysis id",
		})
		return
	}

	limit := 5
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, convErr := strconv.Atoi(raw); convErr == nil {
			limit = parsed
		}
	}
	if limit <= 0 || limit > 10 {
		limit = 5
	}

	items, candidate, err := h.svc.Recommend(c.Request.Context(), userID, analysisID, limit)
	if err != nil {
		if errors.Is(err, repository.ErrFaceAnalysisNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code":    40401,
				"message": "face analysis not found",
			})
			return
		}
		if errors.Is(err, service.ErrAnalysisNotReady) {
			// 分析未完成或缺少性别：前端据此引导补选，而不是拿到空列表。
			c.JSON(http.StatusConflict, gin.H{
				"code":    40901,
				"message": "analysis is not ready or gender is missing",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50006,
			"message": "failed to build recommendations",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"summary": service.Describe(*candidate),
			"items":   items,
			"total":   len(items),
		},
	})
}

type createHairstyleTaskRequest struct {
	AnalysisID int64  `json:"analysis_id" binding:"required,gt=0"`
	StyleID    string `json:"style_id" binding:"required"`
	// DesignMode 本期只接受 styleset（从推荐列表选择）。
	// 后续开放自定义上传时改为 reference 并附带参考图，接口不变。
	DesignMode string `json:"design_mode"`
}

// CreateTask 创建一次发型合成任务。
// 复用 design_tasks 与后台 Worker：创建后立即返回，
// 小程序可返回首页，任务在后台继续。
func (h *HairstyleHandler) CreateTask(c *gin.Context) {
	userID, ok := h.userID(c)
	if !ok {
		return
	}

	var req createHairstyleTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40001,
			"message": "invalid request body",
		})
		return
	}

	mode := strings.TrimSpace(req.DesignMode)
	if mode == "" {
		mode = "styleset"
	}

	taskID, err := h.taskService.CreateFromHairstyle(
		c.Request.Context(),
		userID,
		req.AnalysisID,
		strings.TrimSpace(req.StyleID),
		mode,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInsufficientCredits):
			c.JSON(http.StatusPaymentRequired, gin.H{
				"code":    40202,
				"message": insufficientCreditsMessage(h.quota, false),
			})
		case errors.Is(err, service.ErrDailyQuotaExceeded):
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    42902,
				"message": dailyQuotaMessage(h.quota, false),
			})
		case errors.Is(err, service.ErrInvalidDesignTask):
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    40002,
				"message": "invalid hairstyle task parameters",
			})
		case errors.Is(err, repository.ErrDesignTaskNotFound),
			errors.Is(err, repository.ErrPhotoUnavailable):
			c.JSON(http.StatusNotFound, gin.H{
				"code":    40401,
				"message": "analysis or photo not found",
			})
		case errors.Is(err, service.ErrAnalysisNotReady):
			c.JSON(http.StatusConflict, gin.H{
				"code":    40901,
				"message": "analysis is not ready",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":    50007,
				"message": "failed to create hairstyle task",
			})
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"task_id":  taskID,
			"status":   "queued",
			"style_id": req.StyleID,
		},
	})
}
