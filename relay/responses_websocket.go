package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/codex"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	responsesWebSocketBetaHeader    = "responses_websockets=2026-02-06"
	responsesWebSocketDialTimeout   = 15 * time.Second
	responsesWebSocketWriteTimeout  = 30 * time.Second
	responsesWebSocketTerminalGrace = 2 * time.Second
)

// ResponsesWebSocketFrame owns one replayable WebSocket text message. Call
// Close when the frame has either been forwarded or rejected.
type ResponsesWebSocketFrame struct {
	Storage  common.BodyStorage
	Type     string
	Model    string
	Generate bool
}

func (f *ResponsesWebSocketFrame) Close() error {
	if f == nil || f.Storage == nil {
		return nil
	}
	err := f.Storage.Close()
	f.Storage = nil
	return err
}

// ResponsesWebSocketError is serializable in the wrapped error shape used by
// the official Responses WebSocket client.
type ResponsesWebSocketError struct {
	StatusCode int
	CloseCode  int
	Type       string
	Code       string
	Message    string
	Headers    map[string]string
	Err        error
}

func (e *ResponsesWebSocketError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

type responsesWebSocketErrorEvent struct {
	Type    string                     `json:"type"`
	Status  int                        `json:"status"`
	Error   responsesWebSocketAPIError `json:"error"`
	Headers map[string]string          `json:"headers,omitempty"`
}

type responsesWebSocketAPIError struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func newResponsesWebSocketError(statusCode int, closeCode int, errorType string, code string, err error) *ResponsesWebSocketError {
	message := "responses websocket error"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}
	if errorType == "" {
		errorType = "new_api_error"
	}
	return &ResponsesWebSocketError{
		StatusCode: statusCode,
		CloseCode:  closeCode,
		Type:       errorType,
		Code:       code,
		Message:    message,
		Err:        err,
	}
}

// WriteResponsesWebSocketError sends an application error followed by a close
// frame. It is safe to call after the downstream HTTP connection was upgraded.
func WriteResponsesWebSocketError(conn *websocket.Conn, wsErr *ResponsesWebSocketError) {
	if conn == nil || wsErr == nil {
		return
	}
	statusCode := wsErr.StatusCode
	if statusCode < 100 || statusCode > 599 {
		statusCode = http.StatusInternalServerError
	}
	errorType := wsErr.Type
	if errorType == "" {
		errorType = "new_api_error"
	}
	message := wsErr.Message
	if message == "" {
		message = wsErr.Error()
	}
	payload, err := common.Marshal(responsesWebSocketErrorEvent{
		Type:   "error",
		Status: statusCode,
		Error: responsesWebSocketAPIError{
			Type:    errorType,
			Code:    wsErr.Code,
			Message: message,
		},
		Headers: wsErr.Headers,
	})
	if err == nil {
		_ = conn.SetWriteDeadline(time.Now().Add(responsesWebSocketWriteTimeout))
		_ = conn.WriteMessage(websocket.TextMessage, payload)
		_ = conn.SetWriteDeadline(time.Time{})
	}
	closeCode := wsErr.CloseCode
	if closeCode == 0 {
		closeCode = websocket.ClosePolicyViolation
	}
	writeResponsesWebSocketClose(conn, closeCode, truncateWebSocketCloseReason(message))
}

func truncateWebSocketCloseReason(reason string) string {
	const maxCloseReasonBytes = 123
	if len(reason) <= maxCloseReasonBytes {
		return reason
	}
	return reason[:maxCloseReasonBytes]
}

func writeResponsesWebSocketClose(conn *websocket.Conn, code int, reason string) {
	if conn == nil {
		return
	}
	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(responsesWebSocketWriteTimeout),
	)
}

func responsesWebSocketMaxFrameBytes() int64 {
	maxMB := appconstant.MaxRequestBodyMB
	if maxMB <= 0 {
		maxMB = 128
	}
	return int64(maxMB) << 20
}

