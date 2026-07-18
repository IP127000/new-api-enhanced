package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

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
