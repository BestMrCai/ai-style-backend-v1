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

type DesignTaskHandler struct {
	taskService *service.DesignTaskService
	quota       *service.QuotaService
	tokenSecret string
}

func RegisterDesignTaskRoutes(
	router gin.IRouter,
	taskService *service.DesignTaskService,
	quota *service.QuotaService,
	tokenSecret string,
) {
	h := &DesignTaskHandler{
		taskService: taskService,
		quota:       quota,
		tokenSecret: tokenSecret,
	}

	router.POST("/api/v1/design-tasks", h.Create)
	router.GET("/api/v1/design-tasks", h.List)
	router.GET("/api/v1/design-tasks/:id", h.GetByID)
	router.DELETE("/api/v1/design-tasks/:id", h.Delete)
}

type createDesignTaskRequest struct {
	PhotoID           int64   `json:"photo_id" binding:"required,gt=0"`
	DesignType        string  `json:"design_type" binding:"required"`
	StyleCode         string  `json:"style_code"`
	DesignMode        string  `json:"design_mode"`
	UserRequirement   string  `json:"user_requirement"`
	ReferencePhotoIDs []int64 `json:"reference_photo_ids"`
}

func (h *DesignTaskHandler) Create(c *gin.Context) {
	// 1. 从 Authorization 请求头中读取 Bearer Token。
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	parts := strings.Fields(authHeader)

	if len(parts) != 2 ||
		!strings.EqualFold(parts[0], "Bearer") {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    40101,
			"message": "missing or invalid authorization token",
		})
		return
	}

	// 2. 验证 Token，获取当前用户 ID。
	userID, err := auth.Verify(parts[1], h.tokenSecret)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    40101,
			"message": "invalid or expired token",
		})
		return
	}

	// 3. 解析请求 JSON。
	var req createDesignTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40001,
			"message": "invalid request body",
		})
		return
	}

	// 4. 兼容旧版本小程序。
	// 如果没有传 design_mode，则默认使用自动设计模式。
	if strings.TrimSpace(req.DesignMode) == "" {
		req.DesignMode = "auto"
	}

	// 5. 创建任务。
	taskID, err := h.taskService.CreateWithOptions(
		c.Request.Context(),
		userID,
		req.PhotoID,
		req.DesignType,
		req.StyleCode,
		req.DesignMode,
		req.UserRequirement,
		req.ReferencePhotoIDs,
	)
	if err != nil {
		// 这条是老接口，同样会触发出图，计费口径与 /hairstyles/tasks 一致。
		if errors.Is(err, service.ErrInsufficientCredits) {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"code":    40202,
				"message": insufficientCreditsMessage(h.quota, false),
			})
			return
		}
		if errors.Is(err, service.ErrDailyQuotaExceeded) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    42902,
				"message": dailyQuotaMessage(h.quota, false),
			})
			return
		}

		if errors.Is(err, service.ErrInvalidDesignTask) ||
			errors.Is(err, repository.ErrInvalidDesignPhotos) {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    40002,
				"message": "invalid design task parameters",
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
			"message": "failed to create design task",
		})
		return
	}

	// 6. 返回新任务信息。
	c.JSON(http.StatusCreated, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"task_id":             taskID,
			"status":              "queued",
			"design_type":         req.DesignType,
			"design_mode":         req.DesignMode,
			"user_requirement":    req.UserRequirement,
			"reference_photo_ids": req.ReferencePhotoIDs,
		},
	})
}

type designTaskResponse struct {
	TaskID            int64                  `json:"task_id"`
	PhotoID           int64                  `json:"photo_id"`
	AnalysisID        *int64                 `json:"analysis_id,omitempty"`
	StyleID           *string                `json:"style_id,omitempty"`
	Gender            *string                `json:"gender,omitempty"`
	AgeGroup          *string                `json:"age_group,omitempty"`
	DesignType        string                 `json:"design_type"`
	StyleCode         *string                `json:"style_code"`
	DesignMode        string                 `json:"design_mode"`
	UserRequirement   *string                `json:"user_requirement"`
	Status            string                 `json:"status"`
	ErrorCode         *string                `json:"error_code,omitempty"`
	ErrorMessage      *string                `json:"error_message,omitempty"`
	CreatedAt         string                 `json:"created_at"`
	StartedAt         *string                `json:"started_at"`
	FinishedAt        *string                `json:"finished_at"`
	Results           []designResultResponse `json:"results"`
	ReferencePhotoIDs []int64                `json:"reference_photo_ids"`
}

type designResultResponse struct {
	ID         int64   `json:"id"`
	ResultType string  `json:"result_type"`
	StorageKey *string `json:"storage_key"`
	Suggestion any     `json:"suggestion"`
	CreatedAt  string  `json:"created_at"`
}

type designTaskSummaryResponse struct {
	TaskID       int64   `json:"task_id"`
	AnalysisID   *int64  `json:"analysis_id,omitempty"`
	StyleID      *string `json:"style_id,omitempty"`
	StyleName    string  `json:"style_name"`
	Status       string  `json:"status"`
	HasImage     bool    `json:"has_image"`
	ErrorMessage *string `json:"error_message,omitempty"`
	CreatedAt    string  `json:"created_at"`
	FinishedAt   *string `json:"finished_at"`
}

