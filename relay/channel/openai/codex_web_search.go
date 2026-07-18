package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type codexWebSearchResponse struct {
	EncryptedOutput string            `json:"encrypted_output,omitempty"`
	Output          string            `json:"output"`
	Results         []json.RawMessage `json:"results,omitempty"`
}

// CodexWebSearchHandler returns the standalone search JSON unchanged. Its
// response shape is not compatible with OpenAI Responses and therefore must
// not be decoded by OaiResponsesHandler.
func CodexWebSearchHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	var searchResp codexWebSearchResponse
	if err := common.Unmarshal(body, &searchResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if searchResp.Output == "" && searchResp.EncryptedOutput == "" && searchResp.Results == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid Codex web search response"), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}
	service.IOCopyBytesGracefully(c, resp, body)

	promptTokens := 0
	modelName := ""
	if info != nil {
		promptTokens = info.GetEstimatePromptTokens()
		modelName = info.UpstreamModelName
		if modelName == "" {
			modelName = info.OriginModelName
		}
	}
	// The standalone endpoint returns search context that is fed back into the
	// model. It is input content, not model completion output, so it must not be
	// multiplied by the model's completion-token ratio.
	promptTokens += service.CountTextToken(searchResp.Output, modelName)
	common.SetContextKey(c, appconstant.ContextKeyLocalCountTokens, true)
	return &dto.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: 0,
		TotalTokens:      promptTokens,
	}, nil
}
