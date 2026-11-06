package codex

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	appconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRequestURLAlphaSearch(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeCodex,
			ChannelBaseUrl: "https://chatgpt.com",
		},
		RelayMode: relayconstant.RelayModeAlphaSearch,
	}

	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://chatgpt.com/backend-api/codex/alpha/search", url)
}

// The Codex backend rejects these fields, so the adaptor clears them rather
// than forwarding what the client sent.
func TestConvertOpenAIResponsesRequestDropsPenalties(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeCodex},
		RelayMode:   relayconstant.RelayModeResponses,
	}

	converted, err := adaptor.ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{
		Model:            "gpt-5-codex",
		Input:            json.RawMessage(`"hello"`),
		MaxOutputTokens:  lo.ToPtr(uint(128)),
		Temperature:      lo.ToPtr(1.0),
		FrequencyPenalty: json.RawMessage(`1.5`),
		PresencePenalty:  json.RawMessage(`1.5`),
	})
	require.NoError(t, err)

	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Nil(t, request.MaxOutputTokens)
	assert.Nil(t, request.Temperature)
	assert.Nil(t, request.FrequencyPenalty)
	assert.Nil(t, request.PresencePenalty)
}

func TestCodexRequestPreservesBodyTraceHeadersAndServerAuth(t *testing.T) {
	service.InitHttpClient()

	var capturedBody string
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = string(body)
		capturedHeaders = r.Header.Clone()
		require.Equal(t, "/backend-api/codex/responses", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	originalBody := `{"model":"gpt-5","store":false,"prompt_cache_key":"cache-key","input":"hello"}`
	incoming := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(originalBody))
	incoming.Header.Set("Content-Type", "application/json; charset=utf-8")
	incoming.Header.Set("Authorization", "Bearer client-token")
	incoming.Header.Set("Thread_id", "thread-underscore-123")
	incoming.Header.Set("X-OpenAI-Memgen-Request", "memgen-123")
	incoming.Header.Set("X-ResponsesAPI-Include-Timing-Metrics", "true")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = incoming

	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		RequestHeaders: map[string]string{
			"Authorization":                         "Bearer client-token",
			"Thread_id":                             "thread-underscore-123",
			"X-OpenAI-Memgen-Request":               "memgen-123",
			"X-ResponsesAPI-Include-Timing-Metrics": "true",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:        appconstant.APITypeCodex,
			ChannelType:    appconstant.ChannelTypeCodex,
			ChannelBaseUrl: server.URL,
			ApiKey:         `{"access_token":"server-token","account_id":"server-account"}`,
		},
	}

	require.NoError(t, relaycommon.ApplyCodexClientHeaderPassthroughWithRelayInfo(info))
	result, err := (&Adaptor{}).DoRequest(c, info, strings.NewReader(originalBody))
	require.NoError(t, err)
	resp, ok := result.(*http.Response)
	require.True(t, ok)
	require.NotNil(t, resp)
	require.NoError(t, resp.Body.Close())

	require.Equal(t, originalBody, capturedBody)
	require.Equal(t, "Bearer server-token", capturedHeaders.Get("Authorization"))
	require.Equal(t, "server-account", capturedHeaders.Get("chatgpt-account-id"))
	require.Equal(t, "thread-underscore-123", capturedHeaders.Get("Thread_id"))
	require.Equal(t, "memgen-123", capturedHeaders.Get("X-OpenAI-Memgen-Request"))
	require.Equal(t, "true", capturedHeaders.Get("X-ResponsesAPI-Include-Timing-Metrics"))
	require.Equal(t, "application/json", capturedHeaders.Get("Content-Type"))
}

