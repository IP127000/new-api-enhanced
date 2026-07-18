package codex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
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
			relayMode: relayconstant.RelayModeCodexWebSearch,
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
		RelayMode: relayconstant.RelayModeCodexWebSearch,
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
