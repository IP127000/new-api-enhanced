package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIsCodexModelsRequestUsesActorAuthorizationMarker(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	require.False(t, IsCodexModelsRequest(c))

	c.Request.Header.Set("x-openai-actor-authorization", "new-api-enhanced")
	require.True(t, IsCodexModelsRequest(c))
}

func TestCodexModelsSelectsSubscriptionChannelInMixedGroup(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	oldCache := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = oldCache })
	channels := []*model.Channel{
		{Id: 301, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Group: "default", Models: "gpt-5.4", Priority: common.GetPointer(int64(100)), Key: "api-key"},
		{Id: 302, Type: constant.ChannelTypeCodex, Status: common.ChannelStatusEnabled, Group: "default", Models: "gpt-5.4", Priority: common.GetPointer(int64(0)), Key: `{"access_token":"subscription-token","account_id":"subscription-account"}`},
	}
	require.NoError(t, db.Create(channels).Error)
	for _, channel := range channels {
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "gpt-5.4", ChannelId: channel.Id, Enabled: true, Priority: channel.Priority}).Error)
	}
	for _, cached := range []bool{false, true} {
		common.MemoryCacheEnabled = cached
		if cached {
			model.InitChannelCache()
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		selected, err := selectCodexModelsChannel(c)
		require.NoError(t, err)
		require.NotNil(t, selected)
		require.Equal(t, 302, selected.Id)
	}
}
