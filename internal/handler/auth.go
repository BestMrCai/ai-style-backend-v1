package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"ai-style-backend/internal/auth"
	"ai-style-backend/internal/response"
	"ai-style-backend/internal/service"

	"github.com/gin-gonic/gin"
)

const loginTokenTTL = 24 * time.Hour

// authHandler 负责处理登录 HTTP 请求。
type authHandler struct {
	loginService *service.LoginService
	tokenSecret  string
}

// RegisterAuthRoutes 注册认证相关路由。
func RegisterAuthRoutes(
	router gin.IRouter,
	loginService *service.LoginService,
	tokenSecret string,
) {
	h := &authHandler{
		loginService: loginService,
		tokenSecret:  tokenSecret,
	}

	router.POST("/api/v1/auth/wechat-login", h.wechatLogin)
}

// wechatLogin 处理微信小程序登录请求。
func (h *authHandler) wechatLogin(c *gin.Context) {
	// 1. 检查后端配置。
	if h.loginService == nil || len(h.tokenSecret) < 32 {
		response.Fail(
			c,
			http.StatusServiceUnavailable,
			50301,
			"登录服务暂不可用",
		)
		return
	}

	// 2. 解析请求参数。
	var req struct {
		Code string `json:"code" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(
			c,
			http.StatusBadRequest,
			40001,
			"请求参数无效，请提供微信登录 code",
		)
		return
	}

	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" || len(req.Code) > 512 {
		response.Fail(
			c,
			http.StatusBadRequest,
			40001,
			"微信登录 code 无效",
		)
		return
	}

	// 3. 调用微信登录服务，并限制请求最长执行时间。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()

	user, err := h.loginService.LoginByCode(ctx, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrWeChatConfigMissing):
			response.Fail(
				c,
				http.StatusServiceUnavailable,
				50301,
				"微信登录服务暂不可用",
			)

		case errors.Is(err, service.ErrLoginCodeEmpty):
			response.Fail(
				c,
				http.StatusBadRequest,
				40001,
				"微信登录 code 无效",
			)

		case errors.Is(err, service.ErrWeChatAPI):
			response.Fail(
				c,
				http.StatusBadGateway,
				50201,
				"微信登录失败，请重新尝试",
			)

		default:
			// 详细错误只记录在后端日志，不返回给客户端。
			log.Printf("wechat login failed: %v", err)
			response.Fail(
				c,
				http.StatusInternalServerError,
				50001,
				"登录失败，请稍后重试",
			)
		}
		return
	}

	// 4. 为本系统用户签发 JWT。
	token, err := auth.Sign(user.ID, h.tokenSecret, loginTokenTTL)
	if err != nil {
		log.Printf("sign login token failed: %v", err)
		response.Fail(
			c,
			http.StatusServiceUnavailable,
			50302,
			"登录服务暂不可用",
		)
		return
	}

	// 5. 只返回必要的用户资料，不暴露 OpenID 等内部信息。
	data := gin.H{
		"token":      token,
		"expires_in": int64(loginTokenTTL.Seconds()),
		"user": gin.H{
			"id":         user.ID,
			"nickname":   user.Nickname,
			"avatar_url": user.AvatarURL,
			"status":     user.Status,
		},
	}

	response.Success(c, data)
}
