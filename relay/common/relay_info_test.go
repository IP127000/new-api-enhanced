package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRelayInfoGetFinalRequestRelayFormatPrefersExplicitFinal(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		RequestConversionChain:  []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
		FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatFallsBackToConversionChain(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:            types.RelayFormatOpenAI,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatFallsBackToRelayFormat(t *testing.T) {
	info := &RelayInfo{
		RelayFormat: types.RelayFormatGemini,
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatGemini), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatNilReceiver(t *testing.T) {
	var info *RelayInfo
	require.Equal(t, types.RelayFormat(""), info.GetFinalRequestRelayFormat())
}

func TestGenRelayInfoCodexWebSearchTracksStandaloneCall(t *testing.T) {
	t.Parallel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", nil)
	request := &dto.CodexWebSearchRequest{
		Model: "gpt-5.6-sol",
		Settings: dto.CodexWebSearchSettings{
			SearchContextSize: "low",
		},
	}

	info := GenRelayInfoCodexWebSearch(c, request)
	require.Equal(t, relayconstant.RelayModeCodexWebSearch, info.RelayMode)
	require.Equal(t, types.RelayFormat(types.RelayFormatCodexWebSearch), info.RelayFormat)
	tool := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearch]
	require.NotNil(t, tool)
	require.Equal(t, 1, tool.CallCount)
	require.Equal(t, "low", tool.SearchContextSize)
}

func TestGenRelayInfoResponsesExtractsToolUsageFromRawJSON(t *testing.T) {
	t.Parallel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	request := &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Tools: []byte(`[
			{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"query":{"type":"string"}}}},
			{"type":"web_search_preview","search_context_size":"high"},
			{"type":"web_search"}
		]`),
	}

	info := GenRelayInfoResponses(c, request)
	require.Len(t, info.ResponsesUsageInfo.BuiltInTools, 3)
	require.Equal(t, "high", info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview].SearchContextSize)
	require.Equal(t, "medium", info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearch].SearchContextSize)
	require.Equal(t, "function", info.ResponsesUsageInfo.BuiltInTools["function"].ToolName)
}
