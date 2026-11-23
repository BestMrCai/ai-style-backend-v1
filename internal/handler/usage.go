package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"ai-style-backend/internal/service"

	"github.com/gin-gonic/gin"
)

// UsageHandler 提供使用记录查询。
//
// 免费与扣积分都走这里，前端只需要按 charge_type 决定标签文案。
type UsageHandler struct {
	svc         *service.UsageService
	tokenSecret string
}

func RegisterUsageRoutes(
	router gin.IRouter,
	svc *service.UsageService,
	tokenSecret string,
) {
	h := &UsageHandler{svc: svc, tokenSecret: tokenSecret}
	router.GET("/api/v1/usage-records", h.List)
}

// List 支持按日期区间查询：
//
//	GET /api/v1/usage-records?from=2026-11-01&to=2026-11-19&limit=20&offset=0
//
// 日期为闭区间，按东八区自然日解释；不传表示不限。
func (h *UsageHandler) List(c *gin.Context) {
	userID, ok := quotaUserID(c, h.tokenSecret)
	if !ok {
		return
	}

	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}

	offset := 0
	if raw := strings.TrimSpace(c.Query("offset")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			offset = parsed
		}
	}

	result, err := h.svc.List(
		c.Request.Context(),
		userID,
		strings.TrimSpace(c.Query("from")),
		strings.TrimSpace(c.Query("to")),
		limit,
		offset,
	)
	if err != nil {
		if errors.Is(err, service.ErrInvalidUsageQuery) {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    40005,
				"message": "invalid date range, expected YYYY-MM-DD",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50008,
			"message": "failed to list usage records",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    result,
	})
}
