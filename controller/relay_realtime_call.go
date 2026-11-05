package controller

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay"

	"github.com/gin-gonic/gin"
)

// RelayRealtimeCall creates the HTTP half of a Realtime WebRTC session. The
// media peer connection is owned by the client; the associated sideband control
// connection later joins through GET /v1/realtime?call_id=....
func RelayRealtimeCall(c *gin.Context) {
	requestID := c.GetString(common.RequestIdKey)
	if relayErr := relay.RealtimeCallHelper(c); relayErr != nil {
		logger.LogError(c, fmt.Sprintf("realtime call relay error: %s", common.LocalLogPreview(relayErr.Error())))
		relayErr.SetMessage(common.MessageWithRequestId(relayErr.Error(), requestID))
		c.JSON(relayErr.StatusCode, gin.H{"error": relayErr.ToOpenAIError()})
	}
}
