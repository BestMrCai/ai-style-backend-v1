package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"ai-style-backend/internal/auth"
	"ai-style-backend/internal/repository"
	"ai-style-backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterFaceAnalysisFileRoutes 提供标注图与原始照片读取。
func RegisterFaceAnalysisFileRoutes(
	router gin.IRouter,
	svc *service.FaceAnalysisService,
	photoSvc *service.PhotoService,
	photoRepo *repository.PhotoRepository,
	tokenSecret string,
) {
	router.GET("/api/v1/face-analyses/:id/overlay", func(c *gin.Context) {
		userID, ok := verifyBearerOrQuery(c, tokenSecret)
		if !ok {
			return
		}

		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 40004, "message": "invalid analysis id"})
			return
		}

		item, err := svc.GetByID(c.Request.Context(), userID, id)
		if err != nil {
			if errors.Is(err, repository.ErrFaceAnalysisNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"code": 40401, "message": "face analysis not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"code": 50002, "message": "failed to query face analysis"})
			return
		}

		if item.OverlayKey == nil || *item.OverlayKey == "" {
			c.JSON(http.StatusNotFound, gin.H{"code": 40402, "message": "overlay not found"})
			return
		}

		data, err := photoSvc.Read(c.Request.Context(), *item.OverlayKey)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"code": 40402, "message": "overlay not found"})
			return
		}

		serveImage(c, data)
	})

	router.GET("/api/v1/face-analyses/:id/source", func(c *gin.Context) {
		userID, ok := verifyBearerOrQuery(c, tokenSecret)
		if !ok {
			return
		}

		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 40004, "message": "invalid analysis id"})
			return
		}

		item, err := svc.GetByID(c.Request.Context(), userID, id)
		if err != nil {
			if errors.Is(err, repository.ErrFaceAnalysisNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"code": 40401, "message": "face analysis not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"code": 50002, "message": "failed to query face analysis"})
			return
		}

		photo, err := photoRepo.FindByIDForUser(c.Request.Context(), userID, item.PhotoID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"code": 40403, "message": "photo not found"})
			return
		}

		data, err := photoSvc.Read(c.Request.Context(), photo.StorageKey)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"code": 40403, "message": "photo not found"})
			return
		}

		serveImage(c, data)
	})
}

// serveImage 按文件头返回正确的 Content-Type，避免把 PNG 当 JPEG 返回。
func serveImage(c *gin.Context, data []byte) {
	contentType := http.DetectContentType(data)
	if !strings.HasPrefix(contentType, "image/") {
		contentType = "application/octet-stream"
	}

	c.Header("Cache-Control", "private, max-age=86400")
	c.Data(http.StatusOK, contentType, data)
}

// verifyBearerOrQuery 同时支持 Authorization 头与 ?token=。
// 小程序 <image> 组件无法自定义请求头，只能通过 query 传递令牌。
func verifyBearerOrQuery(c *gin.Context, tokenSecret string) (int64, bool) {
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	parts := strings.Fields(authHeader)

	token := ""
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		token = parts[1]
	} else {
		token = strings.TrimSpace(c.Query("token"))
	}

	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 40101, "message": "missing or invalid authorization token"})
		return 0, false
	}

	userID, err := auth.Verify(token, tokenSecret)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 40101, "message": "invalid or expired token"})
		return 0, false
	}
	return userID, true
}
