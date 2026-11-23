package handler

import (
	"context"
	"log"
	"net/http"
	"time"

	"ai-style-backend/internal/repository"
	"ai-style-backend/internal/response"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterDevRoutes 注册仅供开发环境使用的接口。
func RegisterDevRoutes(router gin.IRouter, db *pgxpool.Pool) {
	userRepo := repository.NewUserRepository(db)

	router.GET("/api/v1/dev/db-check", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(
			c.Request.Context(),
			3*time.Second,
		)
		defer cancel()

		exists, err := userRepo.UsersTableExists(ctx)
		if err != nil {
			log.Printf("development database check failed: %v", err)

			response.Fail(
				c,
				http.StatusServiceUnavailable,
				5001,
				"database check failed",
			)
			return
		}

		if !exists {
			response.Fail(
				c,
				http.StatusServiceUnavailable,
				5002,
				"users table does not exist",
			)
			return
		}

		response.Success(c, gin.H{
			"database_connected": true,
			"users_table_exists": true,
		})
	})
}
