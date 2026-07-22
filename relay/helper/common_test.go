package helper

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type flushErrorResponseWriter struct {
	http.ResponseWriter
	err error
}

func (w *flushErrorResponseWriter) FlushError() error {
	return w.err
}

type hidingFlushErrorWriter struct {
	gin.ResponseWriter
	underlying http.ResponseWriter
}

func (w *hidingFlushErrorWriter) Flush() {}

func (w *hidingFlushErrorWriter) Unwrap() http.ResponseWriter {
	return w.underlying
}

type firstFlushOnlyWriter struct {
	http.ResponseWriter
	calls     int
	secondErr error
}

func (w *firstFlushOnlyWriter) FlushError() error {
	w.calls++
	if w.calls > 1 {
		return w.secondErr
	}
	return nil
}

type delegatingFlushWriter struct {
	gin.ResponseWriter
	underlying *firstFlushOnlyWriter
}

func (w *delegatingFlushWriter) Flush() {
	_ = w.underlying.FlushError()
}

func (w *delegatingFlushWriter) Unwrap() http.ResponseWriter {
	return w.underlying
}

type shortResponseWriter struct {
	gin.ResponseWriter
	wrote int
}

func (w *shortResponseWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	w.wrote++
	if len(data) == 1 {
		return w.ResponseWriter.Write(data)
	}
	return w.ResponseWriter.Write(data[:len(data)/2])
}

func (w *shortResponseWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func TestResponseChunkDataByTypePreservesResponsesSSEWireFormat(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	err := ResponseChunkDataByType(c, "response.output_text.delta", `{"type":"response.output_text.delta","delta":"hello"}`)
	require.NoError(t, err)
	require.Equal(t,
		"event: response.output_text.delta\n"+
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n",
		recorder.Body.String())
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"))
}

func TestFlushWriterReturnsUnderlyingFlushError(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	expectedErr := errors.New("downstream flush failed")
	underlying := &flushErrorResponseWriter{ResponseWriter: recorder, err: expectedErr}
	c.Writer = &hidingFlushErrorWriter{
		ResponseWriter: c.Writer,
		underlying:     underlying,
	}

	err := FlushWriter(c)
	require.ErrorIs(t, err, expectedErr)
}

func TestFlushWriterDoesNotFlushGinWrapperAndUnderlyingTwice(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	underlying := &firstFlushOnlyWriter{
		ResponseWriter: recorder,
		secondErr:      errors.New("client closed after first successful flush"),
	}
	c.Writer = &delegatingFlushWriter{
		ResponseWriter: c.Writer,
		underlying:     underlying,
	}

	err := FlushWriter(c)
	require.NoError(t, err)
	require.Equal(t, 1, underlying.calls)
}

func TestResponseChunkDataBytesByTypeCompletesShortWrites(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	writer := &shortResponseWriter{ResponseWriter: c.Writer}
	c.Writer = writer

	data := []byte(`{"type":"response.completed","response":{"status":"completed"}}`)
	err := ResponseChunkDataBytesByType(c, "response.completed", data)
	require.NoError(t, err)
	require.Greater(t, writer.wrote, 1)
	require.Equal(t,
		"event: response.completed\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n",
		recorder.Body.String())
}

func TestPingDataCompletesShortWrites(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	writer := &shortResponseWriter{ResponseWriter: c.Writer}
	c.Writer = writer

	err := PingData(c)
	require.NoError(t, err)
	require.Greater(t, writer.wrote, 1)
	require.Equal(t, ": PING\n\n", recorder.Body.String())
}
