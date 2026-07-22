package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type failingResponsesWriter struct {
	gin.ResponseWriter
	err error
}

type flushErrorHTTPWriter struct {
	http.ResponseWriter
	err error
}

func (w *flushErrorHTTPWriter) FlushError() error {
	return w.err
}

type failingResponsesFlushWriter struct {
	gin.ResponseWriter
	underlying http.ResponseWriter
}

func (w *failingResponsesFlushWriter) Flush() {}

func (w *failingResponsesFlushWriter) Unwrap() http.ResponseWriter {
	return w.underlying
}

type cancelAfterSuccessfulFlushHTTPWriter struct {
	http.ResponseWriter
	cancel context.CancelFunc
}

func (w *cancelAfterSuccessfulFlushHTTPWriter) FlushError() error {
	w.cancel()
	// Give the stream's main context watcher enough time to observe the cancel
	// while the data handler still owns the write mutex. It must wait for the
	// handler to mark the successfully delivered terminal event done.
	time.Sleep(50 * time.Millisecond)
	return nil
}

func (w *failingResponsesWriter) Write(p []byte) (int, error) {
	if len(p) > 0 && p[0] == '{' {
		return 0, w.err
	}
	return w.ResponseWriter.Write(p)
}

func (w *failingResponsesWriter) WriteString(s string) (int, error) {
	if strings.HasPrefix(s, "{") {
		return 0, w.err
	}
	return w.ResponseWriter.WriteString(s)
}

func TestOaiResponsesStreamHandlerStopsOnResponseCompleted(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	c, recorder, resp, info := newResponsesChatTestContext(t, "", true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp.Body = pr

	type result struct {
		promptTokens     int
		completionTokens int
		totalTokens      int
		err              *types.NewAPIError
	}
	resultCh := make(chan result, 1)
	go func() {
		usage, err := OaiResponsesStreamHandler(c, info, resp)
		res := result{err: err}
		if usage != nil {
			res.promptTokens = usage.PromptTokens
			res.completionTokens = usage.CompletionTokens
			res.totalTokens = usage.TotalTokens
		}
		resultCh <- res
	}()

	_, writeErr := fmt.Fprint(pw, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}}\n")
	require.NoError(t, writeErr)

	var res result
	select {
	case res = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response.completed to stop the stream")
	}
	require.Nil(t, res.err)
	require.Equal(t, 2, res.promptTokens)
	require.Equal(t, 3, res.completionTokens)
	require.Equal(t, 5, res.totalTokens)

	require.NotNil(t, info.StreamStatus)
	require.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	require.False(t, info.StreamStatus.HasErrors())

	got := recorder.Body.String()
	require.Contains(t, got, `event: response.completed`)
}

func TestOaiResponsesStreamHandlerCompactsCodexCompletedEvent(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	largeMarker := strings.Repeat("ignored-output-", 1<<17)
	body := "data: {\"type\":\"response.completed\",\"headers\":{\"x-extra\":\"top\"}," +
		"\"response\":{\"id\":\"resp_compact\",\"end_turn\":true," +
		"\"headers\":{\"openai-model\":\"gpt-5.6-sol\"}," +
		"\"usage\":{\"input_tokens\":200000,\"output_tokens\":12,\"total_tokens\":200012," +
		"\"input_tokens_details\":{\"cached_tokens\":150000,\"cache_write_tokens\":1024}," +
		"\"output_tokens_details\":{\"reasoning_tokens\":7}}," +
		"\"output\":[{\"type\":\"message\",\"content\":\"" + largeMarker + "\"}]}}\n"

	c, recorder, resp, info := newResponsesChatTestContext(t, "", true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp.Body = io.NopCloser(strings.NewReader(body))
	info.ChannelMeta = &relaycommon.ChannelMeta{
		UpstreamModelName: "gpt-5.6-sol",
		ApiType:           constant.APITypeCodex,
		ChannelType:       constant.ChannelTypeCodex,
	}
	info.RelayFormat = types.RelayFormatOpenAIResponses

	usage, err := OaiResponsesStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 200000, usage.PromptTokens)
	require.Equal(t, 12, usage.CompletionTokens)

	wire := recorder.Body.String()
	require.Less(t, len(wire), 2048)
	require.NotContains(t, wire, "ignored-output-")
	require.Contains(t, wire, `"id":"resp_compact"`)
	require.Contains(t, wire, `"end_turn":true`)
	require.Contains(t, wire, `"openai-model":"gpt-5.6-sol"`)
	require.Contains(t, wire, `"cached_tokens":150000`)
	require.Contains(t, wire, `"cache_write_tokens":1024`)
	require.Contains(t, wire, `"reasoning_tokens":7`)
	require.True(t, strings.HasSuffix(wire, "\n\n"))
}

