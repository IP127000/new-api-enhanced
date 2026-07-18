package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetRankings(c *gin.Context) {
	scope, ok := getRankingsScope(c)
	if !ok {
		return
	}

	result, err := service.GetRankingsSnapshot(c.DefaultQuery("period", "week"), scope)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

func getRankingsScope(c *gin.Context) (service.RankingScope, bool) {
	userID := c.GetInt("id")
	if userID <= 0 {
		common.ApiErrorMsg(c, "Please log in to view rankings")
		return service.RankingScope{}, false
	}

	role := c.GetInt("role")
	if role == 0 {
		user, err := model.GetUserById(userID, false)
		if err != nil {
			common.ApiError(c, err)
			return service.RankingScope{}, false
		}
		if user.Status == common.UserStatusDisabled {
			common.ApiErrorMsg(c, "User is disabled")
			return service.RankingScope{}, false
		}
		role = user.Role
	}

	if role >= common.RoleAdminUser {
		return service.RankingScope{Global: true}, true
	}
	if role >= common.RoleCommonUser {
		return service.RankingScope{UserID: userID}, true
	}

	common.ApiErrorMsg(c, "Insufficient privilege")
	return service.RankingScope{}, false
}
