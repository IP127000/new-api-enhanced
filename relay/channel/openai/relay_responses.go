package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const codexTerminalUsageGracePeriod = 2 * time.Second

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	if responsesResponse.HasImageGenerationCall() {
		c.Set("image_generation_call", true)
		c.Set("image_generation_call_quality", responsesResponse.GetQuality())
		c.Set("image_generation_call_size", responsesResponse.GetSize())
	}

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := dto.Usage{}
	if responsesResponse.Usage != nil {
		usage.PromptTokens = responsesResponse.Usage.InputTokens
		usage.CompletionTokens = responsesResponse.Usage.OutputTokens
		usage.TotalTokens = responsesResponse.Usage.TotalTokens
		if responsesResponse.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.CachedTokens = responsesResponse.Usage.InputTokensDetails.CachedTokens
			usage.PromptTokensDetails.CacheWriteTokens = responsesResponse.Usage.InputTokensDetails.CacheWriteTokens
		}
	}
	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil {
		return &usage, nil
	}
	// 解析 Tools 用量
	for _, tool := range responsesResponse.Tools {
		buildToolinfo, ok := info.ResponsesUsageInfo.BuiltInTools[common.Interface2String(tool["type"])]
		if !ok || buildToolinfo == nil {
			logger.LogError(c, fmt.Sprintf("BuiltInTools not found for tool type: %v", tool["type"]))
			continue
		}
		buildToolinfo.CallCount++
	}
	return &usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	isCodexResponsesStream := info != nil && info.ChannelMeta != nil &&
		info.RelayFormat == types.RelayFormatOpenAIResponses &&
		info.ApiType == constant.APITypeCodex &&
		info.ChannelType == constant.ChannelTypeCodex
	collectFallbackOutput := !(operation_setting.SelfUseModeEnabled &&
		info != nil && info.ChannelMeta != nil &&
		info.RelayFormat == types.RelayFormatOpenAIResponses &&
		info.ApiType == constant.APITypeCodex &&
		info.ChannelType == constant.ChannelTypeCodex)

	// Responses events can contain complete request/response snapshots. Use a
	// synchronous handoff only for this format so concurrent large-context
	// sessions cannot queue ten multi-MiB events each; other stream formats keep
	// StreamScannerHandler's established buffering behavior.
	scannerOptions := helper.StreamScannerOptions{DataBufferSize: 0, InlineDataHandler: true}
	if isCodexResponsesStream {
		// Codex multi-agent v2 can preempt a request after a completed reasoning
		// or commentary item when mailbox input arrives, before the immediately
		// following response.completed event. Keep the upstream alive very briefly
		// to capture authoritative usage without retaining a queue of large events.
		scannerOptions.ClientGoneGracePeriod = codexTerminalUsageGracePeriod
	}
	helper.StreamScannerHandlerBytesWithOptions(c, resp, info, scannerOptions, func(data []byte, sr *helper.StreamResult) {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesBillingStreamResponse
		if err := common.Unmarshal(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			sr.Error(err)
			return
		}
		completed := false
		switch streamResponse.Type {
		case "response.completed":
			if streamResponse.Response != nil {
				if streamResponse.Response.Usage != nil {
					if streamResponse.Response.Usage.InputTokens != 0 {
						usage.PromptTokens = streamResponse.Response.Usage.InputTokens
					}
					if streamResponse.Response.Usage.OutputTokens != 0 {
						usage.CompletionTokens = streamResponse.Response.Usage.OutputTokens
					}
					if streamResponse.Response.Usage.TotalTokens != 0 {
						usage.TotalTokens = streamResponse.Response.Usage.TotalTokens
					}
					if streamResponse.Response.Usage.InputTokensDetails != nil {
						usage.PromptTokensDetails.CachedTokens = streamResponse.Response.Usage.InputTokensDetails.CachedTokens
						usage.PromptTokensDetails.CacheWriteTokens = streamResponse.Response.Usage.InputTokensDetails.CacheWriteTokens
					}
				}
				if quality, size, ok := streamResponse.Response.ImageGenerationCall(); ok {
					c.Set("image_generation_call", true)
					c.Set("image_generation_call_quality", quality)
					c.Set("image_generation_call_size", size)
				}
			}
			completed = true
			logger.LogInfo(c, fmt.Sprintf("%s event=response.completed prompt_tokens=%d completion_tokens=%d total_tokens=%d cached_tokens=%d cache_write_tokens=%d", helper.StreamDiagnostic(c, info, "responses_terminal_seen"), usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens, usage.PromptTokensDetails.CachedTokens, usage.PromptTokensDetails.CacheWriteTokens))
		case "response.output_text.delta":
			// Self-use Codex Responses trusts terminal upstream usage and does not
			// retain the whole generated text solely for abnormal-stream fallback.
			if collectFallbackOutput {
				responseTextBuilder.WriteString(streamResponse.Delta)
			}
		case dto.ResponsesOutputTypeItemDone:
			// Codex can intentionally preempt the current stream when multi-agent
			// mailbox input arrives after a reasoning/commentary item. Treat the
			// resulting downstream cancellation like the established function-call
			// close path rather than a network failure.
			if streamResponse.Item != nil {
				if info != nil && info.StreamStatus != nil &&
					(streamResponse.Item.Type == "function_call" ||
						(isCodexResponsesStream && isCodexMailboxPreemptionPoint(streamResponse.Item))) {
					info.StreamStatus.MarkClientCloseExpected()
					logger.LogInfo(c, fmt.Sprintf("%s event=response.output_item.done item_type=%s item_role=%s item_phase=%s", helper.StreamDiagnostic(c, info, "responses_expected_close_marked"), streamResponse.Item.Type, streamResponse.Item.Role, streamResponse.Item.Phase))
				}
				switch streamResponse.Item.Type {
				case dto.BuildInCallWebSearchCall:
					if info != nil && info.ResponsesUsageInfo != nil && info.ResponsesUsageInfo.BuiltInTools != nil {
						for _, toolName := range []string{dto.BuildInToolWebSearch, dto.BuildInToolWebSearchPreview} {
							if webSearchTool, exists := info.ResponsesUsageInfo.BuiltInTools[toolName]; exists && webSearchTool != nil {
								webSearchTool.CallCount++
								break
							}
						}
					}
				}
			}
		case "response.function_call_arguments.done":
			if info != nil && info.StreamStatus != nil {
				info.StreamStatus.MarkClientCloseExpected()
			}
		}

		downstreamGone := c.Request != nil && c.Request.Context().Err() != nil
		if !downstreamGone {
			if err := sendResponsesStreamDataBytes(c, streamResponse.Type, data); err != nil {
				expectedCancelDuringWrite := isCodexResponsesStream && c.Request.Context().Err() != nil &&
					info.StreamStatus != nil && info.StreamStatus.IsClientCloseExpected()
				if expectedCancelDuringWrite {
					logger.LogInfo(c, fmt.Sprintf("%s event_type=%s downstream_write_error=%v", helper.StreamDiagnostic(c, info, "responses_expected_cancel_during_write"), streamResponse.Type, err))
					// The client cancelled while this preemption-point event was being
					// flushed. Let the scanner's bounded grace read only the terminal
					// usage event instead of treating the expected cancel as a write
					// failure and closing the upstream immediately.
				} else {
					logger.LogError(c, fmt.Sprintf("%s event_type=%s downstream_write_error=%v", helper.StreamDiagnostic(c, info, "responses_downstream_write_failed"), streamResponse.Type, err))
					// Usage from response.completed has already been captured above. A
					// failed downstream write means no consumer remains, so stop immediately
					// and let StreamScannerHandler close the upstream body.
					if info != nil && info.StreamStatus != nil {
						info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
					}
					sr.Stop(err)
					return
				}
			}
		}
		if completed {
			// response.completed is the semantic terminal event for Responses SSE.
			// Codex may close the downstream stream immediately after this event
			// while continuing the same session, so do not wait for a later EOF.
			sr.Done()
		}
	})

	if collectFallbackOutput && usage.CompletionTokens == 0 {
		// 计算输出文本的 token 数量
		tempStr := responseTextBuilder.String()
		if len(tempStr) > 0 {
			// 非正常结束，使用输出文本的 token 数量
			completionTokens := service.CountTextToken(tempStr, info.UpstreamModelName)
			usage.CompletionTokens = completionTokens
		}
	}

	if usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	return usage, nil
}

func isCodexMailboxPreemptionPoint(item *dto.ResponsesBillingItem) bool {
	if item == nil {
		return false
	}
	if item.Type == "reasoning" {
		return true
	}
	return item.Type == "message" && item.Role == "assistant" && item.Phase == "commentary"
}
