package codex

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexRequestPreservesBodyTraceHeadersAndServerAuth(t *testing.T) {
	service.InitHttpClient()

	var capturedBody string
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = string(body)
		capturedHeaders = r.Header.Clone()
		require.Equal(t, "/backend-api/codex/responses", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	originalBody := `{"model":"gpt-5","store":false,"prompt_cache_key":"cache-key","input":"hello"}`
	incoming := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(originalBody))
	incoming.Header.Set("Content-Type", "application/json; charset=utf-8")
	incoming.Header.Set("Authorization", "Bearer client-token")
	incoming.Header.Set("Thread_id", "thread-underscore-123")
	incoming.Header.Set("X-OpenAI-Memgen-Request", "memgen-123")
	incoming.Header.Set("X-ResponsesAPI-Include-Timing-Metrics", "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = incoming

	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		RequestHeaders: map[string]string{
			"Authorization":                         "Bearer client-token",
			"Thread_id":                             "thread-underscore-123",
			"X-OpenAI-Memgen-Request":               "memgen-123",
			"X-ResponsesAPI-Include-Timing-Metrics": "true",
		},
		UpstreamRequestBodySize: int64(len(originalBody)),
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:        appconstant.APITypeCodex,
			ChannelType:    appconstant.ChannelTypeCodex,
			ChannelBaseUrl: server.URL,
			ApiKey:         `{"access_token":"server-token","account_id":"server-account"}`,
		},
	}

	require.NoError(t, relaycommon.ApplyCodexClientHeaderPassthroughWithRelayInfo(info))
	result, err := (&Adaptor{}).DoRequest(c, info, strings.NewReader(originalBody))
	require.NoError(t, err)
	resp, ok := result.(*http.Response)
	require.True(t, ok)
	require.NotNil(t, resp)
	require.NoError(t, resp.Body.Close())

	require.Equal(t, originalBody, capturedBody)
	require.Equal(t, "Bearer server-token", capturedHeaders.Get("Authorization"))
	require.Equal(t, "server-account", capturedHeaders.Get("chatgpt-account-id"))
	require.Equal(t, "thread-underscore-123", capturedHeaders.Get("Thread_id"))
	require.Equal(t, "memgen-123", capturedHeaders.Get("X-OpenAI-Memgen-Request"))
	require.Equal(t, "true", capturedHeaders.Get("X-ResponsesAPI-Include-Timing-Metrics"))
	require.Equal(t, "application/json", capturedHeaders.Get("Content-Type"))
}
