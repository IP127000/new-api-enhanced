package relay

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

// CodexWebSearchHelper proxies the standalone /v1/alpha/search protocol used
// by Codex Responses Lite. The payload is not an OpenAI Responses request and
// must not pass through a protocol translator.
func CodexWebSearchHelper(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	info.InitChannelMeta(c)
	if info.ApiType != appconstant.APITypeCodex || info.ChannelType != appconstant.ChannelTypeCodex {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("Codex alpha search requires a type %d Codex OAuth channel", appconstant.ChannelTypeCodex),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if _, ok := info.Request.(*dto.CodexWebSearchRequest); !ok {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid Codex web search request type %T", info.Request),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	if err := relaycommon.ApplyCodexClientHeaderPassthroughWithRelayInfo(info); err != nil {
		return newAPIErrorFromParamOverride(err)
	}

	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
	}
	body, err := storage.Bytes()
	if err != nil {
		return types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
	}
	body, err = sanitizeCodexWebSearchBody(body)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	info.UpstreamRequestBodySize = int64(len(body))

	resp, err := adaptor.DoRequest(c, info, bytes.NewReader(body))
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	httpResp, ok := resp.(*http.Response)
	if !ok || httpResp == nil {
		return types.NewError(fmt.Errorf("invalid Codex web search response type %T", resp), types.ErrorCodeBadResponse)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	if httpResp.StatusCode != http.StatusOK {
		newAPIError := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}
	usageDto, ok := usage.(*dto.Usage)
	if !ok || usageDto == nil {
		return types.NewError(fmt.Errorf("invalid Codex web search usage type %T", usage), types.ErrorCodeBadResponse)
	}
	service.PostTextConsumeQuota(c, info, usageDto, nil)
	return nil
}

func sanitizeCodexWebSearchBody(body []byte) ([]byte, error) {
	var err error
	for _, field := range []string{"prompt_cache_key", "prompt_cache_retention"} {
		body, err = sjson.DeleteBytes(body, field)
		if err != nil {
			return nil, fmt.Errorf("remove unsupported Codex search field %q: %w", field, err)
		}
	}
	return body, nil
}