// ReadResponsesWebSocketFrame reads and validates one response.create text
// frame without materializing large input or tool definitions.
func ReadResponsesWebSocketFrame(conn *websocket.Conn, maxBytes int64) (*ResponsesWebSocketFrame, *ResponsesWebSocketError) {
	frame, wsErr := readResponsesWebSocketTextFrame(conn, maxBytes)
	if wsErr != nil {
		return nil, wsErr
	}
	storage := frame.Storage

	fields, err := common.IndexTopLevelJSONObject(storage, storage.Size())
	if err != nil {
		_ = frame.Close()
		return nil, newResponsesWebSocketError(
			http.StatusBadRequest,
			websocket.CloseInvalidFramePayloadData,
			"invalid_request_error",
			string(types.ErrorCodeInvalidRequest),
			fmt.Errorf("invalid response.create JSON: %w", err),
		)
	}
	fieldsByName := make(map[string]common.JSONFieldSpan, len(fields))
	for _, field := range fields {
		fieldsByName[field.Name] = field
	}

	frame.Type, err = readResponsesWebSocketStringField(storage, fieldsByName, "type", true)
	if err == nil {
		frame.Model, err = readResponsesWebSocketStringField(storage, fieldsByName, "model", true)
	}
	if err == nil {
		if generateField, exists := fieldsByName["generate"]; exists {
			raw, readErr := common.ReadJSONSpan(storage, generateField.Value, 64)
			if readErr != nil {
				err = fmt.Errorf("generate is invalid: %w", readErr)
			} else if string(raw) != "null" {
				if unmarshalErr := common.Unmarshal(raw, &frame.Generate); unmarshalErr != nil {
					err = fmt.Errorf("generate must be a boolean: %w", unmarshalErr)
				}
			}
		}
	}
	if err == nil && frame.Type != "response.create" {
		err = fmt.Errorf("unsupported websocket message type %q", frame.Type)
	}
	if err != nil {
		_ = frame.Close()
		return nil, newResponsesWebSocketError(
			http.StatusBadRequest,
			websocket.ClosePolicyViolation,
			"invalid_request_error",
			string(types.ErrorCodeInvalidRequest),
			err,
		)
	}
	return frame, nil
}

func readResponsesWebSocketTextFrame(conn *websocket.Conn, maxBytes int64) (*ResponsesWebSocketFrame, *ResponsesWebSocketError) {
	if conn == nil {
		return nil, newResponsesWebSocketError(
			http.StatusInternalServerError,
			websocket.CloseInternalServerErr,
			"new_api_error",
			string(types.ErrorCodeInvalidRequest),
			errors.New("websocket connection is nil"),
		)
	}
	if maxBytes <= 0 {
		maxBytes = responsesWebSocketMaxFrameBytes()
	}
	conn.SetReadLimit(maxBytes)
	messageType, reader, err := conn.NextReader()
	if err != nil {
		closeCode := websocket.CloseGoingAway
		statusCode := http.StatusBadRequest
		errorCode := types.ErrorCodeInvalidRequest
		if errors.Is(err, websocket.ErrReadLimit) || websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
			closeCode = websocket.CloseMessageTooBig
			statusCode = http.StatusRequestEntityTooLarge
			errorCode = types.ErrorCodeReadRequestBodyFailed
		}
		return nil, newResponsesWebSocketError(
			statusCode,
			closeCode,
			"invalid_request_error",
			string(errorCode),
			err,
		)
	}
	if messageType != websocket.TextMessage {
		return nil, newResponsesWebSocketError(
			http.StatusBadRequest,
			websocket.CloseUnsupportedData,
			"invalid_request_error",
			string(types.ErrorCodeInvalidRequest),
			fmt.Errorf("responses websocket requires text JSON frames"),
		)
	}

	storage, err := common.CreateBodyStorageFromReader(reader, -1, maxBytes)
	if err != nil {
		statusCode := http.StatusBadRequest
		closeCode := websocket.CloseInvalidFramePayloadData
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, websocket.ErrReadLimit) {
			statusCode = http.StatusRequestEntityTooLarge
			closeCode = websocket.CloseMessageTooBig
		}
		return nil, newResponsesWebSocketError(
			statusCode,
			closeCode,
			"invalid_request_error",
			string(types.ErrorCodeReadRequestBodyFailed),
			err,
		)
	}
	return &ResponsesWebSocketFrame{Storage: storage, Generate: true}, nil
}

func readResponsesWebSocketStringField(
	storage common.BodyStorage,
	fields map[string]common.JSONFieldSpan,
	name string,
	required bool,
) (string, error) {
	field, exists := fields[name]
	if !exists {
		if required {
			return "", fmt.Errorf("%s is required", name)
		}
		return "", nil
	}
	raw, err := common.ReadJSONSpan(storage, field.Value, 64<<10)
	if err != nil {
		return "", fmt.Errorf("%s is invalid: %w", name, err)
	}
	if string(raw) == "null" {
		if required {
			return "", fmt.Errorf("%s is required", name)
		}
		return "", nil
	}
	var value string
	if err := common.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s must be a string: %w", name, err)
	}
	if required && strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

type responsesWebSocketDialFunc func(
	ctx context.Context,
	requestURL string,
	header http.Header,
	info *relaycommon.RelayInfo,
) (*websocket.Conn, *http.Response, error)

type responsesWebSocketRuntime struct {
	dial                responsesWebSocketDialFunc
	beginRateLimit      func(*gin.Context) (*middleware.ModelRequestRateLimitReservation, *middleware.ModelRequestRateLimitError)
	prepareAccounting   func(*gin.Context, *relaycommon.RelayInfo, dto.Request, bool) *types.NewAPIError
	consumeUsage        func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string)
	recordAffinity      func(*gin.Context, int)
	maxFrameBytes       int64
	terminalGracePeriod time.Duration
}

