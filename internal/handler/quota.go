package handler

import (
	"net/http"
	"strings"

	"ai-style-backend/internal/auth"
	"ai-style-backend/internal/service"

	"github.com/gin-gonic/gin"
)

// dailyQuotaMessage 生成"免费额度用完了"的提示。
//
// 不暴露内部字段名，也不说"错误 429"：用户只关心
// 还能不能用、要花多少。
func dailyQuotaMessage(quota *service.QuotaService, faceAnalysis bool) string {
	subject := "AI 发型生成"
	cost := 0
	limit := 0
	if faceAnalysis {
		subject = "脸型分析"
		if quota != nil {
			limit = quota.FaceAnalysisLimit()
			cost = quota.FaceAnalysisCost()
		}
	} else if quota != nil {
		limit = quota.HairstyleLimit()
		cost = quota.HairstyleCost()
	}

	// 有单价的走积分引导，没配单价的退回原来的"明天再来"。
	if cost > 0 {
		if limit > 0 {
			return "今天的" + subject + "免费次数已用完（每天" +
				itoa(limit) + "次），继续使用需要" + itoa(cost) + "积分/次"
		}
		return "继续使用" + subject + "需要" + itoa(cost) + "积分/次"
	}

	if limit > 0 {
		return "今天的" + subject + "次数已用完（每天" +
			itoa(limit) + "次），明天再来试试吧"
	}
	return "今天的" + subject + "次数已用完，明天再来试试吧"
}

// insufficientCreditsMessage 是"免费次数用完 + 积分也不够"的提示。
func insufficientCreditsMessage(quota *service.QuotaService, faceAnalysis bool) string {
	subject := "AI 发型生成"
	cost := 0
	if faceAnalysis {
		subject = "脸型分析"
		if quota != nil {
			cost = quota.FaceAnalysisCost()
		}
	} else if quota != nil {
		cost = quota.HairstyleCost()
	}

	if cost > 0 {
		return "免费次数已用完，积分不足（" + subject + "需要" +
			itoa(cost) + "积分），去积分中心看看吧"
	}
	return "免费次数已用完，积分不足"
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// QuotaHandler 暴露当天的剩余次数，供小程序展示。
//
// 单独做一个只读接口，而不是塞进每个列表响应：
// 前端在分析页、推荐页都需要这个数字，独立接口更好缓存也更好排查。
type QuotaHandler struct {
	quota       *service.QuotaService
	tokenSecret string
}

func RegisterQuotaRoutes(
	router gin.IRouter,
	quota *service.QuotaService,
	tokenSecret string,
) {
	h := &QuotaHandler{quota: quota, tokenSecret: tokenSecret}
	router.GET("/api/v1/quota", h.Get)
}

func (h *QuotaHandler) Get(c *gin.Context) {
	userID, ok := quotaUserID(c, h.tokenSecret)
	if !ok {
		return
	}

	snap, err := h.quota.Remaining(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50006,
			"message": "failed to query quota",
		})
		return
	}

	// 免费次数 + 单次积分单价一起返回：
	// 前端要在"还剩几次免费"和"接下来要花多少积分"之间做展示切换。
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"credits": snap.CreditBalance,
			"face_analysis": gin.H{
				"free_limit":     snap.FaceFreeLimit,
				"free_remaining": snap.FaceFreeRemaining,
				"credit_cost":    snap.FaceCreditCost,
			},
			"hairstyle_design": gin.H{
				"free_limit":     snap.HairstyleFreeLimit,
				"free_remaining": snap.HairstyleFreeRemaining,
				"credit_cost":    snap.HairstyleCreditCost,
			},
		},
	})
}

// quotaUserID 与其它 Handler 一致的 Bearer 解析。
// 这里不抽公共函数：各 Handler 的错误码本就不同，
// 强行统一会让排查时更难定位是哪个接口拒的。
func quotaUserID(c *gin.Context, tokenSecret string) (int64, bool) {
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	parts := strings.Fields(authHeader)

	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    40101,
			"message": "missing or invalid authorization token",
		})
		return 0, false
	}

	userID, err := auth.Verify(parts[1], tokenSecret)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    40101,
			"message": "invalid or expired token",
		})
		return 0, false
	}

	return userID, true
}
