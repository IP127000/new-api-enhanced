package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type responsesWebSocketCapture struct {
	mu                   sync.Mutex
	requestedURL         string
	requestHeader        http.Header
	upstreamConnections  int
	upstreamRequests     [][]byte
	accountingRequests   int
	consumedUsage        []dto.Usage
	firstResponseSeen    []bool
	receivedAtSettlement []int
	affinityRecords      int
}

type responsesWebSocketSnapshot struct {
	requestedURL         string
	requestHeader        http.Header
	upstreamConnections  int
	upstreamRequests     [][]byte
	accountingRequests   int
	consumedUsage        []dto.Usage
	firstResponseSeen    []bool
	receivedAtSettlement []int
	affinityRecords      int
}

func (c *responsesWebSocketCapture) recordUpstreamRequest(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.upstreamRequests = append(c.upstreamRequests, append([]byte(nil), data...))
}

func (c *responsesWebSocketCapture) snapshot() responsesWebSocketSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return responsesWebSocketSnapshot{
		requestedURL:         c.requestedURL,
		requestHeader:        c.requestHeader.Clone(),
		upstreamConnections:  c.upstreamConnections,
		upstreamRequests:     append([][]byte(nil), c.upstreamRequests...),
		accountingRequests:   c.accountingRequests,
		consumedUsage:        append([]dto.Usage(nil), c.consumedUsage...),
		firstResponseSeen:    append([]bool(nil), c.firstResponseSeen...),
		receivedAtSettlement: append([]int(nil), c.receivedAtSettlement...),
		affinityRecords:      c.affinityRecords,
	}
}

func setResponsesWebSocketTestContext(c *gin.Context, modelMapping string) {
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-5.6-sol")
	common.SetContextKey(c, constant.ContextKeyChannelId, 11)
	common.SetContextKey(c, constant.ContextKeyChannelName, "plus-han-test")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeCodex)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://chatgpt.com")
	common.SetContextKey(c, constant.ContextKeyChannelKey, `{"access_token":"server-access-token","account_id":"server-account"}`)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{})
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{})
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]interface{}{})
	common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, map[string]interface{}{})
	common.SetContextKey(c, constant.ContextKeyTokenId, 1)
	common.SetContextKey(c, constant.ContextKeyTokenKey, "downstream-new-api-token")
	common.SetContextKey(c, constant.ContextKeyTokenUnlimited, true)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserId, 1)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
	c.Set("model_mapping", modelMapping)
}

func attachResponsesWebSocketTestFrame(c *gin.Context, frame *ResponsesWebSocketFrame) {
	c.Set(common.KeyBodyStorage, frame.Storage)
	c.Set(common.KeyBodyStorageReleased, false)
	c.Set(common.KeyRequestBody, nil)
	common.SetContextKey(c, constant.ContextKeyJSONBodyTopLevelFields, nil)
	c.Request.Body = io.NopCloser(frame.Storage)
	c.Request.ContentLength = frame.Storage.Size()
	c.Request.Header.Set("Content-Type", "application/json")
}

func writeResponsesWebSocketTestTurn(conn *websocket.Conn, id string, usage *dto.ResponsesBillingUsage) error {
	metadata, _ := common.Marshal(map[string]any{
		"type": "response.metadata",
		"headers": map[string]any{
			"x-codex-turn-state": "turn-state-" + id,
		},
	})
	if err := conn.WriteMessage(websocket.TextMessage, metadata); err != nil {
		return err
	}
	created, _ := common.Marshal(map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": id,
		},
	})
	if err := conn.WriteMessage(websocket.TextMessage, created); err != nil {
		return err
	}
	itemDone := []byte(`{"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}`)
	if err := conn.WriteMessage(websocket.TextMessage, itemDone); err != nil {
		return err
	}
	completed, _ := common.Marshal(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":    id,
			"usage": usage,
			// This field must not be retained in the compact downstream terminal.
			"instructions": strings.Repeat("large", 1024),
		},
	})
	return conn.WriteMessage(websocket.TextMessage, completed)
}

func readResponsesWebSocketTestCompleted(t *testing.T, conn *websocket.Conn) (map[string]any, error) {
	t.Helper()
	sawMetadata := false
	sawOutputItemDone := false
	for {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		var event map[string]any
		require.NoError(t, json.Unmarshal(data, &event))
		switch event["type"] {
		case "response.metadata":
			sawMetadata = true
		case dto.ResponsesOutputTypeItemDone:
			sawOutputItemDone = true
		case "response.completed":
			require.True(t, sawMetadata, "response.metadata must be forwarded for turn-state replay")
			require.True(t, sawOutputItemDone, "response.output_item.done must be forwarded for incremental history matching")
			return event, nil
		}
	}
}