func defaultResponsesWebSocketRuntime() responsesWebSocketRuntime {
	return responsesWebSocketRuntime{
		dial:                dialResponsesWebSocketUpstream,
		beginRateLimit:      middleware.BeginModelRequestRateLimit,
		prepareAccounting:   PrepareRelayAccounting,
		consumeUsage:        service.PostTextConsumeQuota,
		recordAffinity:      service.RecordChannelAffinity,
		maxFrameBytes:       responsesWebSocketMaxFrameBytes(),
		terminalGracePeriod: responsesWebSocketTerminalGrace,
	}
}

type responsesWebSocketRound struct {
	ctx             *gin.Context
	info            *relaycommon.RelayInfo
	request         *dto.OpenAIResponsesRequest
	frame           *ResponsesWebSocketFrame
	outboundStorage common.BodyStorage
	closeOutbound   bool
	generate        bool
	settled         bool
	rateLimit       *middleware.ModelRequestRateLimitReservation
}

func (r *responsesWebSocketRound) releaseBody() {
	if r == nil {
		return
	}
	if r.closeOutbound && r.outboundStorage != nil {
		_ = r.outboundStorage.Close()
	}
	r.outboundStorage = nil
	if r.frame != nil {
		_ = r.frame.Close()
		r.frame = nil
	}
}

func (r *responsesWebSocketRound) refund() {
	if r == nil || r.settled {
		return
	}
	if r.generate && r.info != nil && r.info.Billing != nil {
		r.info.Billing.Refund(r.ctx)
	}
	r.rateLimit.Complete(false)
	r.settled = true
}

func (r *responsesWebSocketRound) settleWithoutTerminalUsage(runtime responsesWebSocketRuntime, reason string) {
	if r == nil || r.settled {
		return
	}
	if !r.generate {
		r.rateLimit.Complete(false)
		r.settled = true
		return
	}
	runtime.consumeUsage(r.ctx, r.info, nil, []string{reason})
	r.rateLimit.Complete(false)
	r.settled = true
}

func newResponsesWebSocketRoundContext(base *gin.Context, storage common.BodyStorage) *gin.Context {
	roundCtx := base.Copy()
	request := base.Request.Clone(base.Request.Context())
	request.Header = base.Request.Header.Clone()
	request.Header.Set("Content-Type", "application/json")
	request.Method = http.MethodPost
	request.ContentLength = storage.Size()
	request.Body = io.NopCloser(storage)
	roundCtx.Request = request
	roundCtx.Set(common.KeyBodyStorage, storage)
	roundCtx.Set(common.KeyBodyStorageReleased, false)
	roundCtx.Set(common.KeyRequestBody, nil)
	common.SetContextKey(roundCtx, appconstant.ContextKeyJSONBodyTopLevelFields, nil)
	common.SetContextKey(roundCtx, appconstant.ContextKeyRequestStartTime, time.Now())
	common.SetContextKey(roundCtx, common.UpstreamRequestIdKey, "")
	requestID := common.NewRequestId()
	roundCtx.Set(common.RequestIdKey, requestID)
	roundCtx.Request = roundCtx.Request.WithContext(context.WithValue(roundCtx.Request.Context(), common.RequestIdKey, requestID))
	return roundCtx
}

