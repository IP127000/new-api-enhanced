package relay

import (
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// RealtimeCallHelper forwards the WebRTC SDP/session bootstrap without
// interpreting or rebuilding its multipart body.
func RealtimeCallHelper(c *gin.Context) *types.NewAPIError {
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAIRealtime, nil, nil)
	if err != nil {
		return types.NewError(err, types.ErrorCodeGenRelayInfoFailed)
	}
	info.InitChannelMeta(c)
	info.RelayMode = relayconstant.RelayModeRealtimeCall
	if info.ApiType != appconstant.APITypeCodex || info.ChannelType != appconstant.ChannelTypeCodex {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("realtime calls require a Codex subscription channel"),
			types.ErrorCodeInvalidApiType,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	storage, err := common.GetBodyStorage(c)
	if err != nil {
		status := http.StatusBadRequest
		if common.IsRequestBodyTooLargeError(err) {
			status = http.StatusRequestEntityTooLarge
		}
		return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, status, types.ErrOptionWithSkipRetry())
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	result, err := adaptor.DoRequest(c, info, storage)
	if err != nil {
		return types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	resp, ok := result.(*http.Response)
	if !ok || resp == nil {
		return types.NewError(fmt.Errorf("realtime call upstream returned an invalid response"), types.ErrorCodeDoRequestFailed)
	}
	defer service.CloseResponseBodyGracefully(resp)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 && resp.Header.Get("Location") != "" {
		var credential struct {
			AccountID string `json:"account_id"`
		}
		_ = common.UnmarshalJsonStr(info.ApiKey, &credential)
		if err := service.RememberRealtimeCall(c, resp.Header.Get("Location"), service.RealtimeCallBinding{
			ChannelID: info.ChannelId, Model: info.OriginModelName, KeyIndex: info.ChannelMultiKeyIndex, AccountID: credential.AccountID,
		}); err != nil {
			return types.NewError(err, types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
		}
	}

	for key, values := range resp.Header {
		if !service.ShouldCopyUpstreamHeader(c, key, values) {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}
	c.Status(resp.StatusCode)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		logger.LogError(c, fmt.Sprintf("failed to copy realtime call response: %s", err.Error()))
	}
	return nil
}
