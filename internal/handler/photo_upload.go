package handler

import (
	"ai-style-backend/internal/auth"
	"ai-style-backend/internal/repository"
	"ai-style-backend/internal/service"
	"ai-style-backend/internal/storage"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// PhotoUploadHandler 处理用户照片上传。
type PhotoUploadHandler struct {
	photoService *service.PhotoService
	photoRepo    *repository.PhotoRepository
	tokenSecret  string
}

// RegisterPhotoRoutes 注册照片上传路由。
func RegisterPhotoRoutes(
	router *gin.Engine,
	photoService *service.PhotoService,
	photoRepo *repository.PhotoRepository,
	tokenSecret string,
) {
	h := &PhotoUploadHandler{
		photoService: photoService,
		photoRepo:    photoRepo,
		tokenSecret:  tokenSecret,
	}

	router.POST("/api/v1/photos", h.Upload)
}

// Upload 接收图片，保存文件，并记录数据库元数据。
func (h *PhotoUploadHandler) Upload(c *gin.Context) {
	// 1. 验证 JWT，确定当前登录用户。
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

	// 2. 限制整个 HTTP 请求体大小。
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		service.MaxPhotoSize+1024*1024,
	)

	// 3. 解析 multipart/form-data。
	if err := c.Request.ParseMultipartForm(1024 * 1024); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40001,
			"message": "invalid multipart upload or request too large",
		})
		return
	}

	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}

	// 4. 必须明确同意照片用于 AI 形象设计。
	consent := strings.TrimSpace(c.Request.FormValue("consent"))
	if consent != "true" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40002,
			"message": "explicit photo processing consent is required",
		})
		return
	}

	// 5. 获取照片类型。
	// 兼容旧版小程序：未传 photo_type 时默认为 portrait。
	photoType := strings.TrimSpace(c.Request.FormValue("photo_type"))
	if photoType == "" {
		photoType = repository.PhotoTypePortrait
	}

	if photoType != repository.PhotoTypePortrait &&
		photoType != repository.PhotoTypeReference {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40006,
			"message": "photo_type must be portrait or reference",
		})
		return
	}

	// 6. 获取图片文件。
	file, header, err := c.Request.FormFile("photo")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40003,
			"message": "photo file is required",
		})
		return
	}
	defer file.Close()

	if header.Size > service.MaxPhotoSize {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40004,
			"message": "photo file is too large",
		})
		return
	}

	// 7. 保存图片文件。
	storageKey, fileSize, err := h.photoService.Save(
		c.Request.Context(),
		file,
		storage.DirUserPhoto,
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    40005,
			"message": err.Error(),
		})
		return
	}

	// 8. 保存数据库记录。
	photoID, err := h.photoRepo.Create(
		c.Request.Context(),
		userID,
		storageKey,
		photoType,
		"granted",
	)
	if err != nil {
		if deleteErr := h.photoService.Delete(c.Request.Context(), storageKey); deleteErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code":    50002,
				"message": "failed to save photo metadata and clean up file",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50001,
			"message": "failed to save photo metadata",
		})
		return
	}

	// 9. 返回结果。
	c.JSON(http.StatusCreated, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"photo_id":    photoID,
			"photo_type":  photoType,
			"storage_key": storageKey,
			"file_size":   fileSize,
		},
	})
}
