package controller

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var responsesWebSocketUpgrader = websocket.Upgrader{
	ReadBufferSize:    32 << 10,
	WriteBufferSize:   32 << 10,
	EnableCompression: true,
	CheckOrigin: func(_ *http.Request) bool {
		return true
	},
}

// RelayResponsesWebSocket upgrades GET /v1/responses before reading the first
// response.create frame. Unlike Realtime, Responses has no model query
// parameter and no application subprotocol; channel selection therefore begins
// only after the first text frame is available.
func RelayResponsesWebSocket(c *gin.Context) {
	if !websocket.IsWebSocketUpgrade(c.Request) {
		c.Header("Upgrade", "websocket")
		c.JSON(http.StatusUpgradeRequired, gin.H{
			"error": gin.H{
				"message": "GET /v1/responses requires a WebSocket upgrade",
				"type":    "invalid_request_error",
				"code":    types.ErrorCodeInvalidRequest,
			},
		})
		return
	}

	client, err := responsesWebSocketUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("responses websocket upgrade failed: %s", common.LocalLogPreview(err.Error())))
		return
	}
	defer client.Close()

	// Bound clients that upgrade but never send the routing frame. Normal
	// connection reads are not given a wall-clock lifetime; the upstream/client
	// ping and close handlers remain in control after the first frame.
	_ = client.SetReadDeadline(time.Now().Add(30 * time.Second))
	firstFrame, wsErr := relay.ReadResponsesWebSocketFrame(client, 0)
	_ = client.SetReadDeadline(time.Time{})
	if wsErr != nil {
		wsErr.Message = common.MessageWithRequestId(wsErr.Message, c.GetString(common.RequestIdKey))
		relay.WriteResponsesWebSocketError(client, wsErr)
		logger.LogError(c, fmt.Sprintf("invalid responses websocket first frame: %s", common.LocalLogPreview(wsErr.Error())))
		return
	}

	// Make the first frame available to the shared selector so prompt_cache_key
	// affinity rules retain the same behavior as POST /v1/responses.
	c.Set(common.KeyBodyStorage, firstFrame.Storage)
	c.Set(common.KeyBodyStorageReleased, false)
	c.Set(common.KeyRequestBody, nil)
	common.SetContextKey(c, constant.ContextKeyJSONBodyTopLevelFields, nil)
	c.Request.Body = io.NopCloser(firstFrame.Storage)
	c.Request.ContentLength = firstFrame.Storage.Size()
	c.Request.Header.Set("Content-Type", "application/json")

	_, selectionErr := middleware.SelectAndSetupChannel(c, middleware.ChannelSelectionOptions{
		Model:               firstFrame.Model,
		RequestPath:         c.Request.URL.Path,
		ShouldSelectChannel: true,
		RequiredChannelType: constant.ChannelTypeCodex,
	})
	if selectionErr != nil {
		_ = firstFrame.Close()
		wsErr = &relay.ResponsesWebSocketError{
			StatusCode: selectionErr.StatusCode,
			CloseCode:  websocket.ClosePolicyViolation,
			Type:       "invalid_request_error",
			Code:       string(selectionErr.Code),
			Message: common.MessageWithRequestId(
				selectionErr.Message,
				c.GetString(common.RequestIdKey),
			),
			Err: selectionErr,
		}
		relay.WriteResponsesWebSocketError(client, wsErr)
		logger.LogError(c, fmt.Sprintf("responses websocket channel selection failed: %s", common.LocalLogPreview(selectionErr.Error())))
		return
	}

	if relayErr := relay.ResponsesWebSocketHelper(c, client, firstFrame); relayErr != nil {
		logger.LogError(c, fmt.Sprintf("responses websocket relay failed: %s", common.LocalLogPreview(relayErr.Error())))
	}
}