func TestCodexRealtimeUsesSubscriptionAuthAndRealtimeHeaders(t *testing.T) {
	type capturedRequest struct {
		path    string
		query   string
		headers http.Header
	}
	captured := make(chan capturedRequest, 1)
	release := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer connection.Close()
		captured <- capturedRequest{
			path:    r.URL.Path,
			query:   r.URL.RawQuery,
			headers: r.Header.Clone(),
		}
		<-release
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	incoming := httptest.NewRequest(http.MethodGet,
		"/v1/live?model=gpt-live-1-codex&trace=voice-test", http.NoBody)
	incoming.Header.Set("Authorization", "Bearer downstream-token")
	incoming.Header.Set("x-openai-actor-authorization", "android-client")
	incoming.Header.Set("openai-alpha", "quicksilver=v2")
	incoming.Header.Set("x-session-id", "voice-session-123")
	incoming.Header.Set("session-id", "codex-session-123")
	incoming.Header.Set("thread-id", "codex-thread-123")
	incoming.Header.Set("originator", "codex_android")
	incoming.Header.Set("x-codex-turn-metadata", `{"thread_source":"android"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = incoming

	info := &relaycommon.RelayInfo{
		RelayMode:      relayconstant.RelayModeRealtime,
		RequestURLPath: incoming.URL.String(),
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           appconstant.APITypeCodex,
			ChannelType:       appconstant.ChannelTypeCodex,
			ChannelBaseUrl:    server.URL,
			ApiKey:            `{"access_token":"server-token","account_id":"server-account"}`,
			UpstreamModelName: "gpt-live-1-codex",
		},
	}

	result, err := (&Adaptor{}).DoRequest(c, info, nil)
	require.NoError(t, err)
	connection, ok := result.(*websocket.Conn)
	require.True(t, ok)
	t.Cleanup(func() { _ = connection.Close() })

	request := <-captured
	require.Equal(t, "/v1/live", request.path)
	require.Contains(t, request.query, "model=gpt-live-1-codex")
	require.Contains(t, request.query, "trace=voice-test")
	require.Equal(t, "Bearer server-token", request.headers.Get("Authorization"))
	require.Equal(t, "server-account", request.headers.Get("chatgpt-account-id"))
	require.Empty(t, request.headers.Get("x-openai-actor-authorization"))
	require.Equal(t, "quicksilver=v2", request.headers.Get("openai-alpha"))
	require.Equal(t, "voice-session-123", request.headers.Get("x-session-id"))
	require.Equal(t, "codex-session-123", request.headers.Get("session-id"))
	require.Equal(t, "codex-thread-123", request.headers.Get("thread-id"))
	require.Equal(t, "codex_android", request.headers.Get("originator"))
	require.Equal(t, `{"thread_source":"android"}`,
		request.headers.Get("x-codex-turn-metadata"))
}

func TestCodexRealtimeRequestURLUsesMappedModel(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode:      relayconstant.RelayModeRealtime,
		RequestURLPath: "/v1/realtime?intent=quicksilver&model=client-model",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       appconstant.ChannelTypeCodex,
			ChannelBaseUrl:    "https://chatgpt.com",
			UpstreamModelName: "gpt-realtime-1.5",
		},
	}

	requestURL, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	require.Equal(t,
		"wss://api.openai.com/v1/realtime?intent=quicksilver&model=gpt-realtime-1.5",
		requestURL)
}

func TestCodexRealtimeCallForwardsMultipartWithSubscriptionAuth(t *testing.T) {
	service.InitHttpClient()

	const body = "--voice-boundary\r\nContent-Disposition: form-data; name=\"sdp\"\r\n\r\nv=0\r\n--voice-boundary--\r\n"
	var capturedBody string
	var capturedHeaders http.Header
	var capturedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = string(payload)
		capturedHeaders = r.Header.Clone()
		capturedQuery = r.URL.RawQuery
		require.Equal(t, "/v1/realtime/calls", r.URL.Path)
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", "/v1/realtime/calls/rtc_test")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("v=answer\r\n"))
	}))
	t.Cleanup(server.Close)

	incoming := httptest.NewRequest(http.MethodPost,
		"/v1/realtime/calls?intent=quicksilver&architecture=avas", strings.NewReader(body))
	incoming.Header.Set("Content-Type", "multipart/form-data; boundary=voice-boundary")
	incoming.Header.Set("Authorization", "Bearer downstream-token")
	incoming.Header.Set("x-session-id", "voice-session-123")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = incoming

	info := &relaycommon.RelayInfo{
		RelayMode:      relayconstant.RelayModeRealtime,
		RequestURLPath: incoming.URL.String(),
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           appconstant.APITypeCodex,
			ChannelType:       appconstant.ChannelTypeCodex,
			ChannelBaseUrl:    server.URL,
			ApiKey:            `{"access_token":"server-token","account_id":"server-account"}`,
			UpstreamModelName: "gpt-realtime-1.5",
		},
	}

	result, err := (&Adaptor{}).DoRequest(c, info, strings.NewReader(body))
	require.NoError(t, err)
	resp, ok := result.(*http.Response)
	require.True(t, ok)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.Equal(t, "/v1/realtime/calls/rtc_test", resp.Header.Get("Location"))

	require.Equal(t, body, capturedBody)
	require.Equal(t, "architecture=avas&intent=quicksilver", capturedQuery)
	require.Equal(t, "Bearer server-token", capturedHeaders.Get("Authorization"))
	require.Equal(t, "server-account", capturedHeaders.Get("chatgpt-account-id"))
	require.Equal(t, "voice-session-123", capturedHeaders.Get("x-session-id"))
	require.Equal(t, "multipart/form-data; boundary=voice-boundary",
		capturedHeaders.Get("Content-Type"))
	require.Empty(t, capturedHeaders.Get("x-openai-actor-authorization"))
}

func TestCodexRealtimeCallURLUsesOpenAIDataPlaneWithoutModelQuery(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode:      relayconstant.RelayModeRealtime,
		RequestURLPath: "/v1/realtime/calls?intent=quicksilver&architecture=avas",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       appconstant.ChannelTypeCodex,
			ChannelBaseUrl:    "https://chatgpt.com/backend-api/codex",
			UpstreamModelName: "gpt-realtime-1.5",
		},
	}

	requestURL, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	require.Equal(t,
		"https://api.openai.com/v1/realtime/calls?architecture=avas&intent=quicksilver",
		requestURL)
}

func TestCodexSidebandURLPreservesCallIDWithoutModel(t *testing.T) {
	for _, path := range []string{"/v1/live/rtc_owned", "/v1/realtime?call_id=rtc_owned"} {
		info := &relaycommon.RelayInfo{
			RelayMode: relayconstant.RelayModeRealtime, RequestURLPath: path,
			ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://chatgpt.com", ChannelType: appconstant.ChannelTypeCodex, UpstreamModelName: "gpt-live-1-codex"},
		}
		got, err := (&Adaptor{}).GetRequestURL(info)
		require.NoError(t, err)
		require.Equal(t, "wss://api.openai.com"+path, got)
	}
}

func TestCodexRealtimeForwardsFramelessAudioEvents(t *testing.T) {
	const audioInput = `{"type":"input_audio.append","audio":"AQID","future":true}`
	const audioOutput = `{"type":"output_audio.delta","delta":"BAUG","future":true}`
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, body, err := conn.ReadMessage()
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, audioInput, string(body))
		assert.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(audioOutput)))
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	done := make(chan struct{})
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		client, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer client.Close()
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeRealtime, RequestURLPath: "/v1/live?model=gpt-live-1-codex", ClientWs: client, StartTime: time.Now(), ChannelMeta: &relaycommon.ChannelMeta{ApiType: appconstant.APITypeCodex, ChannelType: appconstant.ChannelTypeCodex, ChannelBaseUrl: upstream.URL, UpstreamModelName: "gpt-live-1-codex", ApiKey: `{"access_token":"server-token","account_id":"server-account"}`}}
		adaptor := &Adaptor{}
		result, err := adaptor.DoRequest(c, info, nil)
		if !assert.NoError(t, err) {
			return
		}
		info.TargetWs = result.(*websocket.Conn)
		defer info.TargetWs.Close()
		_, apiErr := adaptor.DoResponse(c, nil, info)
		assert.Nil(t, apiErr)
	}))
	defer bridge.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(bridge.URL, "http"), nil)
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(audioInput)))
	_, body, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, audioOutput, string(body))
	require.NoError(t, client.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("realtime bridge did not close")
	}
}