func TestResponsesWebSocketPersistentConversationUsesServerCredentialsAndBillsPerTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalSelfUseMode := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() { operation_setting.SelfUseModeEnabled = originalSelfUseMode })

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
		capture.requestHeader = r.Header.Clone()
		capture.mu.Unlock()

		usages := []*dto.ResponsesBillingUsage{
			nil,
			{
				InputTokens:  12,
				OutputTokens: 3,
				TotalTokens:  15,
				InputTokensDetails: &dto.InputTokenDetails{
					CachedTokens:     7,
					CacheWriteTokens: 2,
				},
			},
			{InputTokens: 4, OutputTokens: 2, TotalTokens: 6},
		}
		ids := []string{"warm-1", "resp-1", "resp-2"}
		for index := range ids {
			_, data, readErr := conn.ReadMessage()
			if readErr != nil {
				upstreamDone <- readErr
				return
			}
			capture.recordUpstreamRequest(data)
			if writeErr := writeResponsesWebSocketTestTurn(conn, ids[index], usages[index]); writeErr != nil {
				upstreamDone <- writeErr
				return
			}
		}
		_, _, err = conn.ReadMessage()
		if closeErr, ok := err.(*websocket.CloseError); ok && closeErr.Code == websocket.CloseNormalClosure {
			upstreamDone <- nil
			return
		}
		upstreamDone <- err
	}))
	defer upstreamServer.Close()
	upstreamWSURL := "ws" + strings.TrimPrefix(upstreamServer.URL, "http")

	runtime := defaultResponsesWebSocketRuntime()
	runtime.dial = func(ctx context.Context, requestURL string, header http.Header, _ *relaycommon.RelayInfo) (*websocket.Conn, *http.Response, error) {
		capture.mu.Lock()
		capture.requestedURL = requestURL
		capture.mu.Unlock()
		return websocket.DefaultDialer.DialContext(ctx, upstreamWSURL, header)
	}
	runtime.prepareAccounting = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ dto.Request, useUpstreamUsage bool) *types.NewAPIError {
		require.True(t, useUpstreamUsage)
		capture.mu.Lock()
		capture.accountingRequests++
		capture.mu.Unlock()
		return nil
	}
	runtime.consumeUsage = func(_ *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage, _ []string) {
		capture.mu.Lock()
		capture.consumedUsage = append(capture.consumedUsage, *usage)
		capture.firstResponseSeen = append(capture.firstResponseSeen, info.HasSendResponse())
		capture.receivedAtSettlement = append(capture.receivedAtSettlement, info.ReceivedResponseCount)
		capture.mu.Unlock()
	}
	runtime.recordAffinity = func(_ *gin.Context, _ int) {
		capture.mu.Lock()
		capture.affinityRecords++
		capture.mu.Unlock()
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
		attachResponsesWebSocketTestFrame(c, frame)
		relayErr := responsesWebSocketHelperWithRuntime(c, client, frame, runtime)
		if relayErr != nil {
			t.Logf("responses websocket relay stopped with error: %v", relayErr)
		}
		relayDone <- relayErr
	}))
	defer downstreamServer.Close()
	downstreamWSURL := "ws" + strings.TrimPrefix(downstreamServer.URL, "http")

	downstreamHeader := http.Header{}
	downstreamHeader.Set("Authorization", "Bearer downstream-token-must-not-leak")
	downstreamHeader.Set("OpenAI-Beta", "attacker-value")
	downstreamHeader.Set("X-OpenAI-Actor-Authorization", "attacker-value")
	downstreamHeader.Set("Originator", "codex_cli_rs")
	downstreamHeader.Set("Session-Id", "session-1")
	downstreamHeader.Set("Thread-Id", "thread-1")
	downstreamHeader.Set("X-Client-Request-Id", "request-1")
	downstreamHeader.Set("Version", "0.144.4")
	client, _, err := websocket.DefaultDialer.Dial(downstreamWSURL, downstreamHeader)
	require.NoError(t, err)
	defer client.Close()

	requests := [][]byte{
		[]byte(`{"type":"response.create","model":"gpt-5.6-sol","instructions":"","input":[{"role":"user","content":"hello"}],"tools":[],"store":false,"stream":true,"generate":false,"future_field":{"keep":true}}`),
		[]byte(`{"type":"response.create","model":"gpt-5.6-sol","instructions":"","previous_response_id":"warm-1","input":[],"tools":[],"store":false,"stream":true,"future_field":{"keep":true}}`),
		[]byte(`{"type":"response.create","model":"gpt-5.6-sol","instructions":"","previous_response_id":"resp-1","input":[{"type":"function_call_output","call_id":"call-1","output":"delta-only"}],"tools":[],"store":false,"stream":true,"future_field":{"keep":true}}`),
	}
	for index, request := range requests {
		require.NoError(t, client.WriteMessage(websocket.TextMessage, request))
		completed, readErr := readResponsesWebSocketTestCompleted(t, client)
		if readErr != nil {
			snapshot := capture.snapshot()
			select {
			case relayErr := <-relayDone:
				t.Fatalf("downstream read failed: %v; relay stopped with: %v; upstream requests=%d accounting=%d usages=%d", readErr, relayErr, len(snapshot.upstreamRequests), snapshot.accountingRequests, len(snapshot.consumedUsage))
			case upstreamErr := <-upstreamDone:
				t.Fatalf("downstream read failed: %v; upstream stopped with: %v", readErr, upstreamErr)
			case <-time.After(time.Second):
				t.Fatalf("downstream read failed without relay diagnostic: %v", readErr)
			}
		}
		response := completed["response"].(map[string]any)
		require.Equal(t, []string{"warm-1", "resp-1", "resp-2"}[index], response["id"])
		require.NotContains(t, response, "instructions")
	}

	require.NoError(t, client.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"),
		time.Now().Add(time.Second),
	))
	select {
	case relayErr := <-relayDone:
		require.Nil(t, relayErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for responses websocket relay to stop")
	}
	select {
	case upstreamErr := <-upstreamDone:
		require.NoError(t, upstreamErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for upstream websocket to stop")
	}

	snapshot := capture.snapshot()
	require.Equal(t, "wss://chatgpt.com/backend-api/codex/responses", snapshot.requestedURL)
	require.Equal(t, 1, snapshot.upstreamConnections)
	require.Equal(t, "Bearer server-access-token", snapshot.requestHeader.Get("Authorization"))
	require.Equal(t, "server-account", snapshot.requestHeader.Get("chatgpt-account-id"))
	require.Equal(t, responsesWebSocketBetaHeader, snapshot.requestHeader.Get("OpenAI-Beta"))
	require.Equal(t, "codex_cli_rs", snapshot.requestHeader.Get("Originator"))
	require.Equal(t, "session-1", snapshot.requestHeader.Get("Session-Id"))
	require.Equal(t, "thread-1", snapshot.requestHeader.Get("Thread-Id"))
	require.Equal(t, "request-1", snapshot.requestHeader.Get("X-Client-Request-Id"))
	require.Equal(t, "0.144.4", snapshot.requestHeader.Get("Version"))
	require.Empty(t, snapshot.requestHeader.Get("X-OpenAI-Actor-Authorization"))
	require.NotContains(t, snapshot.requestHeader.Get("Authorization"), "downstream-token")
	require.Len(t, snapshot.upstreamRequests, 3)
	for index := range requests {
		require.JSONEq(t, string(requests[index]), string(snapshot.upstreamRequests[index]))
	}
	require.Equal(t, 3, snapshot.accountingRequests, "generate=false warmup must still run policy and price initialization")
	require.Len(t, snapshot.consumedUsage, 2, "generate=false warmup must not consume quota")
	require.Equal(t, dto.Usage{
		PromptTokens:     12,
		CompletionTokens: 3,
		TotalTokens:      15,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:     7,
			CacheWriteTokens: 2,
		},
	}, snapshot.consumedUsage[0])
	require.Equal(t, []bool{true, true}, snapshot.firstResponseSeen, "each generated round must record TTFT")
	require.Equal(t, []int{4, 4}, snapshot.receivedAtSettlement)
	require.Equal(t, 1, snapshot.affinityRecords)
}

