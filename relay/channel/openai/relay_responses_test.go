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
