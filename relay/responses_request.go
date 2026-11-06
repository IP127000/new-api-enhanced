package relay

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	appconstant "github.com/QuantumNous/new-api/constant"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

// PrepareResponsesRequest applies the same model, conversion and channel rules
// for HTTP and WebSocket requests. The caller closes closer after the attempt;
// passthrough bodies remain owned by the incoming request's BodyStorage.
// The returned adaptor retains route/conversion state for DoRequest/DoResponse.
func PrepareResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.OpenAIResponsesRequest) (relaychannel.Adaptor, common.ReplayableBody, io.Closer, *types.NewAPIError) {
	info.InitChannelMeta(c)
	if info.RelayMode == relayconstant.RelayModeResponsesCompact &&
		!common.SupportsResponsesCompact(info.ChannelType, info.ApiType) {
		return nil, nil, nil, types.NewErrorWithStatusCode(
			fmt.Errorf("unsupported endpoint %q for api type %d", "/v1/responses/compact", info.ApiType),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	originalModel := req.Model
	useCodexOriginalBody := shouldUseCodexOriginalResponsesBody(info) && info.ChannelSetting.SystemPrompt == ""
	if !useCodexOriginalBody && common.GetContextKeyBool(c, appconstant.ContextKeyCodexResponsesMinimalRequest) {
		req = &dto.OpenAIResponsesRequest{}
		if err := common.UnmarshalBodyReusable(c, req); err != nil {
			return nil, nil, nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		if err := helper.ValidateResponsesRequest(req); err != nil {
			return nil, nil, nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
	}
	var request *dto.OpenAIResponsesRequest
	var err error
	if useCodexOriginalBody {
		copy := *req
		request = &copy
	} else {
		request, err = common.DeepCopy(req)
	}
	if err != nil {
		return nil, nil, nil, types.NewError(fmt.Errorf("failed to copy responses request: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if err := helper.ModelMappedHelper(c, info, request); err != nil {
		return nil, nil, nil, types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	modelBeforeModifiers := request.Model
	if err := helper.ApplyReasoningModelSuffix(c, info, request); err != nil {
		return nil, nil, nil, newConvertRequestFailedError(c, info, err)
	}

	// Explicit model modifiers must use the official converter. Rehydrate the
	// minimal subscription shell before conversion so input and tools survive.
	if useCodexOriginalBody && (request.Model != modelBeforeModifiers || info.ReasoningConversion != nil || request.TopP != req.TopP) {
		request = &dto.OpenAIResponsesRequest{}
		if err := common.UnmarshalBodyReusable(c, request); err != nil {
			return nil, nil, nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		if err := helper.ModelMappedHelper(c, info, request); err != nil {
			return nil, nil, nil, types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
		}
		if err := helper.ApplyReasoningModelSuffix(c, info, request); err != nil {
			return nil, nil, nil, newConvertRequestFailedError(c, info, err)
		}
		useCodexOriginalBody = false
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return nil, nil, nil, types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	if shouldUseCodexOriginalResponsesBody(info) {
		if err := relaycommon.ApplyCodexClientHeaderPassthroughWithRelayInfo(info); err != nil {
			return nil, nil, nil, newAPIErrorFromParamOverride(err)
		}
	}
	if useCodexOriginalBody {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, nil, nil, types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		mappedModel := ""
		if info.IsModelMapped || request.Model != originalModel {
			mappedModel = request.Model
		}
		outbound, owned, err := prepareCodexOriginalResponsesBody(c, storage, info, info.RelayMode == relayconstant.RelayModeResponsesCompact, mappedModel)
		if err != nil {
			var overrideErr *codexParamOverrideApplyError
			if errors.As(err, &overrideErr) {
				return nil, nil, nil, newAPIErrorFromParamOverride(err)
			}
			return nil, nil, nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		body := common.NewReplayableBodyReader(outbound)
		if owned {
			return adaptor, body, outbound, nil
		}
		return adaptor, body, io.NopCloser(body), nil
	}
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, nil, nil, types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		body := common.NewReplayableBodyReader(storage)
		return adaptor, body, io.NopCloser(body), nil
	}

	convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, info, *request)
	if err != nil {
		return nil, nil, nil, newConvertRequestFailedError(c, info, err)
	}
	relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)
	jsonData, err := common.Marshal(convertedRequest)
	if err != nil {
		return nil, nil, nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
	if err != nil {
		return nil, nil, nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return nil, nil, nil, newAPIErrorFromParamOverride(err)
		}
	}

	logger.LogDebug(c, "requestBody: %s", jsonData)
	body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return nil, nil, nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return adaptor, body, closer, nil
}