func TestPrepareResponsesWebSocketRoundMapsOnlyModel(t *testing.T) {
	originalSelfUseMode := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() { operation_setting.SelfUseModeEnabled = originalSelfUseMode })

	body := []byte(`{"type":"response.create","model":"client-model","instructions":"","previous_response_id":"resp-1","input":[{"role":"user","content":"new-only"}],"tools":[],"store":false,"stream":true,"client_metadata":{"x-codex-turn-state":"state-1"},"future_field":{"keep":true}}`)
	storage, err := common.CreateBodyStorage(body)
	require.NoError(t, err)
	frame := &ResponsesWebSocketFrame{Storage: storage, Type: "response.create", Model: "client-model", Generate: true}
	t.Cleanup(func() { _ = frame.Close() })

	request := httptest.NewRequest(http.MethodGet, "/v1/responses", http.NoBody)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = request
	setResponsesWebSocketTestContext(c, `{"client-model":"gpt-5.6-sol"}`)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "client-model")

	runtime := defaultResponsesWebSocketRuntime()
	runtime.prepareAccounting = func(*gin.Context, *relaycommon.RelayInfo, dto.Request, bool) *types.NewAPIError { return nil }
	runtime.consumeUsage = func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) {}
	runtime.recordAffinity = func(*gin.Context, int) {}
	round, wsErr := prepareResponsesWebSocketRound(c, frame, "client-model", runtime)
	require.Nil(t, wsErr)
	require.NotNil(t, round)
	t.Cleanup(round.releaseBody)

	reader, err := round.outboundStorage.NewReader()
	require.NoError(t, err)
	outbound, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	var got map[string]any
	var original map[string]any
	require.NoError(t, json.Unmarshal(outbound, &got))
	require.NoError(t, json.Unmarshal(body, &original))
	require.Equal(t, "gpt-5.6-sol", got["model"])
	delete(got, "model")
	delete(original, "model")
	require.Equal(t, original, got)
}

