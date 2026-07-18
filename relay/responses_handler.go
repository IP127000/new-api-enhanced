package relay

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func ResponsesHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)
	if info.RelayMode == relayconstant.RelayModeResponsesCompact {
		switch info.ApiType {
		case appconstant.APITypeOpenAI, appconstant.APITypeCodex:
		default:
			return types.NewErrorWithStatusCode(
				fmt.Errorf("unsupported endpoint %q for api type %d", "/v1/responses/compact", info.ApiType),
				types.ErrorCodeInvalidRequest,
				http.StatusBadRequest,
				types.ErrOptionWithSkipRetry(),
			)
		}
	}

	var responsesReq *dto.OpenAIResponsesRequest
	switch req := info.Request.(type) {
	case *dto.OpenAIResponsesRequest:
		responsesReq = req
	case *dto.OpenAIResponsesCompactionRequest:
		// Only fields documented for POST /v1/responses/compact are forwarded:
		// model, input, instructions, previous_response_id, prompt_cache_key,
		// prompt_cache_options, prompt_cache_retention, service_tier.
		// Undocumented Codex-parity fields (tools, reasoning, text) are parsed
		// for client compatibility but intentionally not sent upstream.
		responsesReq = &dto.OpenAIResponsesRequest{
			Model:                req.Model,
			Input:                req.Input,
			Instructions:         req.Instructions,
			PreviousResponseID:   req.PreviousResponseID,
			ParallelToolCalls:    req.ParallelToolCalls,
			ServiceTier:          req.ServiceTier,
			PromptCacheKey:       req.PromptCacheKey,
			PromptCacheOptions:   req.PromptCacheOptions,
			PromptCacheRetention: req.PromptCacheRetention,
		}
	default:
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected dto.OpenAIResponsesRequest or dto.OpenAIResponsesCompactionRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	useCodexOriginalBody := shouldUseCodexOriginalResponsesBody(info)
	if !useCodexOriginalBody && common.GetContextKeyBool(c, appconstant.ContextKeyCodexResponsesMinimalRequest) {
		// The distributor initially selected a Codex channel, but a retry can
		// move to a different channel type. Rehydrate the complete DTO only for
		// that exceptional conversion path; Codex original-body attempts remain
		// allocation-bounded.
		fullRequest := &dto.OpenAIResponsesRequest{}
		if err := common.UnmarshalBodyReusable(c, fullRequest); err != nil {
			return types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		if err := helper.ValidateResponsesRequest(fullRequest); err != nil {
			return types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		responsesReq = fullRequest
	}
	var request *dto.OpenAIResponsesRequest
	if useCodexOriginalBody {
		// The Codex path forwards the original stored JSON body. It only needs a
		// separate Model field for ModelMappedHelper, so a shallow struct copy
		// avoids duplicating large Input/Tools/Instructions RawMessages.
		requestCopy := *responsesReq
		request = &requestCopy
	} else {
		copiedRequest, err := common.DeepCopy(responsesReq)
		if err != nil {
			return types.NewError(fmt.Errorf("failed to copy request to GeneralOpenAIRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		request = copiedRequest
	}

	if err := helper.ModelMappedHelper(c, info, request); err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	var requestBody io.Reader
	var codexOutboundStorage common.BodyStorage
	var closeCodexOutboundStorage bool
	if useCodexOriginalBody {
		if err := relaycommon.ApplyCodexClientHeaderPassthroughWithRelayInfo(info); err != nil {
			return newAPIErrorFromParamOverride(err)
		}
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		mappedModel := ""
		if info.IsModelMapped {
			mappedModel = request.Model
		}
		codexOutboundStorage, closeCodexOutboundStorage, err = prepareCodexOriginalResponsesBody(
			c,
			storage,
			info,
			info.RelayMode == relayconstant.RelayModeResponsesCompact,
			mappedModel,
		)
		if err != nil {
			var overrideErr *codexParamOverrideApplyError
			if errors.As(err, &overrideErr) {
				return newAPIErrorFromParamOverride(err)
			}
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		if closeCodexOutboundStorage {
			defer codexOutboundStorage.Close()
		}
		attemptReader, err := codexOutboundStorage.NewReader()
		if err != nil {
			return types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		defer attemptReader.Close()
		info.UpstreamRequestBodySize = codexOutboundStorage.Size()
		requestBody = attemptReader
	} else if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		attemptReader, err := storage.NewReader()
		if err != nil {
			return types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		defer attemptReader.Close()
		info.UpstreamRequestBodySize = storage.Size()
		requestBody = attemptReader
	} else {
		convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, info, *request)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)
		jsonData, err := common.Marshal(convertedRequest)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		// remove disabled fields for OpenAI Responses API
		jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		// apply param override
		if len(info.ParamOverride) > 0 {
			jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
			if err != nil {
				return newAPIErrorFromParamOverride(err)
			}
		}

		logger.LogDebug(c, "requestBody: %s", jsonData)
		body, size, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		defer closer.Close()
		jsonData = nil
		info.UpstreamRequestBodySize = size
		requestBody = body
	}

	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	if resp != nil {
		httpResp = resp.(*http.Response)

		if httpResp.StatusCode != http.StatusOK {
			newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
			// reset status code 重置状态码
			service.ResetStatusCode(newAPIError, statusCodeMappingStr)
			return newAPIError
		}
	}
	if useCodexOriginalBody && info.IsStream && httpResp != nil && httpResp.StatusCode == http.StatusOK {
		// HTTP 200 commits a Codex Responses stream in the current relay: any
		// later SSE failure is surfaced on that stream rather than replayed. The
		// per-attempt reader has already been closed by doRequest, so the replay
		// storage is no longer needed while generation continues.
		commitCodexResponsesStream(c, codexOutboundStorage, closeCodexOutboundStorage)
	}

	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		// reset status code 重置状态码
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	usageDto := usage.(*dto.Usage)
	if info.RelayMode == relayconstant.RelayModeResponsesCompact {
		originModelName := info.OriginModelName
		originPriceData := info.PriceData

		_, err := helper.ModelPriceHelper(c, info, info.GetEstimatePromptTokens(), &types.TokenCountMeta{})
		if err != nil {
			info.OriginModelName = originModelName
			info.PriceData = originPriceData
			return types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithSkipRetry(), types.ErrOptionWithStatusCode(http.StatusBadRequest))
		}
		service.PostTextConsumeQuota(c, info, usageDto, nil)

		info.OriginModelName = originModelName
		info.PriceData = originPriceData
		return nil
	}

	if strings.HasPrefix(info.OriginModelName, "gpt-4o-audio") {
		service.PostAudioConsumeQuota(c, info, usageDto, "")
	} else {
		service.PostTextConsumeQuota(c, info, usageDto, nil)
	}
	return nil
}

type codexParamOverrideApplyError struct {
	err error
}

func (e *codexParamOverrideApplyError) Error() string {
	return e.err.Error()
}

func (e *codexParamOverrideApplyError) Unwrap() error {
	return e.err
}

func commitCodexResponsesStream(c *gin.Context, outboundStorage common.BodyStorage, closeOutboundStorage bool) {
	common.SetContextKey(c, appconstant.ContextKeyRelayRetryCommitted, true)
	if closeOutboundStorage && outboundStorage != nil {
		_ = outboundStorage.Close()
	}
	common.ReleaseBodyStorage(c)
}

func prepareCodexOriginalResponsesBody(
	c *gin.Context,
	storage common.BodyStorage,
	info *relaycommon.RelayInfo,
	compact bool,
	mappedModel string,
) (common.BodyStorage, bool, error) {
	if len(info.ParamOverride) > 0 {
		if relaycommon.CanApplyParamOverrideWithoutBody(info.ParamOverride) {
			if _, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{}`), info); err != nil {
				return nil, false, &codexParamOverrideApplyError{err: err}
			}
		} else {
			// Arbitrary parameter overrides can address nested paths and therefore
			// retain the established in-memory fallback. Normal Codex subscription
			// traffic only has header operations and uses the streaming path below.
			body, err := storage.Bytes()
			if err != nil {
				return nil, false, err
			}
			body, err = relaycommon.ApplyParamOverrideWithRelayInfo(body, info)
			if err != nil {
				return nil, false, &codexParamOverrideApplyError{err: err}
			}
			body, _, err = sanitizeCodexOriginalResponsesBody(body, compact, mappedModel)
			if err != nil {
				return nil, false, err
			}
			outboundStorage, err := common.CreateBodyStorage(body)
			if err != nil {
				return nil, false, err
			}
			return outboundStorage, true, nil
		}
	}

	fields, err := common.GetOrIndexJSONBodyFields(c, storage)
	if err != nil {
		return nil, false, err
	}
	fieldsByName := make(map[string]common.JSONFieldSpan, len(fields))
	for _, field := range fields {
		fieldsByName[field.Name] = field
	}

	edits := make(map[string]common.JSONFieldEdit, 4)
	additions := make([]common.JSONFieldAddition, 0, 2)
	changed := false
	if mappedModel != "" {
		currentModel := ""
		if modelField, exists := fieldsByName["model"]; exists {
			modelJSON, err := common.ReadJSONSpan(storage, modelField.Value, 64<<10)
			if err != nil {
				return nil, false, err
			}
			currentModel = common.JsonRawMessageToString(modelJSON)
		}
		if currentModel != mappedModel {
			encodedModel, err := common.Marshal(mappedModel)
			if err != nil {
				return nil, false, err
			}
			edits["model"] = common.JSONFieldEdit{Replacement: encodedModel}
			changed = true
		}
	}
	if _, exists := fieldsByName["instructions"]; !exists {
		additions = append(additions, common.JSONFieldAddition{Name: "instructions", Value: []byte(`""`)})
		changed = true
	}
	if !compact {
		if storeField, exists := fieldsByName["store"]; exists {
			isFalse := false
			if storeField.Value.End-storeField.Value.Start == int64(len("false")) {
				storeJSON, err := common.ReadJSONSpan(storage, storeField.Value, int64(len("false")))
				if err != nil {
					return nil, false, err
				}
				isFalse = string(storeJSON) == "false"
			}
			if !isFalse {
				edits["store"] = common.JSONFieldEdit{Replacement: []byte("false")}
				changed = true
			}
		} else {
			additions = append(additions, common.JSONFieldAddition{Name: "store", Value: []byte("false")})
			changed = true
		}
		for _, fieldName := range []string{"max_output_tokens", "temperature"} {
			if _, exists := fieldsByName[fieldName]; exists {
				edits[fieldName] = common.JSONFieldEdit{Delete: true}
				changed = true
			}
		}
	}
	if !changed {
		return storage, false, nil
	}

	reader, size, err := common.NewTopLevelJSONObjectReader(storage, fields, edits, additions)
	if err != nil {
		return nil, false, err
	}
	outboundStorage, err := common.CreateBodyStorageFromReader(reader, size, size)
	if err != nil {
		return nil, false, err
	}
	return outboundStorage, true, nil
}

func shouldUseCodexOriginalResponsesBody(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ApiType != appconstant.APITypeCodex || info.ChannelType != appconstant.ChannelTypeCodex {
		return false
	}
	return info.RelayMode == relayconstant.RelayModeResponses ||
		info.RelayMode == relayconstant.RelayModeResponsesCompact
}

func sanitizeCodexOriginalResponsesBody(body []byte, compact bool, mappedModel string) ([]byte, bool, error) {
	var err error
	changed := false
	if mappedModel != "" && gjson.GetBytes(body, "model").String() != mappedModel {
		body, err = sjson.SetBytes(body, "model", mappedModel)
		if err != nil {
			return nil, false, fmt.Errorf("set mapped Codex model: %w", err)
		}
		changed = true
	}
	if !gjson.GetBytes(body, "instructions").Exists() {
		body, err = sjson.SetBytes(body, "instructions", "")
		if err != nil {
			return nil, false, fmt.Errorf("set Codex instructions: %w", err)
		}
		changed = true
	}
	if compact {
		return body, changed, nil
	}

	store := gjson.GetBytes(body, "store")
	if !store.Exists() || store.Type != gjson.False {
		body, err = sjson.SetBytes(body, "store", false)
		if err != nil {
			return nil, false, fmt.Errorf("set Codex store: %w", err)
		}
		changed = true
	}
	for _, field := range []string{"max_output_tokens", "temperature"} {
		if !gjson.GetBytes(body, field).Exists() {
			continue
		}
		body, err = sjson.DeleteBytes(body, field)
		if err != nil {
			return nil, false, fmt.Errorf("remove unsupported Codex field %q: %w", field, err)
		}
		changed = true
	}
	return body, changed, nil
}