func prepareResponsesWebSocketRound(
	base *gin.Context,
	frame *ResponsesWebSocketFrame,
	sessionModel string,
	runtime responsesWebSocketRuntime,
) (*responsesWebSocketRound, *ResponsesWebSocketError) {
	if frame == nil || frame.Storage == nil {
		return nil, newResponsesWebSocketError(
			http.StatusBadRequest,
			websocket.ClosePolicyViolation,
			"invalid_request_error",
			string(types.ErrorCodeInvalidRequest),
			errors.New("response.create frame is empty"),
		)
	}
	if frame.Model != sessionModel {
		return nil, newResponsesWebSocketError(
			http.StatusBadRequest,
			websocket.ClosePolicyViolation,
			"invalid_request_error",
			string(types.ErrorCodeInvalidRequest),
			fmt.Errorf("model cannot change on a responses websocket connection"),
		)
	}

	roundCtx := newResponsesWebSocketRoundContext(base, frame.Storage)
	reservation, limitErr := runtime.beginRateLimit(roundCtx)
	if limitErr != nil {
		statusCode := limitErr.StatusCode
		closeCode := websocket.CloseTryAgainLater
		errorType := "rate_limit_error"
		code := "rate_limit_exceeded"
		if statusCode != http.StatusTooManyRequests {
			closeCode = websocket.CloseInternalServerErr
			errorType = "new_api_error"
			code = "rate_limit_check_failed"
		}
		return nil, newResponsesWebSocketError(
			statusCode,
			closeCode,
			errorType,
			code,
			limitErr,
		)
	}
	round := &responsesWebSocketRound{
		ctx:       roundCtx,
		frame:     frame,
		generate:  frame.Generate,
		rateLimit: reservation,
	}
	abortRound := func(wsErr *ResponsesWebSocketError) (*responsesWebSocketRound, *ResponsesWebSocketError) {
		round.rateLimit.Complete(false)
		return nil, wsErr
	}

	request, err := helper.GetAndValidateResponsesRequest(roundCtx)
	if err != nil {
		return abortRound(newResponsesWebSocketError(
			http.StatusBadRequest,
			websocket.ClosePolicyViolation,
			"invalid_request_error",
			string(types.ErrorCodeInvalidRequest),
			err,
		))
	}
	stream := true
	request.Stream = &stream

	info, err := relaycommon.GenRelayInfo(roundCtx, types.RelayFormatOpenAIResponses, request, nil)
	if err != nil {
		return abortRound(newResponsesWebSocketError(
			http.StatusInternalServerError,
			websocket.CloseInternalServerErr,
			"new_api_error",
			string(types.ErrorCodeGenRelayInfoFailed),
			err,
		))
	}
	round.info = info
	round.request = request
	info.IsStream = true
	info.InitChannelMeta(roundCtx)
	if info.ApiType != appconstant.APITypeCodex || info.ChannelType != appconstant.ChannelTypeCodex {
		return abortRound(newResponsesWebSocketError(
			http.StatusBadRequest,
			websocket.ClosePolicyViolation,
			"invalid_request_error",
			string(types.ErrorCodeGetChannelFailed),
			fmt.Errorf("responses websocket requires a Codex subscription channel"),
		))
	}
	if err := helper.ModelMappedHelper(roundCtx, info, request); err != nil {
		return abortRound(newResponsesWebSocketError(
			http.StatusBadRequest,
			websocket.ClosePolicyViolation,
			"invalid_request_error",
			string(types.ErrorCodeChannelModelMappedError),
			err,
		))
	}
	if err := relaycommon.ApplyCodexClientHeaderPassthroughWithRelayInfo(info); err != nil {
		return abortRound(newResponsesWebSocketError(
			http.StatusBadRequest,
			websocket.ClosePolicyViolation,
			"invalid_request_error",
			string(types.ErrorCodeChannelParamOverrideInvalid),
			err,
		))
	}

	// A generate=false prewarm still carries the complete prompt. Run sensitive
	// checks and price initialization with upstream-usage settlement so it
	// cannot bypass request policy or pre-consume quota for a no-generation turn.
	useUpstreamUsage := !frame.Generate || operation_setting.SelfUseModeEnabled
	if accountingErr := runtime.prepareAccounting(roundCtx, info, request, useUpstreamUsage); accountingErr != nil {
		return abortRound(newResponsesWebSocketError(
			accountingErr.StatusCode,
			websocket.ClosePolicyViolation,
			string(accountingErr.GetErrorType()),
			string(accountingErr.GetErrorCode()),
			accountingErr,
		))
	}

	mappedModel := ""
	if info.IsModelMapped {
		mappedModel = request.Model
	}
	round.outboundStorage, round.closeOutbound, err = prepareCodexOriginalResponsesBody(
		roundCtx,
		frame.Storage,
		info,
		false,
		mappedModel,
	)
	if err != nil {
		round.refund()
		return nil, newResponsesWebSocketError(
			http.StatusBadRequest,
			websocket.ClosePolicyViolation,
			"invalid_request_error",
			string(types.ErrorCodeConvertRequestFailed),
			err,
		)
	}
	return round, nil
}

func buildResponsesWebSocketUpstream(
	c *gin.Context,
	info *relaycommon.RelayInfo,
) (string, http.Header, *ResponsesWebSocketError) {
	adaptor := &codex.Adaptor{}
	requestURL, err := adaptor.GetRequestURL(info)
	if err != nil {
		return "", nil, newResponsesWebSocketError(
			http.StatusInternalServerError,
			websocket.CloseInternalServerErr,
			"new_api_error",
			string(types.ErrorCodeDoRequestFailed),
			err,
		)
	}
	parsedURL, err := url.Parse(requestURL)
	if err != nil {
		return "", nil, newResponsesWebSocketError(
			http.StatusInternalServerError,
			websocket.CloseInternalServerErr,
			"new_api_error",
			string(types.ErrorCodeDoRequestFailed),
			err,
		)
	}
	switch parsedURL.Scheme {
	case "https":
		parsedURL.Scheme = "wss"
	case "http":
		parsedURL.Scheme = "ws"
	case "wss", "ws":
	default:
		return "", nil, newResponsesWebSocketError(
			http.StatusInternalServerError,
			websocket.CloseInternalServerErr,
			"new_api_error",
			string(types.ErrorCodeDoRequestFailed),
			fmt.Errorf("unsupported Codex websocket URL scheme %q", parsedURL.Scheme),
		)
	}

	header := make(http.Header)
	if err := adaptor.SetupRequestHeader(c, &header, info); err != nil {
		return "", nil, newResponsesWebSocketError(
			http.StatusInternalServerError,
			websocket.CloseInternalServerErr,
			"new_api_error",
			string(types.ErrorCodeDoRequestFailed),
			err,
		)
	}
	headerOverride, err := channel.ResolveHeaderOverride(info, c)
	if err != nil {
		return "", nil, newResponsesWebSocketError(
			http.StatusBadRequest,
			websocket.ClosePolicyViolation,
			"invalid_request_error",
			string(types.ErrorCodeChannelHeaderOverrideInvalid),
			err,
		)
	}
	for name, value := range headerOverride {
		header.Set(name, value)
	}

	// Transport capability and subscription identity are authoritative server
	// configuration. They cannot be supplied or replaced by the downstream API
	// token or a client-controlled header.
	header.Set("OpenAI-Beta", responsesWebSocketBetaHeader)
	header.Del("x-openai-actor-authorization")
	header.Set("Content-Type", "application/json")
	return parsedURL.String(), header, nil
}

