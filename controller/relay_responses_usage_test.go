package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldUseCodexResponsesUpstreamUsageOnlyIsStrictlyScoped(t *testing.T) {
	originalSelfUse := operation_setting.SelfUseModeEnabled
	t.Cleanup(func() { operation_setting.SelfUseModeEnabled = originalSelfUse })
	operation_setting.SelfUseModeEnabled = true

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeCodex)
	info := relaycommon.GenRelayInfoResponses(c, &dto.OpenAIResponsesRequest{Model: "gpt-test"})

	// This is the real controller lifecycle: ChannelMeta is still nil until
	// ResponsesHelper starts, while the selected channel is already in context.
	require.Nil(t, info.ChannelMeta)
	require.True(t, shouldUseCodexResponsesUpstreamUsageOnly(c, info))

	operation_setting.SelfUseModeEnabled = false
	require.False(t, shouldUseCodexResponsesUpstreamUsageOnly(c, info))
	operation_setting.SelfUseModeEnabled = true

	info.RelayFormat = types.RelayFormatOpenAI
	require.False(t, shouldUseCodexResponsesUpstreamUsageOnly(c, info))
	info.RelayFormat = types.RelayFormatOpenAIResponses
	info.RelayFormat = types.RelayFormatOpenAIResponsesCompaction
	require.False(t, shouldUseCodexResponsesUpstreamUsageOnly(c, info))
	info.RelayFormat = types.RelayFormatOpenAIResponses

	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	require.False(t, shouldUseCodexResponsesUpstreamUsageOnly(c, info))
	require.False(t, shouldUseCodexResponsesUpstreamUsageOnly(nil, info))
	require.False(t, shouldUseCodexResponsesUpstreamUsageOnly(c, nil))
}
