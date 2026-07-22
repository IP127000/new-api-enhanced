package helper

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func FlushWriter(c *gin.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("flush panic recovered: %v", r)
		}
	}()

	if c == nil || c.Writer == nil {
		return nil
	}

	if requestContextDone(c) {
		return fmt.Errorf("request context done: %w", c.Request.Context().Err())
	}

	// Gin's responseWriter implements http.Flusher with a no-error Flush method.
	// Calling only that method hides the underlying net/http FlushError result,
	// so a terminal SSE frame can be recorded as delivered even when the
	// connection failed while the server was flushing it. Unwrap transparent
	// response-writer layers first and invoke the underlying error-aware flush
	// exactly once. Calling Gin's Flush and then FlushError would double-flush;
	// Codex may close immediately after the first successful terminal flush, so
	// that second call could falsely turn a delivered frame into client_gone.
	writer := http.ResponseWriter(c.Writer)
	for {
		if flusher, ok := writer.(interface{ FlushError() error }); ok {
			if err := flusher.FlushError(); err != nil {
				return fmt.Errorf("flush response failed: %w", err)
			}
			return nil
		}

		unwrapper, ok := writer.(interface {
			Unwrap() http.ResponseWriter
		})
		if ok {
			next := unwrapper.Unwrap()
			if next == nil || next == writer {
				return errors.New("streaming error: invalid response writer unwrap")
			}
			writer = next
			continue
		}

		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
			return nil
		}
		return errors.New("streaming error: flusher not found")
	}
}

func requestContextDone(c *gin.Context) bool {
	return c != nil && c.Request != nil && c.Request.Context().Err() != nil
}

func SetEventStreamHeaders(c *gin.Context) {
	// 检查是否已经设置过头部
	if _, exists := c.Get("event_stream_headers_set"); exists {
		return
	}

	// 设置标志，表示头部已经设置过
	c.Set("event_stream_headers_set", true)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
}

func ClaudeData(c *gin.Context, resp dto.ClaudeResponse) error {
	if requestContextDone(c) {
		return nil
	}

	jsonData, err := common.Marshal(resp)
	if err != nil {
		common.SysError("error marshalling stream response: " + err.Error())
	} else {
		c.Render(-1, common.CustomEvent{Data: fmt.Sprintf("event: %s\n", resp.Type)})
		c.Render(-1, common.CustomEvent{Data: "data: " + string(jsonData)})
	}
	_ = FlushWriter(c)
	return nil
}

func ClaudeChunkData(c *gin.Context, resp dto.ClaudeResponse, data string) {
	if requestContextDone(c) {
		return
	}

	c.Render(-1, common.CustomEvent{Data: fmt.Sprintf("event: %s\n", resp.Type)})
	c.Render(-1, common.CustomEvent{Data: fmt.Sprintf("data: %s\n", data)})
	_ = FlushWriter(c)
}

func ResponseChunkData(c *gin.Context, resp dto.ResponsesStreamResponse, data string) error {
	return ResponseChunkDataByType(c, resp.Type, data)
}