func dialResponsesWebSocketUpstream(
	ctx context.Context,
	requestURL string,
	header http.Header,
	info *relaycommon.RelayInfo,
) (*websocket.Conn, *http.Response, error) {
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = responsesWebSocketDialTimeout
	dialer.EnableCompression = true
	if info != nil && strings.TrimSpace(info.ChannelSetting.Proxy) != "" {
		client, err := service.NewProxyHttpClient(info.ChannelSetting.Proxy)
		if err != nil {
			return nil, nil, err
		}
		transport, ok := client.Transport.(*http.Transport)
		if !ok || transport == nil {
			return nil, nil, fmt.Errorf("unsupported websocket proxy transport %T", client.Transport)
		}
		dialer.Proxy = transport.Proxy
		dialer.NetDialContext = transport.DialContext
		if transport.TLSClientConfig != nil {
			dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
		}
	}
	return dialer.DialContext(ctx, requestURL, header)
}

func safeResponsesWebSocketHandshakeHeaders(header http.Header) map[string]string {
	if len(header) == 0 {
		return nil
	}
	safeNames := []string{
		"retry-after",
		"x-request-id",
		"x-codex-primary-used-percent",
		"x-codex-primary-window-minutes",
		"x-codex-secondary-used-percent",
		"x-codex-secondary-window-minutes",
	}
	result := make(map[string]string)
	for _, name := range safeNames {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			result[name] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func writeResponsesWebSocketStoredFrame(conn *websocket.Conn, storage common.BodyStorage) error {
	if conn == nil || storage == nil {
		return errors.New("websocket frame storage is nil")
	}
	reader, err := storage.NewReader()
	if err != nil {
		return err
	}
	defer reader.Close()

	_ = conn.SetWriteDeadline(time.Now().Add(responsesWebSocketWriteTimeout))
	writer, err := conn.NextWriter(websocket.TextMessage)
	if err != nil {
		_ = conn.SetWriteDeadline(time.Time{})
		return err
	}
	_, copyErr := io.Copy(writer, reader)
	closeErr := writer.Close()
	_ = conn.SetWriteDeadline(time.Time{})
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

type responsesWebSocketReadResult struct {
	frame *ResponsesWebSocketFrame
	err   *ResponsesWebSocketError
}

func startResponsesWebSocketReader(
	ctx context.Context,
	conn *websocket.Conn,
	maxBytes int64,
	validateResponseCreate bool,
) <-chan responsesWebSocketReadResult {
	results := make(chan responsesWebSocketReadResult)
	go func() {
		defer close(results)
		for {
			var frame *ResponsesWebSocketFrame
			var wsErr *ResponsesWebSocketError
			if validateResponseCreate {
				frame, wsErr = ReadResponsesWebSocketFrame(conn, maxBytes)
			} else {
				frame, wsErr = readResponsesWebSocketTextFrame(conn, maxBytes)
			}
			result := responsesWebSocketReadResult{frame: frame, err: wsErr}
			select {
			case results <- result:
				if wsErr != nil {
					return
				}
			case <-ctx.Done():
				if frame != nil {
					_ = frame.Close()
				}
				return
			}
		}
	}()
	return results
}

func readResponsesWebSocketEvent(frame *ResponsesWebSocketFrame) (string, *dto.ResponsesBillingStreamResponse, error) {
	if frame == nil || frame.Storage == nil {
		return "", nil, errors.New("upstream websocket frame is empty")
	}
	fields, err := common.IndexTopLevelJSONObject(frame.Storage, frame.Storage.Size())
	if err != nil {
		return "", nil, err
	}
	fieldsByName := make(map[string]common.JSONFieldSpan, len(fields))
	for _, field := range fields {
		fieldsByName[field.Name] = field
	}
	eventType, err := readResponsesWebSocketStringField(frame.Storage, fieldsByName, "type", true)
	if err != nil {
		return "", nil, err
	}

	if eventType != "response.completed" && eventType != dto.ResponsesOutputTypeItemDone {
		return eventType, nil, nil
	}
	if _, err := frame.Storage.Seek(0, io.SeekStart); err != nil {
		return "", nil, err
	}
	var event dto.ResponsesBillingStreamResponse
	if err := common.DecodeJson(frame.Storage, &event); err != nil {
		return "", nil, err
	}
	_, _ = frame.Storage.Seek(0, io.SeekStart)
	return eventType, &event, nil
}

func observeResponsesWebSocketBuiltInTool(info *relaycommon.RelayInfo, event *dto.ResponsesBillingStreamResponse) {
	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil ||
		event == nil || event.Item == nil || event.Item.Type != dto.BuildInCallWebSearchCall {
		return
	}
	for _, toolName := range []string{dto.BuildInToolWebSearch, dto.BuildInToolWebSearchPreview} {
		if tool, exists := info.ResponsesUsageInfo.BuiltInTools[toolName]; exists && tool != nil {
			tool.CallCount++
			return
		}
	}
}

func settleResponsesWebSocketRound(
	round *responsesWebSocketRound,
	event *dto.ResponsesBillingStreamResponse,
	runtime responsesWebSocketRuntime,
) {
	if round == nil || round.settled {
		return
	}
	var source *dto.ResponsesBillingUsage
	if event != nil && event.Response != nil {
		source = event.Response.Usage
		if quality, size, ok := event.Response.ImageGenerationCall(); ok {
			round.ctx.Set("image_generation_call", true)
			round.ctx.Set("image_generation_call_quality", quality)
			round.ctx.Set("image_generation_call_size", size)
		}
	}
	if !round.generate {
		// ChatGPT reports input usage for generate=false prewarm, but Codex treats
		// it as connection setup rather than an inference request and excludes it
		// from turn usage. Policy and total-request limiting already ran before the
		// frame was sent; do not create a quota/log entry for setup-only usage.
		round.rateLimit.Complete(false)
		round.settled = true
		return
	}
	usage := openai.ResponsesBillingUsageToUsage(source)
	runtime.consumeUsage(round.ctx, round.info, usage, nil)
	round.rateLimit.Complete(true)
	round.settled = true
}

func forwardResponsesWebSocketEvent(
	client *websocket.Conn,
	frame *ResponsesWebSocketFrame,
	eventType string,
	event *dto.ResponsesBillingStreamResponse,
) error {
	if eventType == "response.completed" && event != nil {
		if compacted, ok, err := openai.CompactCodexCompletedEvent(event); err != nil {
			return err
		} else if ok {
			_ = client.SetWriteDeadline(time.Now().Add(responsesWebSocketWriteTimeout))
			err = client.WriteMessage(websocket.TextMessage, compacted)
			_ = client.SetWriteDeadline(time.Time{})
			return err
		}
	}
	return writeResponsesWebSocketStoredFrame(client, frame.Storage)
}

func upstreamResponsesWebSocketFrameError(err *ResponsesWebSocketError) *ResponsesWebSocketError {
	if err == nil {
		return nil
	}
	if closeErr, ok := err.Err.(*websocket.CloseError); ok {
		return &ResponsesWebSocketError{
			StatusCode: http.StatusBadGateway,
			CloseCode:  closeErr.Code,
			Type:       "upstream_error",
			Code:       string(types.ErrorCodeDoRequestFailed),
			Message:    closeErr.Text,
			Err:        closeErr,
		}
	}
	err.StatusCode = http.StatusBadGateway
	err.CloseCode = websocket.CloseInternalServerErr
	err.Type = "upstream_error"
	err.Code = string(types.ErrorCodeDoRequestFailed)
	return err
}

func isResponsesWebSocketDisconnect(wsErr *ResponsesWebSocketError) bool {
	if wsErr == nil {
		return false
	}
	var closeErr *websocket.CloseError
	if errors.As(wsErr.Err, &closeErr) {
		return true
	}
	return wsErr.CloseCode == websocket.CloseGoingAway
}

func drainResponsesWebSocketRound(
	upstreamFrames <-chan responsesWebSocketReadResult,
	round **responsesWebSocketRound,
	runtime responsesWebSocketRuntime,
	timeout time.Duration,
	settleMissingUsage bool,
) {
	if round == nil || *round == nil {
		return
	}
	if timeout <= 0 {
		timeout = responsesWebSocketTerminalGrace
	}
	timer := time.NewTimer(timeout)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	finishMissing := func() {
		if *round == nil {
			return
		}
		if settleMissingUsage {
			(*round).settleWithoutTerminalUsage(runtime, "WebSocket 协议异常，未收到上游终态 usage")
		} else {
			(*round).refund()
		}
		*round = nil
	}

	for *round != nil {
		select {
		case result, ok := <-upstreamFrames:
			if !ok || result.err != nil {
				finishMissing()
				return
			}
			if (*round).info != nil {
				(*round).info.SetFirstResponseTime()
				(*round).info.ReceivedResponseCount++
			}
			eventType, event, err := readResponsesWebSocketEvent(result.frame)
			_ = result.frame.Close()
			if err != nil {
				finishMissing()
				return
			}
			if eventType == dto.ResponsesOutputTypeItemDone {
				observeResponsesWebSocketBuiltInTool((*round).info, event)
			}
			switch eventType {
			case "response.completed":
				settleResponsesWebSocketRound(*round, event, runtime)
				*round = nil
			case "error", "response.failed", "response.incomplete":
				finishMissing()
			}
		case <-timer.C:
			finishMissing()
		}
	}
}

// ResponsesWebSocketHelper runs one persistent downstream/upstream Responses
// session. The selected Codex channel in c is fixed for the lifetime of the
// connection; reconnecting or switching accounts behind previous_response_id
// is intentionally forbidden.
func ResponsesWebSocketHelper(
	c *gin.Context,
	client *websocket.Conn,
	firstFrame *ResponsesWebSocketFrame,
) *types.NewAPIError {
	return responsesWebSocketHelperWithRuntime(c, client, firstFrame, defaultResponsesWebSocketRuntime())
}

func responsesWebSocketHelperWithRuntime(
	c *gin.Context,
	client *websocket.Conn,
	firstFrame *ResponsesWebSocketFrame,
	runtime responsesWebSocketRuntime,
) *types.NewAPIError {
	if runtime.maxFrameBytes <= 0 {
		runtime.maxFrameBytes = responsesWebSocketMaxFrameBytes()
	}
	if runtime.terminalGracePeriod <= 0 {
		runtime.terminalGracePeriod = responsesWebSocketTerminalGrace
	}
	if runtime.dial == nil || runtime.beginRateLimit == nil || runtime.prepareAccounting == nil || runtime.consumeUsage == nil || runtime.recordAffinity == nil {
		wsErr := newResponsesWebSocketError(
			http.StatusInternalServerError,
			websocket.CloseInternalServerErr,
			"new_api_error",
			string(types.ErrorCodeDoRequestFailed),
			errors.New("responses websocket runtime is incomplete"),
		)
		WriteResponsesWebSocketError(client, wsErr)
		return types.NewError(wsErr, types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}

	sessionModel := firstFrame.Model
	firstRound, wsErr := prepareResponsesWebSocketRound(c, firstFrame, sessionModel, runtime)
	if wsErr != nil {
		_ = firstFrame.Close()
		WriteResponsesWebSocketError(client, wsErr)
		return types.NewError(wsErr, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	upstreamURL, upstreamHeader, wsErr := buildResponsesWebSocketUpstream(firstRound.ctx, firstRound.info)
	if wsErr != nil {
		firstRound.refund()
		firstRound.releaseBody()
		WriteResponsesWebSocketError(client, wsErr)
		return types.NewError(wsErr, types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}

	upstream, handshakeResponse, err := runtime.dial(c.Request.Context(), upstreamURL, upstreamHeader, firstRound.info)
	if handshakeResponse != nil && handshakeResponse.Body != nil {
		defer handshakeResponse.Body.Close()
	}
	if err != nil {
		firstRound.refund()
		firstRound.releaseBody()
		statusCode := http.StatusBadGateway
		headers := map[string]string(nil)
		if handshakeResponse != nil {
			if handshakeResponse.StatusCode >= 100 && handshakeResponse.StatusCode <= 599 {
				statusCode = handshakeResponse.StatusCode
			}
			headers = safeResponsesWebSocketHandshakeHeaders(handshakeResponse.Header)
		}
		wsErr = newResponsesWebSocketError(
			statusCode,
			websocket.CloseInternalServerErr,
			"upstream_error",
			string(types.ErrorCodeDoRequestFailed),
			fmt.Errorf("upstream websocket handshake failed: %w", err),
		)
		wsErr.Headers = headers
		WriteResponsesWebSocketError(client, wsErr)
		return types.NewError(wsErr, types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}
	defer upstream.Close()

	if err := writeResponsesWebSocketStoredFrame(upstream, firstRound.outboundStorage); err != nil {
		firstRound.refund()
		firstRound.releaseBody()
		wsErr = newResponsesWebSocketError(
			http.StatusBadGateway,
			websocket.CloseInternalServerErr,
			"upstream_error",
			string(types.ErrorCodeDoRequestFailed),
			err,
		)
		WriteResponsesWebSocketError(client, wsErr)
		return types.NewError(wsErr, types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}
	firstRound.releaseBody()
	common.ReleaseBodyStorage(c)

	readerCtx, cancelReaders := context.WithCancel(c.Request.Context())
	defer cancelReaders()
	downstreamFrames := startResponsesWebSocketReader(readerCtx, client, runtime.maxFrameBytes, true)
	upstreamFrames := startResponsesWebSocketReader(readerCtx, upstream, runtime.maxFrameBytes, false)
	activeRound := firstRound
	affinityRecorded := false

	refundActive := func() {
		if activeRound != nil {
			activeRound.refund()
			activeRound = nil
		}
	}

	for {
		select {
		case result, ok := <-downstreamFrames:
			if !ok || (result.err != nil && isResponsesWebSocketDisconnect(result.err)) {
				drainResponsesWebSocketRound(
					upstreamFrames,
					&activeRound,
					runtime,
					runtime.terminalGracePeriod,
					true,
				)
				if result.err != nil {
					var closeErr *websocket.CloseError
					if errors.As(result.err.Err, &closeErr) {
						writeResponsesWebSocketClose(upstream, closeErr.Code, closeErr.Text)
					}
				}
				return nil
			}
			if result.err != nil {
				WriteResponsesWebSocketError(client, result.err)
				drainResponsesWebSocketRound(
					upstreamFrames,
					&activeRound,
					runtime,
					runtime.terminalGracePeriod,
					true,
				)
				writeResponsesWebSocketClose(upstream, websocket.ClosePolicyViolation, "invalid downstream websocket frame")
				return types.NewError(result.err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
			}
			if activeRound != nil {
				_ = result.frame.Close()
				wsErr = newResponsesWebSocketError(
					http.StatusConflict,
					websocket.ClosePolicyViolation,
					"invalid_request_error",
					string(types.ErrorCodeInvalidRequest),
					errors.New("a response.create request is already active"),
				)
				WriteResponsesWebSocketError(client, wsErr)
				drainResponsesWebSocketRound(
					upstreamFrames,
					&activeRound,
					runtime,
					runtime.terminalGracePeriod,
					true,
				)
				writeResponsesWebSocketClose(upstream, websocket.ClosePolicyViolation, "concurrent response.create")
				return types.NewError(wsErr, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
			}
			if !result.frame.Generate {
				_ = result.frame.Close()
				wsErr = newResponsesWebSocketError(
					http.StatusBadRequest,
					websocket.ClosePolicyViolation,
					"invalid_request_error",
					string(types.ErrorCodeInvalidRequest),
					errors.New("generate=false is only allowed on the first response.create frame"),
				)
				WriteResponsesWebSocketError(client, wsErr)
				return types.NewError(wsErr, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
			}

			nextRound, prepareErr := prepareResponsesWebSocketRound(c, result.frame, sessionModel, runtime)
			if prepareErr != nil {
				_ = result.frame.Close()
				WriteResponsesWebSocketError(client, prepareErr)
				return types.NewError(prepareErr, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
			}
			if err := writeResponsesWebSocketStoredFrame(upstream, nextRound.outboundStorage); err != nil {
				nextRound.refund()
				nextRound.releaseBody()
				wsErr = newResponsesWebSocketError(
					http.StatusBadGateway,
					websocket.CloseInternalServerErr,
					"upstream_error",
					string(types.ErrorCodeDoRequestFailed),
					err,
				)
				WriteResponsesWebSocketError(client, wsErr)
				return types.NewError(wsErr, types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
			}
			nextRound.releaseBody()
			activeRound = nextRound

		case result, ok := <-upstreamFrames:
			if !ok || result.err != nil {
				refundActive()
				if result.err != nil {
					upstreamErr := upstreamResponsesWebSocketFrameError(result.err)
					if closeErr, isClose := result.err.Err.(*websocket.CloseError); isClose {
						writeResponsesWebSocketClose(client, closeErr.Code, closeErr.Text)
						return nil
					}
					WriteResponsesWebSocketError(client, upstreamErr)
					return types.NewError(upstreamErr, types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
				}
				writeResponsesWebSocketClose(client, websocket.CloseGoingAway, "upstream websocket closed")
				return nil
			}

			eventRound := activeRound
			if eventRound != nil && eventRound.info != nil {
				eventRound.info.SetFirstResponseTime()
				eventRound.info.ReceivedResponseCount++
			}
			eventType, event, eventErr := readResponsesWebSocketEvent(result.frame)
			if eventErr != nil {
				_ = result.frame.Close()
				refundActive()
				wsErr = newResponsesWebSocketError(
					http.StatusBadGateway,
					websocket.CloseInvalidFramePayloadData,
					"upstream_error",
					string(types.ErrorCodeBadResponseBody),
					eventErr,
				)
				WriteResponsesWebSocketError(client, wsErr)
				return types.NewError(wsErr, types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
			}
			if eventType == dto.ResponsesOutputTypeItemDone && activeRound != nil {
				observeResponsesWebSocketBuiltInTool(activeRound.info, event)
			}
			if eventType == "response.completed" {
				settleResponsesWebSocketRound(activeRound, event, runtime)
				if activeRound != nil && activeRound.generate && !affinityRecorded {
					runtime.recordAffinity(c, activeRound.info.ChannelId)
					affinityRecorded = true
				}
				activeRound = nil
			}

			if err := forwardResponsesWebSocketEvent(client, result.frame, eventType, event); err != nil {
				_ = result.frame.Close()
				drainResponsesWebSocketRound(
					upstreamFrames,
					&activeRound,
					runtime,
					runtime.terminalGracePeriod,
					true,
				)
				return nil
			}
			if eventRound != nil && eventRound.info != nil {
				eventRound.info.SendResponseCount++
			}
			_ = result.frame.Close()

			if eventType == "error" || eventType == "response.failed" || eventType == "response.incomplete" {
				refundActive()
				writeResponsesWebSocketClose(client, websocket.CloseInternalServerErr, "upstream response failed")
				return nil
			}
		}
	}
}

func logResponsesWebSocketError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	logger.LogError(c, fmt.Sprintf("responses websocket relay error: %s", common.LocalLogPreview(err.Error())))
}
