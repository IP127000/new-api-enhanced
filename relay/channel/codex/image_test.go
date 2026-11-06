package codex

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertImageGenerationRequestForCodexEndpoint(t *testing.T) {
	t.Parallel()

	request := dto.ImageRequest{
		Model:   "gpt-image-2",
		Prompt:  "a small red fox",
		Quality: "auto",
		Size:    "auto",
	}

	converted, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}, request)
	require.NoError(t, err)
	assert.Equal(t, request, converted)
}

func TestConvertImageEditRequestForCodexEndpoint(t *testing.T) {
	t.Parallel()

	request := dto.ImageRequest{
		Model:  "gpt-image-2",
		Prompt: "add a red hat",
		Images: json.RawMessage(`[{"image_url":"data:image/png;base64,Zm9v"}]`),
	}
	converted, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesEdits,
	}, request)
	require.NoError(t, err)
	assert.Equal(t, request, converted)
}

func TestGetRequestURLSupportsCodexImagesEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		relayMode int
		want      string
	}{
		{
			name:      "generation",
			relayMode: relayconstant.RelayModeImagesGenerations,
			want:      "https://chatgpt.com/backend-api/codex/images/generations",
		},
		{
			name:      "edit",
			relayMode: relayconstant.RelayModeImagesEdits,
			want:      "https://chatgpt.com/backend-api/codex/images/edits",
		},
		{
			name:      "standalone web search",
			relayMode: relayconstant.RelayModeAlphaSearch,
			want:      "https://chatgpt.com/backend-api/codex/alpha/search",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := (&Adaptor{}).GetRequestURL(&relaycommon.RelayInfo{
				RelayMode: test.relayMode,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl: "https://chatgpt.com",
				},
			})
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestSetupRequestHeaderForCodexWebSearch(t *testing.T) {
	t.Parallel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", nil)
	headers := http.Header{
		"X-Openai-Actor-Authorization": []string{"new-api-enhanced"},
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeAlphaSearch,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: `{"access_token":"upstream-access","account_id":"account-123"}`,
		},
	}

	err := (&Adaptor{}).SetupRequestHeader(c, &headers, info)
	require.NoError(t, err)
	assert.Equal(t, "Bearer upstream-access", headers.Get("Authorization"))
	assert.Equal(t, "account-123", headers.Get("chatgpt-account-id"))
	assert.Equal(t, "application/json", headers.Get("Content-Type"))
	assert.Equal(t, "application/json", headers.Get("Accept"))
	assert.Equal(t, "codex_cli_rs", headers.Get("originator"))
	assert.Empty(t, headers.Get("x-openai-actor-authorization"))
	assert.Empty(t, headers.Get("OpenAI-Beta"))
}

func TestCodexModelListIncludesImageModel(t *testing.T) {
	t.Parallel()
	assert.Contains(t, ModelList, "gpt-image-2")
	assert.NotContains(t, ModelList, "gpt-image-2-compact")
}

func TestSetupRequestHeaderConsumesActorAuthorizationMarker(t *testing.T) {
	t.Parallel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	headers := http.Header{
		"X-Openai-Actor-Authorization": []string{"new-api-enhanced"},
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: `{"access_token":"upstream-access","account_id":"account-123"}`,
		},
	}

	err := (&Adaptor{}).SetupRequestHeader(c, &headers, info)
	require.NoError(t, err)
	assert.Empty(t, headers.Get("x-openai-actor-authorization"))
	assert.Equal(t, "Bearer upstream-access", headers.Get("Authorization"))
	assert.Equal(t, "account-123", headers.Get("chatgpt-account-id"))
}

func TestSetupRequestHeaderConsumesActorAuthorizationMarkerForImageEdit(t *testing.T) {
	t.Parallel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)
	headers := http.Header{
		"X-Openai-Actor-Authorization": []string{"new-api-enhanced"},
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesEdits,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: `{"access_token":"upstream-access","account_id":"account-123"}`,
		},
	}

	err := (&Adaptor{}).SetupRequestHeader(c, &headers, info)
	require.NoError(t, err)
	assert.Empty(t, headers.Get("x-openai-actor-authorization"))
	assert.Equal(t, "Bearer upstream-access", headers.Get("Authorization"))
	assert.Equal(t, "account-123", headers.Get("chatgpt-account-id"))
	assert.Equal(t, "application/json", headers.Get("Content-Type"))
}

func TestCodexSearchAndImagesHTTPRoundTrip(t *testing.T) {
	service.InitHttpClient()
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	for _, tc := range []struct {
		name, path, body, response string
		mode                       int
		stream                     bool
	}{
		{name: "web_search", path: "/backend-api/codex/alpha/search", body: `{"model":"gpt-5.6-sol","query":"New API","future":{"keep":true}}`, response: `{"results":[{"url":"https://example.com","title":"result"}],"future":true}`, mode: relayconstant.RelayModeAlphaSearch},
		{name: "image_generation", path: "/backend-api/codex/images/generations", body: `{"model":"gpt-image-2","prompt":"red fox","background":"transparent","future":true}`, response: `{"data":[{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":4,"output_tokens":8}}`, mode: relayconstant.RelayModeImagesGenerations},
		{name: "image_edit", path: "/backend-api/codex/images/edits", body: `{"model":"gpt-image-2","prompt":"red hat","images":[{"image_url":"data:image/png;base64,aW1hZ2U="}]}`, response: `{"data":[{"b64_json":"ZWRpdA=="}],"usage":{"input_tokens":4,"output_tokens":8}}`, mode: relayconstant.RelayModeImagesEdits},
		{name: "image_stream", path: "/backend-api/codex/images/generations", body: `{"model":"gpt-image-2","prompt":"fox","stream":true}`, response: "event: image_generation.completed\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":\"aW1hZ2U=\",\"usage\":{\"input_tokens\":4,\"output_tokens\":8}}\n\n", mode: relayconstant.RelayModeImagesGenerations, stream: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tc.path, r.URL.Path)
				assert.Equal(t, "Bearer subscription-token", r.Header.Get("Authorization"))
				assert.Equal(t, "subscription-account", r.Header.Get("chatgpt-account-id"))
				assert.Empty(t, r.Header.Get("x-openai-actor-authorization"))
				raw, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, tc.body, string(raw))
				contentType := "application/json"
				if tc.stream {
					contentType = "text/event-stream"
				}
				w.Header().Set("Content-Type", contentType)
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader(tc.body))
			c.Request.Header.Set("Authorization", "Bearer client-token")
			c.Request.Header.Set("x-openai-actor-authorization", "client-marker")
			info := &relaycommon.RelayInfo{RelayMode: tc.mode, IsStream: tc.stream, StartTime: time.Now(), ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeCodex, ApiType: constant.APITypeCodex, ChannelBaseUrl: server.URL, ApiKey: `{"access_token":"subscription-token","account_id":"subscription-account"}`}}
			adaptor := &Adaptor{}
			result, err := adaptor.DoRequest(c, info, strings.NewReader(tc.body))
			require.NoError(t, err)
			resp := result.(*http.Response)
			if tc.mode == relayconstant.RelayModeAlphaSearch {
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Equal(t, tc.response, string(body))
			} else {
				usage, apiErr := adaptor.DoResponse(c, resp, info)
				require.Nil(t, apiErr)
				require.Equal(t, 4, usage.(*dto.Usage).PromptTokens)
				require.Equal(t, 8, usage.(*dto.Usage).CompletionTokens)
				require.Contains(t, recorder.Body.String(), "b64_json")
			}
		})
	}
}