func TestOaiResponsesStreamHandlerKeepsNonCodexCompletedEventVerbatim(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := `data: {"type":"response.completed","response":{"id":"resp_raw","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5},"output":[{"type":"message","marker":"keep-me"}]}}` + "\n"
	c, recorder, resp, info := newResponsesChatTestContext(t, "", true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp.Body = io.NopCloser(strings.NewReader(body))

	_, err := OaiResponsesStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.Contains(t, recorder.Body.String(), `"marker":"keep-me"`)
}

func TestCompactCodexCompletedEventFallsBackWithoutResponseID(t *testing.T) {
	data, ok, err := compactCodexCompletedEvent(&dto.ResponsesBillingStreamResponse{
		Type:     "response.completed",
		Response: &dto.ResponsesBillingResponse{Usage: &dto.ResponsesBillingUsage{TotalTokens: 5}},
	})
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, data)
}

func TestResponsesBillingUsageToUsagePreservesTokenBreakdowns(t *testing.T) {
	usage := ResponsesBillingUsageToUsage(&dto.ResponsesBillingUsage{
		InputTokens:  20,
		OutputTokens: 8,
		TotalTokens:  28,
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens:         11,
			CachedCreationTokens: 3,
			CacheWriteTokens:     2,
			TextTokens:           17,
			ImageTokens:          3,
		},
		OutputTokensDetails: &dto.OutputTokenDetails{
			TextTokens:      5,
			ImageTokens:     1,
			ReasoningTokens: 2,
		},
	})

	require.Equal(t, 20, usage.PromptTokens)
	require.Equal(t, 8, usage.CompletionTokens)
	require.Equal(t, 28, usage.TotalTokens)
	require.Equal(t, 11, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 3, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 2, usage.PromptTokensDetails.CacheWriteTokens)
	require.Equal(t, 17, usage.PromptTokensDetails.TextTokens)
	require.Equal(t, 3, usage.PromptTokensDetails.ImageTokens)
	require.Equal(t, 5, usage.CompletionTokenDetails.TextTokens)
	require.Equal(t, 1, usage.CompletionTokenDetails.ImageTokens)
	require.Equal(t, 2, usage.CompletionTokenDetails.ReasoningTokens)
}

func TestOaiResponsesStreamHandlerTreatsFunctionCallClientCloseAsExpected(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	reqCtx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(reqCtx)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAI,
		DisablePing: true,
	}

	errCh := make(chan *types.NewAPIError, 1)
	go func() {
		_, err := OaiResponsesStreamHandler(c, info, resp)
		errCh <- err
	}()

	_, writeErr := fmt.Fprint(pw, "data: {\"type\":\"response.function_call_arguments.done\"}\n")
	require.NoError(t, writeErr)
	require.Eventually(t, func() bool {
		return info.StreamStatus != nil && info.StreamStatus.IsClientCloseExpected()
	}, 2*time.Second, 10*time.Millisecond)

	cancel()

	select {
	case err := <-errCh:
		require.Nil(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for expected function-call close")
	}

	require.NotNil(t, info.StreamStatus)
	require.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.StreamStatus.EndReason)
	require.True(t, info.StreamStatus.IsNormalEnd())
}

