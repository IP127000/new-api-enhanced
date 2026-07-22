package relay

import (
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestSettleResponsesWebSocketWarmupIgnoresSetupUsage(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	round := &responsesWebSocketRound{
		ctx:      c,
		info:     &relaycommon.RelayInfo{},
		generate: false,
	}
	consumeCalls := 0
	runtime := defaultResponsesWebSocketRuntime()
	runtime.consumeUsage = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage, _ []string) {
		consumeCalls++
	}

	settleResponsesWebSocketRound(round, &dto.ResponsesBillingStreamResponse{
		Type: "response.completed",
		Response: &dto.ResponsesBillingResponse{
			Usage: &dto.ResponsesBillingUsage{
				InputTokens:  9,
				OutputTokens: 1,
				TotalTokens:  10,
			},
		},
	}, runtime)

	require.True(t, round.settled)
	require.Zero(t, consumeCalls)
}

func TestResponsesWebSocketConcurrentCreateSettlesCommittedRound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	runtime := defaultResponsesWebSocketRuntime()
	runtime.beginRateLimit = func(*gin.Context) (*middleware.ModelRequestRateLimitReservation, *middleware.ModelRequestRateLimitError) {
		return nil, nil
	}
	runtime.prepareAccounting = func(*gin.Context, *relaycommon.RelayInfo, dto.Request, bool) *types.NewAPIError {
		return nil
	}
	usageSettled := make(chan dto.Usage, 1)
	runtime.consumeUsage = func(_ *gin.Context, _ *relaycommon.RelayInfo, usage *dto.Usage, _ []string) {
		usageSettled <- *usage
	}
	runtime.recordAffinity = func(*gin.Context, int) {}
	runtime.terminalGracePeriod = 2 * time.Second

	allowTerminal := make(chan struct{})
	client, capture, relayDone, upstreamDone := startResponsesWebSocketRateLimitSession(
		t,
		runtime,
		nextResponsesWebSocketRateLimitUserID(),
		func(upstream *websocket.Conn, capture *responsesWebSocketCapture) error {
			_, data, err := upstream.ReadMessage()
			if err != nil {
				return err
			}
			capture.recordUpstreamRequest(data)
			<-allowTerminal
			return writeResponsesWebSocketTestTurn(upstream, "resp-committed", &dto.ResponsesBillingUsage{
				InputTokens:  7,
				OutputTokens: 2,
				TotalTokens:  9,
			})
		},
	)

	first := []byte(`{"type":"response.create","model":"gpt-5.6-sol","input":[{"role":"user","content":"one"}]}`)
	second := []byte(`{"type":"response.create","model":"gpt-5.6-sol","input":[{"role":"user","content":"two"}]}`)
	require.NoError(t, client.WriteMessage(websocket.TextMessage, first))
	require.NoError(t, client.WriteMessage(websocket.TextMessage, second))

	errorEvent := readResponsesWebSocketErrorEvent(t, client)
	require.Equal(t, float64(http.StatusConflict), errorEvent["status"])
	require.Contains(t, errorEvent["error"].(map[string]any)["message"], "already active")
	close(allowTerminal)

	select {
	case usage := <-usageSettled:
		require.Equal(t, dto.Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}, usage)
	case <-time.After(5 * time.Second):
		t.Fatal("committed round was not settled after downstream protocol violation")
	}
	select {
	case relayErr := <-relayDone:
		require.NotNil(t, relayErr)
		require.Contains(t, relayErr.Error(), "already active")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for relay shutdown")
	}
	select {
	case err := <-upstreamDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for upstream shutdown")
	}
	require.Len(t, capture.snapshot().upstreamRequests, 1, "conflicting create must not reach upstream")
}