// ResponseChunkDataByType writes a Responses SSE frame without formatting or
// rendering the (potentially very large) JSON payload into additional strings.
// It also returns every downstream write error so callers can promptly close
// the upstream response instead of continuing to buffer data for a gone client.
func ResponseChunkDataByType(c *gin.Context, eventType string, data string) error {
	if c == nil || c.Writer == nil {
		return errors.New("context or writer is nil")
	}
	if requestContextDone(c) {
		return fmt.Errorf("request context done: %w", c.Request.Context().Err())
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	if c.Writer.Header().Get("Cache-Control") == "" {
		c.Writer.Header().Set("Cache-Control", "no-cache")
	}

	// Keep the wire format identical to the previous CustomEvent rendering:
	// event: <type>\ndata: <verbatim JSON>\n\n.
	prefix := "event: " + eventType + "\ndata: "
	if err := writeStringAll(c.Writer, prefix); err != nil {
		return fmt.Errorf("write response event prefix failed: %w", err)
	}
	if err := writeStringAll(c.Writer, data); err != nil {
		return fmt.Errorf("write response event data failed: %w", err)
	}
	if err := writeStringAll(c.Writer, "\n\n"); err != nil {
		return fmt.Errorf("write response event terminator failed: %w", err)
	}
	return FlushWriter(c)
}

// ResponseChunkDataBytesByType writes a Responses SSE frame directly from the
// scanner-owned payload. The caller must keep data valid until this function
// returns and must not mutate it concurrently.
func ResponseChunkDataBytesByType(c *gin.Context, eventType string, data []byte) error {
	if c == nil || c.Writer == nil {
		return errors.New("context or writer is nil")
	}
	if requestContextDone(c) {
		return fmt.Errorf("request context done: %w", c.Request.Context().Err())
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	if c.Writer.Header().Get("Cache-Control") == "" {
		c.Writer.Header().Set("Cache-Control", "no-cache")
	}

	prefix := "event: " + eventType + "\ndata: "
	if err := writeStringAll(c.Writer, prefix); err != nil {
		return fmt.Errorf("write response event prefix failed: %w", err)
	}
	if err := writeBytesAll(c.Writer, data); err != nil {
		return fmt.Errorf("write response event data failed: %w", err)
	}
	if err := writeStringAll(c.Writer, "\n\n"); err != nil {
		return fmt.Errorf("write response event terminator failed: %w", err)
	}
	return FlushWriter(c)
}

func writeStringAll(writer io.Writer, data string) error {
	for len(data) > 0 {
		n, err := io.WriteString(writer, data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func writeBytesAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func StringData(c *gin.Context, str string) error {
	if c == nil || c.Writer == nil {
		return errors.New("context or writer is nil")
	}

	if requestContextDone(c) {
		return fmt.Errorf("request context done: %w", c.Request.Context().Err())
	}

	c.Render(-1, common.CustomEvent{Data: "data: " + str})
	return FlushWriter(c)
}

func PingData(c *gin.Context) error {
	if c == nil || c.Writer == nil {
		return errors.New("context or writer is nil")
	}

	if requestContextDone(c) {
		return fmt.Errorf("request context done: %w", c.Request.Context().Err())
	}

	if err := writeBytesAll(c.Writer, []byte(": PING\n\n")); err != nil {
		return fmt.Errorf("write ping data failed: %w", err)
	}
	return FlushWriter(c)
}

func ObjectData(c *gin.Context, object interface{}) error {
	if object == nil {
		return errors.New("object is nil")
	}
	jsonData, err := common.Marshal(object)
	if err != nil {
		return fmt.Errorf("error marshalling object: %w", err)
	}
	return StringData(c, string(jsonData))
}

func Done(c *gin.Context) {
	_ = StringData(c, "[DONE]")
}

func WssString(c *gin.Context, ws *websocket.Conn, str string) error {
	if ws == nil {
		logger.LogError(c, "websocket connection is nil")
		return errors.New("websocket connection is nil")
	}
	//common.LogInfo(c, fmt.Sprintf("sending message: %s", str))
	return ws.WriteMessage(1, []byte(str))
}

func WssObject(c *gin.Context, ws *websocket.Conn, object interface{}) error {
	jsonData, err := common.Marshal(object)
	if err != nil {
		return fmt.Errorf("error marshalling object: %w", err)
	}
	if ws == nil {
		logger.LogError(c, "websocket connection is nil")
		return errors.New("websocket connection is nil")
	}
	//common.LogInfo(c, fmt.Sprintf("sending message: %s", jsonData))
	return ws.WriteMessage(1, jsonData)
}

func WssError(c *gin.Context, ws *websocket.Conn, openaiError types.OpenAIError) {
	if ws == nil {
		return
	}
	errorObj := &dto.RealtimeEvent{
		Type:    "error",
		EventId: GetLocalRealtimeID(c),
		Error:   &openaiError,
	}
	_ = WssObject(c, ws, errorObj)
}

func GetResponseID(c *gin.Context) string {
	logID := c.GetString(common.RequestIdKey)
	return fmt.Sprintf("chatcmpl-%s", logID)
}

func GetLocalRealtimeID(c *gin.Context) string {
	logID := c.GetString(common.RequestIdKey)
	return fmt.Sprintf("evt_%s", logID)
}

func GenerateStartEmptyResponse(id string, createAt int64, model string, systemFingerprint *string) *dto.ChatCompletionsStreamResponse {
	return &dto.ChatCompletionsStreamResponse{
		Id:                id,
		Object:            "chat.completion.chunk",
		Created:           createAt,
		Model:             model,
		SystemFingerprint: systemFingerprint,
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					Role:    "assistant",
					Content: common.GetPointer(""),
				},
			},
		},
	}
}

func GenerateStopResponse(id string, createAt int64, model string, finishReason string) *dto.ChatCompletionsStreamResponse {
	return &dto.ChatCompletionsStreamResponse{
		Id:                id,
		Object:            "chat.completion.chunk",
		Created:           createAt,
		Model:             model,
		SystemFingerprint: nil,
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				FinishReason: &finishReason,
			},
		},
	}
}

func GenerateFinalUsageResponse(id string, createAt int64, model string, usage dto.Usage) *dto.ChatCompletionsStreamResponse {
	return &dto.ChatCompletionsStreamResponse{
		Id:                id,
		Object:            "chat.completion.chunk",
		Created:           createAt,
		Model:             model,
		SystemFingerprint: nil,
		Choices:           make([]dto.ChatCompletionsStreamResponseChoice, 0),
		Usage:             &usage,
	}
}
