package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"ai-style-backend/internal/repository"
	"ai-style-backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterDesignTaskFileRoutes 提供设计效果图读取。
//
// 与面部分析的标注图一致：<image> 组件无法自定义请求头，
// 因此同时支持 Authorization 与 ?token=。
func RegisterDesignTaskFileRoutes(
	router gin.IRouter,
	taskService *service.DesignTaskService,
	photoSvc *service.PhotoService,
	tokenSecret string,
) {
	router.GET("/api/v1/design-tasks/:id/image", func(c *gin.Context) {
		userID, ok := verifyBearerOrQuery(c, tokenSecret)
		if !ok {
			return
		}

		taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || taskID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 40004, "message": "invalid task id"})
			return
		}

		task, err := taskService.GetByID(c.Request.Context(), userID, taskID)
		if err != nil {
			if errors.Is(err, repository.ErrDesignTaskNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"code": 40401, "message": "design task not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"code": 50002, "message": "failed to query design task"})
			return
		}

		// 只取第一条带图的记录：一期每个任务只生成一张。
		var storageKey string
		for _, result := range task.Results {
			if result.StorageKey != nil && strings.TrimSpace(*result.StorageKey) != "" {
				storageKey = *result.StorageKey
				break
			}
		}

		if storageKey == "" {
			c.JSON(http.StatusNotFound, gin.H{"code": 40404, "message": "result image not found"})
			return
		}

		data, err := photoSvc.Read(c.Request.Context(), storageKey)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"code": 40404, "message": "result image not found"})
			return
		}

		serveImage(c, data)
	})
}
