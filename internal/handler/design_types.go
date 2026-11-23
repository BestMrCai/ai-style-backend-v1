package handler

import (
	"ai-style-backend/internal/response"

	"github.com/gin-gonic/gin"
)

// DesignType 表示一种 AI 形象设计服务类型。
type DesignType struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// GetDesignTypes 返回目前支持的设计类型。
func GetDesignTypes(c *gin.Context) {
	designTypes := []DesignType{
		{
			Code:        "hairstyle",
			Name:        "AI 发型设计",
			Description: "根据用户照片和需求，提供发型设计建议。",
		},
		{
			Code:        "outfit",
			Name:        "AI 穿搭设计",
			Description: "根据用户需求，提供服装搭配建议。",
		},
		{
			Code:        "overall",
			Name:        "整体形象设计",
			Description: "综合考虑发型、穿搭和整体风格。",
		},
	}

	response.Success(c, designTypes)
}

// RegisterDesignTypeRoutes 注册设计类型相关路由。
func RegisterDesignTypeRoutes(router gin.IRouter) {
	router.GET("/api/v1/design-types", GetDesignTypes)
}