func TestOaiResponsesStreamHandlerCapturesUsageAfterCodexMailboxPreemption(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	reqCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(reqCtx)
	c.Writer = &cancelAfterWriter{
		ResponseWriter: c.Writer,
		needle:         `"type":"reasoning"`,
		cancel:         cancel,
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-test",
			ApiType:           constant.APITypeCodex,
			ChannelType:       constant.ChannelTypeCodex,
		},
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAIResponses,
		DisablePing: true,
	}

	type result struct {
		usage *dto.Usage
		err   *types.NewAPIError
	}
	resultCh := make(chan result, 1)
	go func() {
		usage, err := OaiResponsesStreamHandler(c, info, resp)
		resultCh <- result{usage: usage, err: err}
	}()

	_, writeErr := fmt.Fprint(pw, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\"}}\n")
	require.NoError(t, writeErr)
	require.Eventually(t, func() bool {
		return reqCtx.Err() != nil && info.StreamStatus != nil && info.StreamStatus.IsClientCloseExpected()
	}, 2*time.Second, 10*time.Millisecond)

	_, writeErr = fmt.Fprint(pw, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":200000,\"output_tokens\":12,\"total_tokens\":200012,\"input_tokens_details\":{\"cached_tokens\":150000}}}}\n")
	require.NoError(t, writeErr)

	select {
	case res := <-resultCh:
		require.Nil(t, res.err)
		require.NotNil(t, res.usage)
		require.Equal(t, 200000, res.usage.PromptTokens)
		require.Equal(t, 12, res.usage.CompletionTokens)
		require.Equal(t, 200012, res.usage.TotalTokens)
		require.Equal(t, 150000, res.usage.PromptTokensDetails.CachedTokens)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for terminal usage grace")
	}

	require.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.StreamStatus.EndReason)
	require.Contains(t, recorder.Body.String(), `"type":"reasoning"`)
	require.NotContains(t, recorder.Body.String(), `"type":"response.completed"`)
}

func TestIsCodexMailboxPreemptionPoint(t *testing.T) {
	tests := []struct {
		name string
		item *dto.ResponsesBillingItem
		want bool
	}{
		{name: "reasoning", item: &dto.ResponsesBillingItem{Type: "reasoning"}, want: true},
		{name: "assistant commentary", item: &dto.ResponsesBillingItem{Type: "message", Role: "assistant", Phase: "commentary"}, want: true},
		{name: "assistant final", item: &dto.ResponsesBillingItem{Type: "message", Role: "assistant", Phase: "final"}},
		{name: "custom tool", item: &dto.ResponsesBillingItem{Type: "custom_tool_call"}},
		{name: "nil", item: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isCodexMailboxPreemptionPoint(tt.item))
		})
	}
}

func TestOaiResponsesStreamHandlerWriteFailureClosesUpstream(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	upstreamBody := &blockingBody{
		chunk: []byte("data: {\"type\":\"response.completed\",\"response\":{" +
			"\"usage\":{\"input_tokens\":200000,\"output_tokens\":12,\"total_tokens\":200012," +
			"\"input_tokens_details\":{\"cached_tokens\":150000}}}}\n"),
		closed: make(chan struct{}),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	writeErr := errors.New("client connection is gone")
	c.Writer = &failingResponsesWriter{ResponseWriter: c.Writer, err: writeErr}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       upstreamBody,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAI,
		DisablePing: true,
	}

	done := make(chan struct{})
	var gotUsage *dto.Usage
	go func() {
		gotUsage, _ = OaiResponsesStreamHandler(c, info, resp)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler kept reading upstream after the downstream write failed")
	}
	select {
	case <-upstreamBody.closed:
	default:
		t.Fatal("upstream response body was not closed after downstream write failure")
	}

	require.NotNil(t, info.StreamStatus)
	require.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
	require.ErrorIs(t, info.StreamStatus.EndError, writeErr)
	require.True(t, info.StreamStatus.HasErrors())
	require.NotNil(t, gotUsage)
	require.Equal(t, 200000, gotUsage.PromptTokens)
	require.Equal(t, 12, gotUsage.CompletionTokens)
	require.Equal(t, 200012, gotUsage.TotalTokens)
	require.Equal(t, 150000, gotUsage.PromptTokensDetails.CachedTokens)
}

