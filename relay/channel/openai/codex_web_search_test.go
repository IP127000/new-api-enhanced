package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexWebSearchHandlerForwardsResponseAndBuildsUsage(t *testing.T) {
	service.InitTokenEncoders()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", nil)
	body := `{"encrypted_output":"ciphertext","output":"OpenAI search result with a citation.","results":[{"type":"text_result","url":"https://openai.com"}]}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-4o",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o",
		},
	}
	info.SetEstimatePromptTokens(12)

	usage, err := CodexWebSearchHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	expectedSearchContentTokens := service.CountTextToken("OpenAI search result with a citation.", "gpt-4o")
	require.Equal(t, 12+expectedSearchContentTokens, usage.PromptTokens)
	require.Zero(t, usage.CompletionTokens)
	require.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)
	require.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
	require.JSONEq(t, body, recorder.Body.String())
}
