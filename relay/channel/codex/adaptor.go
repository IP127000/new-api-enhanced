package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("codex channel: endpoint not supported")
}

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	return nil, errors.New("codex channel: /v1/messages endpoint not supported")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("codex channel: endpoint not supported")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if info == nil || (info.RelayMode != relayconstant.RelayModeImagesGenerations &&
		info.RelayMode != relayconstant.RelayModeImagesEdits) {
		return nil, errors.New("codex channel: image endpoint not supported")
	}
	return request, nil
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	return nil, errors.New("codex channel: /v1/chat/completions endpoint not supported")
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("codex channel: /v1/rerank endpoint not supported")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("codex channel: /v1/embeddings endpoint not supported")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	isCompact := info != nil && info.RelayMode == relayconstant.RelayModeResponsesCompact

	if info != nil && info.ChannelSetting.SystemPrompt != "" {
		systemPrompt := info.ChannelSetting.SystemPrompt

		if len(request.Instructions) == 0 {
			if b, err := common.Marshal(systemPrompt); err == nil {
				request.Instructions = b
			} else {
				return nil, err
			}
		} else if info.ChannelSetting.SystemPromptOverride {
			var existing string
			if err := common.Unmarshal(request.Instructions, &existing); err == nil {
				existing = strings.TrimSpace(existing)
				if existing == "" {
					if b, err := common.Marshal(systemPrompt); err == nil {
						request.Instructions = b
					} else {
						return nil, err
					}
				} else {
					if b, err := common.Marshal(systemPrompt + "\n" + existing); err == nil {
						request.Instructions = b
					} else {
						return nil, err
					}
				}
			} else {
				if b, err := common.Marshal(systemPrompt); err == nil {
					request.Instructions = b
				} else {
					return nil, err
				}
			}
		}
	}
	// Codex backend requires the `instructions` field to be present.
	// Keep it consistent with Codex CLI behavior by defaulting to an empty string.
	if len(request.Instructions) == 0 {
		request.Instructions = json.RawMessage(`""`)
	}

	if isCompact {
		return request, nil
	}
	// codex: store must be false
	request.Store = json.RawMessage("false")
	// rm max_output_tokens
	request.MaxOutputTokens = nil
	request.Temperature = nil
	request.FrequencyPenalty = nil
	request.PresencePenalty = nil
	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if info != nil && (info.RelayMode == relayconstant.RelayModeRealtime || info.RelayMode == relayconstant.RelayModeRealtimeCall) {
		if isRealtimeCallRequest(info) {
			return channel.DoApiRequest(a, c, info, requestBody)
		}
		return channel.DoWssRequest(a, c, info, requestBody)
	}
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	switch info.RelayMode {
	case relayconstant.RelayModeRealtime:
		realtimeErr, realtimeUsage := openai.OpenaiRealtimeHandler(c, info)
		return realtimeUsage, realtimeErr
	case relayconstant.RelayModeAlphaSearch:
		// Alpha search responses are handled by relay.AlphaSearchHelper.
		return nil, types.NewError(errors.New("codex channel: alpha search response should be handled by AlphaSearchHelper"), types.ErrorCodeInvalidRequest)
	case relayconstant.RelayModeResponsesCompact:
		return openai.OaiResponsesCompactionHandler(c, resp)
	case relayconstant.RelayModeResponses:
		if info.IsStream {
			return openai.OaiResponsesStreamHandler(c, info, resp)
		}
		return openai.OaiResponsesHandler(c, info, resp)
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		if info.IsStream {
			return openai.OpenaiImageStreamHandler(c, info, resp)
		}
		return openai.OpenaiImageHandler(c, info, resp)
	default:
		return nil, types.NewError(errors.New("codex channel: endpoint not supported"), types.ErrorCodeInvalidRequest)
	}
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	var path string
	switch info.RelayMode {
	case relayconstant.RelayModeResponses:
		path = "/backend-api/codex/responses"
	case relayconstant.RelayModeResponsesCompact:
		path = "/backend-api/codex/responses/compact"
	case relayconstant.RelayModeImagesGenerations:
		path = "/backend-api/codex/images/generations"
	case relayconstant.RelayModeImagesEdits:
		path = "/backend-api/codex/images/edits"
	case relayconstant.RelayModeAlphaSearch:
		path = "/backend-api/codex/alpha/search"
	case relayconstant.RelayModeRealtime, relayconstant.RelayModeRealtimeCall:
		return codexRealtimeRequestURL(info)
	default:
		return "", errors.New("codex channel: endpoint not supported")
	}
	return relaycommon.GetFullRequestURL(info.ChannelBaseUrl, path, info.ChannelType), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)

	key := strings.TrimSpace(info.ApiKey)
	if !strings.HasPrefix(key, "{") {
		return errors.New("codex channel: key must be a JSON object")
	}

	oauthKey, err := ParseOAuthKey(key)
	if err != nil {
		return err
	}

	accessToken := strings.TrimSpace(oauthKey.AccessToken)
	accountID := strings.TrimSpace(oauthKey.AccountID)

	if accessToken == "" {
		return errors.New("codex channel: access_token is required")
	}
	if accountID == "" {
		return errors.New("codex channel: account_id is required")
	}

	req.Set("Authorization", "Bearer "+accessToken)
	req.Set("chatgpt-account-id", accountID)
	// The Codex client may use this header as a local capability marker for a
	// trusted custom provider. It is not an upstream credential and must never
	// be forwarded to ChatGPT.
	req.Del("x-openai-actor-authorization")
	if info.RelayMode == relayconstant.RelayModeRealtime || info.RelayMode == relayconstant.RelayModeRealtimeCall {
		headerNames := appconstant.CodexClientPassThroughHeaders()
		headerNames = append(headerNames, "openai-alpha", "x-session-id")
		for _, name := range headerNames {
			if value := strings.TrimSpace(c.Request.Header.Get(name)); value != "" {
				req.Set(name, value)
			}
		}
		if req.Get("originator") == "" {
			req.Set("originator", "codex_cli_rs")
		}
		if isRealtimeCallRequest(info) {
			contentType := strings.TrimSpace(c.Request.Header.Get("Content-Type"))
			if contentType == "" {
				return errors.New("codex channel: realtime call Content-Type is required")
			}
			req.Set("Content-Type", contentType)
			if accept := strings.TrimSpace(c.Request.Header.Get("Accept")); accept != "" {
				req.Set("Accept", accept)
			}
		}
		return nil
	}

	if (info.RelayMode == relayconstant.RelayModeResponses || info.RelayMode == relayconstant.RelayModeResponsesCompact) && req.Get("OpenAI-Beta") == "" {
		req.Set("OpenAI-Beta", "responses=experimental")
	}
	if req.Get("originator") == "" {
		req.Set("originator", "codex_cli_rs")
	}

	// chatgpt.com/backend-api/codex/responses is strict about Content-Type.
	// Clients may omit it or include parameters like `application/json; charset=utf-8`,
	// which can be rejected by the upstream. Force the exact media type.
	req.Set("Content-Type", "application/json")
	if info.IsStream {
		req.Set("Accept", "text/event-stream")
	} else if req.Get("Accept") == "" {
		req.Set("Accept", "application/json")
	}

	return nil
}

func codexRealtimeRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info == nil {
		return "", errors.New("codex channel: missing realtime relay info")
	}
	downstream, err := url.Parse(info.RequestURLPath)
	if err != nil {
		return "", fmt.Errorf("codex channel: invalid realtime request URL: %w", err)
	}

	// Codex subscription Responses, image generation, and web search use the
	// ChatGPT backend. Realtime media is different: the Codex harness opens its
	// websocket against the OpenAI Realtime data plane and authenticates it with
	// the same subscription OAuth/account headers. Pointing this websocket at
	// chatgpt.com is rejected by its browser-only Cloudflare challenge.
	baseURL := strings.TrimRight(strings.TrimSpace(info.ChannelBaseUrl), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	} else if channelBase, parseErr := url.Parse(baseURL); parseErr == nil &&
		strings.EqualFold(channelBase.Hostname(), "chatgpt.com") {
		baseURL = "https://api.openai.com"
	}

	upstreamPath := "/v1/realtime"
	isCall := isRealtimeCallRequest(info)
	if isCall {
		upstreamPath = downstream.Path
	}
	if strings.HasPrefix(downstream.Path, "/v1/live") {
		upstreamPath = downstream.Path
	}
	fullURL := relaycommon.GetFullRequestURL(baseURL, upstreamPath, info.ChannelType)
	upstream, err := url.Parse(fullURL)
	if err != nil {
		return "", fmt.Errorf("codex channel: invalid realtime upstream URL: %w", err)
	}
	if !isCall {
		switch upstream.Scheme {
		case "https":
			upstream.Scheme = "wss"
		case "http":
			upstream.Scheme = "ws"
		case "ws", "wss":
		default:
			return "", fmt.Errorf("codex channel: unsupported realtime URL scheme %q", upstream.Scheme)
		}
	} else if upstream.Scheme != "http" && upstream.Scheme != "https" {
		return "", fmt.Errorf("codex channel: unsupported realtime call URL scheme %q", upstream.Scheme)
	}

	query := upstream.Query()
	for _, name := range []string{"intent", "call_id", "architecture", "trace"} {
		for _, value := range downstream.Query()[name] {
			query.Add(name, value)
		}
	}
	if !isCall && downstream.Query().Get("call_id") == "" && !strings.HasPrefix(downstream.Path, "/v1/live/") {
		model := strings.TrimSpace(info.UpstreamModelName)
		if model == "" {
			model = strings.TrimSpace(downstream.Query().Get("model"))
		}
		if model != "" {
			query.Set("model", model)
		}
	}
	upstream.RawQuery = query.Encode()
	return upstream.String(), nil
}

func isRealtimeCallRequest(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	downstream, err := url.Parse(info.RequestURLPath)
	return err == nil && (downstream.Path == "/v1/realtime/calls" || info.RelayMode == relayconstant.RelayModeRealtimeCall)
}