func TestOaiResponsesStreamHandlerFlushFailureIsNotMarkedDone(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	upstreamBody := &blockingBody{
		chunk: []byte("data: {\"type\":\"response.completed\",\"response\":{" +
			"\"usage\":{\"input_tokens\":200000,\"output_tokens\":12,\"total_tokens\":200012}}}\n"),
		closed: make(chan struct{}),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	flushErr := errors.New("terminal flush failed")
	c.Writer = &failingResponsesFlushWriter{
		ResponseWriter: c.Writer,
		underlying: &flushErrorHTTPWriter{
			ResponseWriter: recorder,
			err:            flushErr,
		},
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       upstreamBody,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAI,
		DisablePing: true,
	}

	usage, gotErr := OaiResponsesStreamHandler(c, info, resp)
	require.Nil(t, gotErr)
	require.NotNil(t, usage)
	require.Equal(t, 200000, usage.PromptTokens)
	require.Equal(t, 12, usage.CompletionTokens)

	require.NotNil(t, info.StreamStatus)
	require.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
	require.ErrorIs(t, info.StreamStatus.EndError, flushErr)
	require.True(t, info.StreamStatus.HasErrors())
	select {
	case <-upstreamBody.closed:
	default:
		t.Fatal("upstream response body was not closed after terminal flush failure")
	}
}

func TestOaiResponsesStreamHandlerExpectedCancelDuringCompletedWriteIsNotMarkedDone(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	reqCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(reqCtx)
	c.Writer = &cancelAfterWriter{
		ResponseWriter: c.Writer,
		needle:         "event: response.completed",
		cancel:         cancel,
	}

	body := "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\"}}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_cancel\",\"usage\":{" +
		"\"input_tokens\":200000,\"output_tokens\":12,\"total_tokens\":200012}}}\n"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-test",
			ApiType:           constant.APITypeCodex,
			ChannelType:       constant.ChannelTypeCodex,
		},
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAIResponses,
		DisablePing: true,
	}

	usage, gotErr := OaiResponsesStreamHandler(c, info, resp)
	require.Nil(t, gotErr)
	require.NotNil(t, usage)
	require.Equal(t, 200000, usage.PromptTokens)
	require.Equal(t, 12, usage.CompletionTokens)
	require.ErrorIs(t, reqCtx.Err(), context.Canceled)

	require.NotNil(t, info.StreamStatus)
	require.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.StreamStatus.EndReason)
	require.NotEqual(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
}

func TestOaiResponsesStreamHandlerSuccessfulTerminalFlushWinsImmediateClientCancel(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	reqCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(reqCtx)
	c.Writer = &failingResponsesFlushWriter{
		ResponseWriter: c.Writer,
		underlying: &cancelAfterSuccessfulFlushHTTPWriter{
			ResponseWriter: recorder,
			cancel:         cancel,
		},
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.completed\",\"response\":{" +
				"\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}}\n")),
		Header: http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAIResponses,
		DisablePing: true,
	}

	usage, gotErr := OaiResponsesStreamHandler(c, info, resp)
	require.Nil(t, gotErr)
	require.NotNil(t, usage)
	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 3, usage.CompletionTokens)
	require.ErrorIs(t, reqCtx.Err(), context.Canceled)

	require.NotNil(t, info.StreamStatus)
	require.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	require.Contains(t, recorder.Body.String(), `event: response.completed`)
}
