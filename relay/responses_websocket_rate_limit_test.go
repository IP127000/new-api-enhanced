package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

var responsesWebSocketRateLimitUserSequence atomic.Int64

func nextResponsesWebSocketRateLimitUserID() int {
	return 940000 + int(responsesWebSocketRateLimitUserSequence.Add(1))
}

func configureResponsesWebSocketMemoryRateLimit(t *testing.T, total, success int) {
	t.Helper()

	setting.ModelRequestRateLimitMutex.Lock()
	originalEnabled := setting.ModelRequestRateLimitEnabled
	originalDuration := setting.ModelRequestRateLimitDurationMinutes
	originalTotal := setting.ModelRequestRateLimitCount
	originalSuccess := setting.ModelRequestRateLimitSuccessCount
	originalGroups := setting.ModelRequestRateLimitGroup
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	setting.ModelRequestRateLimitCount = total
	setting.ModelRequestRateLimitSuccessCount = success
	setting.ModelRequestRateLimitGroup = map[string][2]int{}
	setting.ModelRequestRateLimitMutex.Unlock()
	originalRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false

	t.Cleanup(func() {
		setting.ModelRequestRateLimitMutex.Lock()
		setting.ModelRequestRateLimitEnabled = originalEnabled
		setting.ModelRequestRateLimitDurationMinutes = originalDuration
		setting.ModelRequestRateLimitCount = originalTotal
		setting.ModelRequestRateLimitSuccessCount = originalSuccess
		setting.ModelRequestRateLimitGroup = originalGroups
		setting.ModelRequestRateLimitMutex.Unlock()
		common.RedisEnabled = originalRedisEnabled
	})
}

func startResponsesWebSocketRateLimitSession(
	t *testing.T,
	runtime responsesWebSocketRuntime,
	userID int,
	upstreamHandler func(*websocket.Conn, *responsesWebSocketCapture) error,
) (*websocket.Conn, *responsesWebSocketCapture, <-chan *types.NewAPIError, <-chan error) {
	t.Helper()

	capture := &responsesWebSocketCapture{}
	upstreamDone := make(chan error, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			upstreamDone <- err
			return
		}
		defer conn.Close()
		capture.mu.Lock()
		capture.upstreamConnections++
		capture.mu.Unlock()
		upstreamDone <- upstreamHandler(conn, capture)
	}))
	t.Cleanup(upstreamServer.Close)
	upstreamWSURL := "ws" + strings.TrimPrefix(upstreamServer.URL, "http")
	runtime.dial = func(
		ctx context.Context,
		_ string,
		header http.Header,
		_ *relaycommon.RelayInfo,
	) (*websocket.Conn, *http.Response, error) {
		return websocket.DefaultDialer.DialContext(ctx, upstreamWSURL, header)
	}

	relayDone := make(chan *types.NewAPIError, 1)
	downstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			relayDone <- types.NewError(err, types.ErrorCodeInvalidRequest)
			return
		}
		defer client.Close()
		frame, frameErr := ReadResponsesWebSocketFrame(client, 1<<20)
		if frameErr != nil {
			relayDone <- types.NewError(frameErr, types.ErrorCodeInvalidRequest)
			return
		}
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		setResponsesWebSocketTestContext(c, "")
		common.SetContextKey(c, constant.ContextKeyUserId, userID)
		c.Set("id", userID)
		attachResponsesWebSocketTestFrame(c, frame)
		relayDone <- responsesWebSocketHelperWithRuntime(c, client, frame, runtime)
	}))
	t.Cleanup(downstreamServer.Close)

	client, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(downstreamServer.URL, "http"),
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client, capture, relayDone, upstreamDone
}

func readResponsesWebSocketErrorEvent(t *testing.T, client *websocket.Conn) map[string]any {
	t.Helper()
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	messageType, data, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	var event map[string]any
	require.NoError(t, json.Unmarshal(data, &event))
	require.Equal(t, "error", event["type"])
	return event
}