// List 返回当前用户的发型合成记录，用于「我的 → 我的发型」。
func (h *DesignTaskHandler) List(c *gin.Context) {
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

	items, err := h.taskService.ListForUser(c.Request.Context(), userID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50002,
			"message": "failed to list design tasks",
		})
		return
	}

	data := make([]designTaskSummaryResponse, 0, len(items))
	for _, item := range items {
		var finishedAt *string
		if item.FinishedAt != nil {
			value := item.FinishedAt.Format(time.RFC3339)
			finishedAt = &value
		}

		data = append(data, designTaskSummaryResponse{
			TaskID:       item.ID,
			AnalysisID:   item.AnalysisID,
			StyleID:      item.StyleID,
			StyleName:    item.StyleName,
			Status:       item.Status,
			HasImage:     item.HasImage,
			ErrorMessage: item.ErrorMessage,
			CreatedAt:    item.CreatedAt.Format(time.RFC3339),
			FinishedAt:   finishedAt,
		})
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

// Delete 删除当前用户的一条发型合成记录。
func (h *DesignTaskHandler) Delete(c *gin.Context) {
	userID, ok := h.userID(c)
	if !ok {
		return
	}

	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || taskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40004,
			"message": "invalid task id",
		})
		return
	}

	if err := h.taskService.Delete(c.Request.Context(), userID, taskID); err != nil {
		if errors.Is(err, repository.ErrDesignTaskNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code":    40401,
				"message": "design task not found",
			})
			return
		}

		if errors.Is(err, service.ErrInvalidDesignTask) {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    40002,
				"message": "invalid design task parameters",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50005,
			"message": "failed to delete design task",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    gin.H{"task_id": taskID},
	})
}

// userID 从 Bearer Token 解析当前用户。
func (h *DesignTaskHandler) userID(c *gin.Context) (int64, bool) {
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

// GetByID 查询当前用户自己的设计任务。
func (h *DesignTaskHandler) GetByID(c *gin.Context) {
	// 1. 验证 Token。
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	parts := strings.Fields(authHeader)

	if len(parts) != 2 ||
		!strings.EqualFold(parts[0], "Bearer") {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    40101,
			"message": "missing or invalid authorization token",
		})
		return
	}

	userID, err := auth.Verify(parts[1], h.tokenSecret)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    40101,
			"message": "invalid or expired token",
		})
		return
	}

	// 2. 解析任务 ID。
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || taskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40004,
			"message": "invalid task id",
		})
		return
	}

	// 3. 查询任务。
	task, err := h.taskService.GetByID(
		c.Request.Context(),
		userID,
		taskID,
	)
	if err != nil {
		if errors.Is(err, repository.ErrDesignTaskNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code":    40401,
				"message": "design task not found",
			})
			return
		}

		if errors.Is(err, service.ErrInvalidDesignTask) {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    40002,
				"message": "invalid design task parameters",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50002,
			"message": "failed to query design task",
		})
		return
	}

	// 4. 转换设计结果。
	results := make([]designResultResponse, 0, len(task.Results))

	for _, result := range task.Results {
		var suggestion any

		if len(result.Suggestion) > 0 {
			if err := json.Unmarshal(result.Suggestion, &suggestion); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"code":    50003,
					"message": "failed to parse design result",
				})
				return
			}
		}

		results = append(results, designResultResponse{
			ID:         result.ID,
			ResultType: result.ResultType,
			StorageKey: result.StorageKey,
			Suggestion: suggestion,
			CreatedAt:  result.CreatedAt.Format(time.RFC3339),
		})
	}

	// 5. 转换时间字段。
	var startedAt *string
	if task.StartedAt != nil {
		value := task.StartedAt.Format(time.RFC3339)
		startedAt = &value
	}

	var finishedAt *string
	if task.FinishedAt != nil {
		value := task.FinishedAt.Format(time.RFC3339)
		finishedAt = &value
	}

	// 6. 返回任务信息。
	response := designTaskResponse{
		TaskID:            task.ID,
		PhotoID:           task.PhotoID,
		AnalysisID:        task.AnalysisID,
		StyleID:           task.StyleID,
		Gender:            task.Gender,
		AgeGroup:          task.AgeGroup,
		DesignType:        task.DesignType,
		StyleCode:         task.StyleCode,
		DesignMode:        task.DesignMode,
		UserRequirement:   task.UserRequirement,
		Status:            task.Status,
		ErrorCode:         task.ErrorCode,
		ErrorMessage:      task.ErrorMessage,
		CreatedAt:         task.CreatedAt.Format(time.RFC3339),
		StartedAt:         startedAt,
		FinishedAt:        finishedAt,
		Results:           results,
		ReferencePhotoIDs: task.ReferencePhotoIDs,
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    response,
	})
}
