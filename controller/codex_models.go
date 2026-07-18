package controller

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/codex"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const codexActorAuthorizationHeader = "x-openai-actor-authorization"

var codexModelsChannelCandidates = []string{
	"gpt-5.4",
	"gpt-5.5",
	"gpt-5.6-sol",
	"gpt-5.4-mini",
	"gpt-image-2",
}

func IsCodexModelsRequest(c *gin.Context) bool {
	return strings.TrimSpace(c.GetHeader(codexActorAuthorizationHeader)) != ""
}

// ListCodexModels proxies the subscription-backed Codex model catalog. The
// response wire shape intentionally differs from the public OpenAI model list.
func ListCodexModels(c *gin.Context) {
	channel, err := selectCodexModelsChannel(c)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "service_unavailable",
		}})
		return
	}

	oauthKey, err := codex.ParseOAuthKey(strings.TrimSpace(channel.Key))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "codex models: invalid channel credential",
			"type":    "upstream_error",
		}})
		return
	}

	status, headers, body, err := fetchCodexModels(c, channel, oauthKey.AccessToken, oauthKey.AccountID)
	if err != nil {
		common.SysError("failed to fetch codex models: " + err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "failed to fetch Codex model catalog",
			"type":    "upstream_error",
		}})
		return
	}

	if (status == http.StatusUnauthorized || status == http.StatusForbidden) && strings.TrimSpace(oauthKey.RefreshToken) != "" {
		refreshCtx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
		defer cancel()
		refreshed, refreshedChannel, refreshErr := service.RefreshCodexChannelCredential(
			refreshCtx,
			channel.Id,
			service.CodexCredentialRefreshOptions{ResetCaches: true},
		)
		if refreshErr == nil {
			channel = refreshedChannel
			status, headers, body, err = fetchCodexModels(c, channel, refreshed.AccessToken, refreshed.AccountID)
			if err != nil {
				common.SysError("failed to fetch codex models after refresh: " + err.Error())
			}
		}
	}

	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "failed to fetch Codex model catalog",
			"type":    "upstream_error",
		}})
		return
	}

	for _, name := range []string{"Content-Type", "ETag", "OpenAI-Request-ID", "x-request-id"} {
		if value := headers.Get(name); value != "" {
			c.Header(name, value)
		}
	}
	contentType := headers.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(status, contentType, body)
}

func selectCodexModelsChannel(c *gin.Context) (*model.Channel, error) {
	if channelIDValue, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId); ok {
		channelID, err := strconv.Atoi(fmt.Sprint(channelIDValue))
		if err != nil {
			return nil, fmt.Errorf("codex models: invalid token channel")
		}
		channel, err := model.GetChannelById(channelID, true)
		if err != nil {
			return nil, err
		}
		if channel == nil || channel.Type != constant.ChannelTypeCodex || channel.Status != common.ChannelStatusEnabled {
			return nil, fmt.Errorf("codex models: configured token channel is unavailable")
		}
		return channel, nil
	}

	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "" {
		group = common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	}
	for _, modelName := range codexModelsChannelCandidates {
		channel, err := model.GetRandomSatisfiedChannel(group, modelName, 0, "/v1/models")
		if err != nil {
			return nil, err
		}
		if channel != nil && channel.Type == constant.ChannelTypeCodex {
			return channel, nil
		}
	}
	return nil, fmt.Errorf("codex models: no subscription channel is available for group %q", group)
}

func fetchCodexModels(c *gin.Context, channel *model.Channel, accessToken string, accountID string) (int, http.Header, []byte, error) {
	client, err := service.NewProxyHttpClient(channel.GetSetting().Proxy)
	if err != nil {
		return 0, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	return service.FetchCodexModels(
		ctx,
		client,
		channel.GetBaseURL(),
		accessToken,
		accountID,
		c.Request.URL.RawQuery,
		c.Request.Header,
	)
}
