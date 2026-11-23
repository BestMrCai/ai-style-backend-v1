package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ai-style-backend/internal/auth"
	"ai-style-backend/internal/repository"
	"ai-style-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type FaceAnalysisHandler struct {
	svc         *service.FaceAnalysisService
	quota       *service.QuotaService
	tokenSecret string
}

func RegisterFaceAnalysisRoutes(
	router gin.IRouter,
	svc *service.FaceAnalysisService,
	quota *service.QuotaService,
	tokenSecret string,
) {
	h := &FaceAnalysisHandler{svc: svc, quota: quota, tokenSecret: tokenSecret}

	router.POST("/api/v1/face-analyses", h.Create)
	router.GET("/api/v1/face-analyses", h.List)
	router.GET("/api/v1/face-analyses/:id", h.GetByID)
	router.DELETE("/api/v1/face-analyses/:id", h.Delete)
	router.PATCH("/api/v1/face-analyses/:id/profile", h.UpdateProfile)
}

type updateProfileRequest struct {
	Gender   string `json:"gender" binding:"required"`
	AgeGroup string `json:"age_group"`
}

// UpdateProfile 允许在分析完成后补填或修改性别与年龄段。
// 已有的报告结论不变，只影响之后的发型推荐。
func (h *FaceAnalysisHandler) UpdateProfile(c *gin.Context) {
	userID, ok := h.userID(c)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40004,
			"message": "invalid analysis id",
		})
		return
	}

	var req updateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40001,
			"message": "invalid request body",
		})
		return
	}

	err = h.svc.UpdateProfile(
		c.Request.Context(),
		userID,
		id,
		strings.TrimSpace(req.Gender),
		strings.TrimSpace(req.AgeGroup),
	)
	if err != nil {
		if errors.Is(err, service.ErrInvalidFaceAnalysis) {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    40002,
				"message": "invalid gender or age_group",
			})
			return
		}
		if errors.Is(err, repository.ErrFaceAnalysisNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code":    40401,
				"message": "face analysis not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50005,
			"message": "failed to update profile",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"analysis_id": id,
			"gender":      req.Gender,
			"age_group":   req.AgeGroup,
		},
	})
}

func (h *FaceAnalysisHandler) userID(c *gin.Context) (int64, bool) {
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

type createFaceAnalysisRequest struct {
	PhotoID int64 `json:"photo_id" binding:"required,gt=0"`
	// Gender 必填：male / female。性别属于「这一次分析的对象」，
	// 用户可能上传家人朋友的照片，因此不能从账号资料推导。
	Gender string `json:"gender" binding:"required"`
	// AgeGroup 选填，空表示用户选择暂不填写。
	AgeGroup string `json:"age_group"`
}

func (h *FaceAnalysisHandler) Create(c *gin.Context) {
	userID, ok := h.userID(c)
	if !ok {
		return
	}

	var req createFaceAnalysisRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40001,
			"message": "invalid request body",
		})
		return
	}

	id, err := h.svc.Create(
		c.Request.Context(),
		userID,
		req.PhotoID,
		strings.TrimSpace(req.Gender),
		strings.TrimSpace(req.AgeGroup),
	)
	if err != nil {
		// 免费次数用完了，但还能用积分继续：402 让前端区分"可以花钱"与"完全没戏"。
		if errors.Is(err, service.ErrInsufficientCredits) {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"code":    40201,
				"message": insufficientCreditsMessage(h.quota, true),
			})
			return
		}
		// 超额且该功能不收费：返回 429 + 可直接展示的文案。
		if errors.Is(err, service.ErrDailyQuotaExceeded) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    42901,
				"message": dailyQuotaMessage(h.quota, true),
			})
			return
		}
		if errors.Is(err, service.ErrInvalidFaceAnalysis) {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    40002,
				"message": "invalid gender or age_group",
			})
			return
		}
		if errors.Is(err, repository.ErrPhotoUnavailable) {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    40003,
				"message": "photo unavailable or not authorized",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50001,
			"message": "failed to create face analysis",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"analysis_id": id,
			"status":      "queued",
			"photo_id":    req.PhotoID,
			"gender":      req.Gender,
			"age_group":   req.AgeGroup,
		},
	})
}

type faceAnalysisItem struct {
	ID           int64           `json:"analysis_id"`
	PhotoID      int64           `json:"photo_id"`
	Status       string          `json:"status"`
	Gender       *string         `json:"gender,omitempty"`
	AgeGroup     *string         `json:"age_group,omitempty"`
	ErrorCode    *string         `json:"error_code,omitempty"`
	ErrorMessage *string         `json:"error_message,omitempty"`
	CreatedAt    string          `json:"created_at"`
	StartedAt    *string         `json:"started_at"`
	FinishedAt   *string         `json:"finished_at"`
	Result       json.RawMessage `json:"result,omitempty"`
	OverlayURL   string          `json:"overlay_url,omitempty"`
}

func formatTime(t time.Time) string { return t.Format(time.RFC3339) }

func optionalTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := t.Format(time.RFC3339)
	return &v
}

func toItem(f *repository.FaceAnalysis, includeResult bool) faceAnalysisItem {
	item := faceAnalysisItem{
		ID:           f.ID,
		PhotoID:      f.PhotoID,
		Status:       f.Status,
		Gender:       f.Gender,
		AgeGroup:     f.AgeGroup,
		ErrorCode:    f.ErrorCode,
		ErrorMessage: f.ErrorMessage,
		CreatedAt:    formatTime(f.CreatedAt),
		StartedAt:    optionalTime(f.StartedAt),
		FinishedAt:   optionalTime(f.FinishedAt),
	}

	if f.OverlayKey != nil {
		item.OverlayURL = "/api/v1/face-analyses/" +
			strconv.FormatInt(f.ID, 10) + "/overlay"
	}

	if includeResult && len(f.ResultJSON) > 0 {
		item.Result = json.RawMessage(f.ResultJSON)
	}

	return item
}

func (h *FaceAnalysisHandler) GetByID(c *gin.Context) {
	userID, ok := h.userID(c)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40004,
			"message": "invalid analysis id",
		})
		return
	}

	item, err := h.svc.GetByID(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, repository.ErrFaceAnalysisNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code":    40401,
				"message": "face analysis not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50002,
			"message": "failed to query face analysis",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    toItem(item, item.Status == "succeeded"),
	})
}

func (h *FaceAnalysisHandler) List(c *gin.Context) {
	userID, ok := h.userID(c)
	if !ok {
		return
	}

	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}

	items, err := h.svc.List(c.Request.Context(), userID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50003,
			"message": "failed to list face analyses",
		})
		return
	}

	data := make([]faceAnalysisItem, 0, len(items))
	for i := range items {
		// 列表不返回完整报告，避免响应过大。
		data = append(data, toItem(&items[i], false))
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"items": data,
			"total": len(data),
		},
	})
}

// Delete 删除当前用户的一条分析记录。
func (h *FaceAnalysisHandler) Delete(c *gin.Context) {
	userID, ok := h.userID(c)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40004,
			"message": "invalid analysis id",
		})
		return
	}

	if err := h.svc.Delete(c.Request.Context(), userID, id); err != nil {
		if errors.Is(err, repository.ErrFaceAnalysisNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code":    40401,
				"message": "face analysis not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50004,
			"message": "failed to delete face analysis",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    gin.H{"analysis_id": id},
	})
}