func TestPrepareResponsesWebSocketRoundRejectsNonCodexChannel(t *testing.T) {
	body := []byte(`{"type":"response.create","model":"gpt-5.6-sol","instructions":"","input":[],"store":false,"stream":true}`)
	storage, err := common.CreateBodyStorage(body)
	require.NoError(t, err)
	frame := &ResponsesWebSocketFrame{Storage: storage, Type: "response.create", Model: "gpt-5.6-sol", Generate: true}
	t.Cleanup(func() { _ = frame.Close() })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", http.NoBody)
	setResponsesWebSocketTestContext(c, "")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)

	runtime := defaultResponsesWebSocketRuntime()
	runtime.prepareAccounting = func(*gin.Context, *relaycommon.RelayInfo, dto.Request, bool) *types.NewAPIError {
		t.Fatal("accounting must not run for a non-Codex channel")
		return nil
	}
	round, wsErr := prepareResponsesWebSocketRound(c, frame, "gpt-5.6-sol", runtime)
	require.Nil(t, round)
	require.NotNil(t, wsErr)
	require.Equal(t, http.StatusBadRequest, wsErr.StatusCode)
	require.Contains(t, wsErr.Message, "Codex subscription channel")
}

func TestReadResponsesWebSocketFrameRejectsOversizedMessage(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	result := make(chan *ResponsesWebSocketError, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			result <- newResponsesWebSocketError(500, websocket.CloseInternalServerErr, "", "", err)
			return
		}
		defer conn.Close()
		_, wsErr := ReadResponsesWebSocketFrame(conn, 128)
		result <- wsErr
	}))
	defer server.Close()

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer client.Close()
	payload := fmt.Sprintf(`{"type":"response.create","model":"gpt-5.6-sol","input":"%s"}`, strings.Repeat("x", 256))
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(payload)))
	select {
	case wsErr := <-result:
		require.NotNil(t, wsErr)
		require.Equal(t, websocket.CloseMessageTooBig, wsErr.CloseCode)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for oversized frame rejection")
	}
}

func TestReadResponsesWebSocketFrameRejectsFragmentedOversizedMessage(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	result := make(chan *ResponsesWebSocketError, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			result <- newResponsesWebSocketError(500, websocket.CloseInternalServerErr, "", "", err)
			return
		}
		defer conn.Close()
		_, wsErr := ReadResponsesWebSocketFrame(conn, 128)
		result <- wsErr
	}))
	defer server.Close()

	dialer := websocket.Dialer{WriteBufferSize: 32}
	client, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer client.Close()

	payload := []byte(fmt.Sprintf(
		`{"type":"response.create","model":"gpt-5.6-sol","input":"%s"}`,
		strings.Repeat("x", 256),
	))
	writer, err := client.NextWriter(websocket.TextMessage)
	require.NoError(t, err)
	for len(payload) > 0 {
		chunkSize := 16
		if len(payload) < chunkSize {
			chunkSize = len(payload)
		}
		_, err = writer.Write(payload[:chunkSize])
		if err != nil {
			break
		}
		payload = payload[chunkSize:]
	}
	// The server may close as soon as the fragmented message crosses the read
	// limit, so a late client write or final flush can legitimately see EPIPE.
	_ = writer.Close()

	select {
	case wsErr := <-result:
		require.NotNil(t, wsErr)
		require.Equal(t, http.StatusRequestEntityTooLarge, wsErr.StatusCode)
		require.Equal(t, websocket.CloseMessageTooBig, wsErr.CloseCode)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for fragmented oversized frame rejection")
	}
}