func TestResponsesWebSocketRateLimitsEachCreateWithoutDoubleCountingHandshake(t *testing.T) {
	gin.SetMode(gin.TestMode)
	configureResponsesWebSocketMemoryRateLimit(t, 2, 100)

	runtime := defaultResponsesWebSocketRuntime()
	runtime.prepareAccounting = func(*gin.Context, *relaycommon.RelayInfo, dto.Request, bool) *types.NewAPIError {
		return nil
	}
	runtime.consumeUsage = func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) {}
	runtime.recordAffinity = func(*gin.Context, int) {}

	client, capture, relayDone, upstreamDone := startResponsesWebSocketRateLimitSession(
		t,
		runtime,
		nextResponsesWebSocketRateLimitUserID(),
		func(upstream *websocket.Conn, capture *responsesWebSocketCapture) error {
			for index, id := range []string{"warm-rate-1", "resp-rate-1"} {
				_, data, err := upstream.ReadMessage()
				if err != nil {
					return err
				}
				capture.recordUpstreamRequest(data)
				var usage *dto.ResponsesBillingUsage
				if index == 1 {
					usage = &dto.ResponsesBillingUsage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}
				}
				if err := writeResponsesWebSocketTestTurn(upstream, id, usage); err != nil {
					return err
				}
			}
			_, _, _ = upstream.ReadMessage()
			return nil
		},
	)

	warmup := []byte(`{"type":"response.create","model":"gpt-5.6-sol","input":[],"generate":false}`)
	require.NoError(t, client.WriteMessage(websocket.TextMessage, warmup))
	completed, err := readResponsesWebSocketTestCompleted(t, client)
	require.NoError(t, err)
	require.Equal(t, "warm-rate-1", completed["response"].(map[string]any)["id"])

	generated := []byte(`{"type":"response.create","model":"gpt-5.6-sol","input":[{"role":"user","content":"one"}]}`)
	require.NoError(t, client.WriteMessage(websocket.TextMessage, generated))
	completed, err = readResponsesWebSocketTestCompleted(t, client)
	require.NoError(t, err)
	require.Equal(t, "resp-rate-1", completed["response"].(map[string]any)["id"])

	require.NoError(t, client.WriteMessage(websocket.TextMessage, generated))
	errorEvent := readResponsesWebSocketErrorEvent(t, client)
	require.Equal(t, float64(http.StatusTooManyRequests), errorEvent["status"])
	errorBody := errorEvent["error"].(map[string]any)
	require.Equal(t, "rate_limit_error", errorBody["type"])
	require.Equal(t, "rate_limit_exceeded", errorBody["code"])

	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, closeErr := client.ReadMessage()
	var wsClose *websocket.CloseError
	require.ErrorAs(t, closeErr, &wsClose)
	require.Equal(t, websocket.CloseTryAgainLater, wsClose.Code)

	select {
	case relayErr := <-relayDone:
		require.NotNil(t, relayErr)
		require.Contains(t, relayErr.Error(), "总请求数限制")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for rate-limited relay to stop")
	}
	select {
	case err := <-upstreamDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for upstream websocket to stop")
	}
	require.Len(t, capture.snapshot().upstreamRequests, 2, "rejected create must not reach upstream")
}

func TestResponsesWebSocketRejectsGenerateFalseAfterFirstFrameBeforeRateLimitOrUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	runtime := defaultResponsesWebSocketRuntime()
	var limiterCalls atomic.Int32
	runtime.beginRateLimit = func(*gin.Context) (*middleware.ModelRequestRateLimitReservation, *middleware.ModelRequestRateLimitError) {
		limiterCalls.Add(1)
		return nil, nil
	}
	runtime.prepareAccounting = func(*gin.Context, *relaycommon.RelayInfo, dto.Request, bool) *types.NewAPIError {
		return nil
	}
	runtime.consumeUsage = func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) {}
	runtime.recordAffinity = func(*gin.Context, int) {}

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
			if err := writeResponsesWebSocketTestTurn(upstream, "resp-first", nil); err != nil {
				return err
			}
			_, _, _ = upstream.ReadMessage()
			return nil
		},
	)

	first := []byte(`{"type":"response.create","model":"gpt-5.6-sol","input":[{"role":"user","content":"one"}]}`)
	require.NoError(t, client.WriteMessage(websocket.TextMessage, first))
	_, err := readResponsesWebSocketTestCompleted(t, client)
	require.NoError(t, err)

	laterWarmup := []byte(`{"type":"response.create","model":"gpt-5.6-sol","input":[],"generate":false}`)
	require.NoError(t, client.WriteMessage(websocket.TextMessage, laterWarmup))
	errorEvent := readResponsesWebSocketErrorEvent(t, client)
	require.Equal(t, float64(http.StatusBadRequest), errorEvent["status"])
	require.Contains(t, errorEvent["error"].(map[string]any)["message"], "only allowed on the first")

	select {
	case relayErr := <-relayDone:
		require.NotNil(t, relayErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for invalid prewarm relay to stop")
	}
	select {
	case err := <-upstreamDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for upstream websocket to stop")
	}
	require.Equal(t, int32(1), limiterCalls.Load(), "rejected later prewarm must not reserve rate allowance")
	require.Len(t, capture.snapshot().upstreamRequests, 1, "rejected later prewarm must not reach upstream")
}
