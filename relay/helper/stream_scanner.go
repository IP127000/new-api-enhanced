package helper

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
)

const (
	InitialScannerBufferSize    = 64 << 10  // 64KB (64*1024)
	DefaultMaxScannerBufferSize = 128 << 20 // 64MB (64*1024*1024) default SSE buffer size
	DefaultPingInterval         = 10 * time.Second
	defaultStreamDataBufferSize = 10
	// streamWriteTimeout bounds a single blocked write to a slow client so the
	// unconditional wg.Wait() in cleanup can always finish. Without it, a slow
	// but connected client (full TCP buffer, no server WriteTimeout) could hang
	// the handler forever.
	streamWriteTimeout = 30 * time.Second
)

func getScannerBufferSize() int {
	if constant.StreamScannerMaxBufferMB > 0 {
		return constant.StreamScannerMaxBufferMB << 20
	}
	return DefaultMaxScannerBufferSize
}

func NewStreamScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, InitialScannerBufferSize), getScannerBufferSize())
	return scanner
}

func copyCodexSSEHeaders(c *gin.Context, resp *http.Response) {
	if c == nil || c.Writer == nil || resp == nil {
		return
	}
	// codex
	for _, name := range []string{"X-Reasoning-Included", "X-Codex-Turn-State"} {
		values := resp.Header.Values(name)
		if !service.ShouldCopyUpstreamHeader(c, name, values) {
			continue
		}
		for _, value := range values {
			if value != "" {
				c.Writer.Header().Add(name, value)
			}
		}
	}
}

// ExtendWriteDeadline pushes the connection write deadline forward before each
// stream write. Best-effort: writers that don't support deadlines (e.g.
// httptest recorders) are silently ignored.
func ExtendWriteDeadline(c *gin.Context) {
	if c == nil || c.Writer == nil {
		return
	}
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(streamWriteTimeout))
}

// ClearWriteDeadline removes the per-write deadline after a stream write has
// completed. Leaving the deadline installed turns a single-write timeout into
// an idle-stream timeout and can reset a healthy HTTP/2 stream later.
func ClearWriteDeadline(c *gin.Context) {
	if c == nil || c.Writer == nil {
		return
	}
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})
}

type StreamScannerOptions struct {
	// DataBufferSize controls the number of complete SSE payloads queued between
	// the upstream scanner and the data handler. Zero uses synchronous handoff.
	DataBufferSize int
	// InlineDataHandler runs the data handler on the scanner goroutine. This is
	// the real synchronous mode for large Responses events: the scanner cannot
	// read the next complete event until the current event has been parsed and
	// forwarded, so large event payloads do not accumulate in two goroutines.
	InlineDataHandler bool
	// ClientGoneGracePeriod keeps reading the upstream briefly after an expected
	// client cancellation. It is intended for protocols whose terminal usage
	// event can immediately trail the event that caused client-side preemption.
	// No downstream writes should be attempted during this grace.
	ClientGoneGracePeriod time.Duration
}

func setClientGoneEndReason(c *gin.Context, status *relaycommon.StreamStatus) {
	if status != nil && status.IsClientCloseExpected() {
		status.SetEndReason(relaycommon.StreamEndReasonHandlerStop, nil)
		return
	}
	status.SetEndReason(relaycommon.StreamEndReasonClientGone, c.Request.Context().Err())
}

// StreamDiagnostic returns a safe, request-correlated stream lifecycle summary
// without including request or response payloads.
func StreamDiagnostic(c *gin.Context, info *relaycommon.RelayInfo, stage string) string {
	requestID := ""
	upstreamRequestID := ""
	contextErr := "<nil>"
	if c != nil {
		requestID = c.GetString(common.RequestIdKey)
		upstreamRequestID = c.GetString(common.UpstreamRequestIdKey)
		if c.Request != nil && c.Request.Context().Err() != nil {
			contextErr = c.Request.Context().Err().Error()
		}
	}
	expectedClose := false
	endReason := relaycommon.StreamEndReasonNone
	received := 0
	if info != nil {
		received = info.ReceivedResponseCount
		if info.StreamStatus != nil {
			expectedClose = info.StreamStatus.IsClientCloseExpected()
			endReason = info.StreamStatus.EndReason
		}
	}
	return fmt.Sprintf("stream_diag stage=%s request_id=%s upstream_request_id=%s context_err=%s expected_close=%t end_reason=%s received=%d", stage, requestID, upstreamRequestID, contextErr, expectedClose, endReason, received)
}

func StreamScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataHandler func(data string, sr *StreamResult)) {
	StreamScannerHandlerWithOptions(c, resp, info, StreamScannerOptions{
		DataBufferSize: defaultStreamDataBufferSize,
	}, dataHandler)
}

// StreamScannerHandlerWithDataBufferSize lets memory-sensitive stream formats
// opt into a smaller queue without changing the established buffering behavior
// of every other relay format. A size of zero uses synchronous handoff.
func StreamScannerHandlerWithDataBufferSize(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataBufferSize int, dataHandler func(data string, sr *StreamResult)) {
	StreamScannerHandlerWithOptions(c, resp, info, StreamScannerOptions{
		DataBufferSize: dataBufferSize,
	}, dataHandler)
}

func StreamScannerHandlerWithOptions(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, options StreamScannerOptions, dataHandler func(data string, sr *StreamResult)) {
	dataBufferSize := options.DataBufferSize
	if dataBufferSize < 0 {
		dataBufferSize = 0
	}
	if options.ClientGoneGracePeriod < 0 {
		options.ClientGoneGracePeriod = 0
	}
	streamScannerHandler(c, resp, info, dataBufferSize, options.ClientGoneGracePeriod, options.InlineDataHandler, dataHandler, nil)
}

// StreamScannerHandlerBytesWithOptions is the allocation-bounded variant for
// large SSE payloads. The byte slice is valid only for the duration of the
// callback and must not be retained. Handling is forced inline so Scanner may
// not reuse its backing buffer before the callback returns.
func StreamScannerHandlerBytesWithOptions(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, options StreamScannerOptions, dataHandler func(data []byte, sr *StreamResult)) {
	dataBufferSize := options.DataBufferSize
	if dataBufferSize < 0 {
		dataBufferSize = 0
	}
	if options.ClientGoneGracePeriod < 0 {
		options.ClientGoneGracePeriod = 0
	}
	streamScannerHandler(c, resp, info, dataBufferSize, options.ClientGoneGracePeriod, true, nil, dataHandler)
}

func streamScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataBufferSize int, clientGoneGracePeriod time.Duration, inlineDataHandler bool, dataHandler func(data string, sr *StreamResult), bytesDataHandler func(data []byte, sr *StreamResult)) {

	if resp == nil || (dataHandler == nil && bytesDataHandler == nil) {
		return
	}

	// 无条件新建 StreamStatus
	info.StreamStatus = relaycommon.NewStreamStatus()

	streamingTimeout := time.Duration(constant.StreamingTimeout) * time.Second

	var (
		stopChan    = make(chan bool, 3) // 增加缓冲区避免阻塞
		scanner     = NewStreamScanner(resp.Body)
		ticker      = time.NewTicker(streamingTimeout)
		pingTicker  *time.Ticker
		writeMutex  sync.Mutex     // Mutex to protect concurrent writes
		wg          sync.WaitGroup // 用于等待所有 goroutine 退出
		cleanupOnce sync.Once
		stopOnce    sync.Once
		clientGone  atomic.Bool
	)

	stop := func() {
		stopOnce.Do(func() {
			close(stopChan)
		})
	}
	observeClientGone := func(source string) bool {
		// A request context can be canceled immediately after the client parses a
		// successfully flushed terminal event. Wait for any in-flight SSE handler
		// to finish and set Done/Stop before claiming the stream ended because the
		// client disappeared; otherwise sync.Once can permanently preserve a false
		// client_gone even though response.completed was delivered.
		writeMutex.Lock()
		defer writeMutex.Unlock()
		if info.StreamStatus.EndReason != relaycommon.StreamEndReasonNone {
			// The scanner may have observed an expected cancellation first and
			// armed the terminal-usage grace before the main select receives the
			// same context signal. Preserve that grace instead of treating the
			// existing handler_stop as a reason to clean up immediately.
			if info.StreamStatus.EndReason == relaycommon.StreamEndReasonHandlerStop &&
				clientGoneGracePeriod > 0 && info.StreamStatus.IsClientCloseExpected() {
				clientGone.Store(true)
				return false
			}
			return true
		}
		setClientGoneEndReason(c, info.StreamStatus)
		logger.LogError(c, StreamDiagnostic(c, info, "downstream_context_done source="+source))
		if clientGoneGracePeriod <= 0 || !info.StreamStatus.IsClientCloseExpected() {
			return true
		}
		clientGone.Store(true)
		logger.LogInfo(c, StreamDiagnostic(c, info, fmt.Sprintf("client_gone_grace_started duration=%s source=%s", clientGoneGracePeriod, source)))
		return false
	}

	generalSettings := operation_setting.GetGeneralSetting()
	pingEnabled := generalSettings.PingIntervalEnabled && !info.DisablePing
	pingInterval := time.Duration(generalSettings.PingIntervalSeconds) * time.Second
	if pingInterval <= 0 {
		pingInterval = DefaultPingInterval
	}

	if pingEnabled {
		pingTicker = time.NewTicker(pingInterval)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, "stop_chan", stopChan)

	logger.LogDebug(c, "relay timeout seconds: %d", common.RelayTimeout)
	logger.LogDebug(c, "relay max idle conns: %d", common.RelayMaxIdleConns)
	logger.LogDebug(c, "relay max idle conns per host: %d", common.RelayMaxIdleConnsPerHost)
	logger.LogDebug(c, "streaming timeout seconds: %d", int64(streamingTimeout.Seconds()))
	logger.LogDebug(c, "ping interval seconds: %d", int64(pingInterval.Seconds()))

	cleanup := func() {
		cleanupOnce.Do(func() {
			cancel()
			stop()
			if resp.Body != nil {
				logger.LogInfo(c, StreamDiagnostic(c, info, "upstream_body_close"))
				_ = resp.Body.Close()
			}

			ticker.Stop()
			if pingTicker != nil {
				pingTicker.Stop()
			}

			wg.Wait()
		})
	}
	// Ensure gin.Context is not returned to Gin's pool while any stream goroutine can still use it.
	defer cleanup()

	scanner.Split(bufio.ScanLines)
	copyCodexSSEHeaders(c, resp)
	SetEventStreamHeaders(c)
	// Handle ping data sending with improved error handling
	if pingEnabled && pingTicker != nil {
		wg.Add(1)
		gopool.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					logger.LogError(c, fmt.Sprintf("ping goroutine panic: %v", r))
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("ping panic: %v", r))
					stop()
				}
				logger.LogDebug(c, "ping goroutine exited")
				wg.Done()
			}()

			// 添加超时保护，防止 goroutine 无限运行
			maxPingDuration := 30 * time.Minute // 最大 ping 持续时间
			pingTimeout := time.NewTimer(maxPingDuration)
			defer pingTimeout.Stop()

			for {
				select {
				case <-pingTicker.C:
					var err error
					func() {
						writeMutex.Lock()
						defer writeMutex.Unlock()
						ExtendWriteDeadline(c)
						defer ClearWriteDeadline(c)
						err = PingData(c)
					}()
					if err != nil {
						logger.LogError(c, "ping data error: "+err.Error())
						info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPingFail, err)
						return
					}
					logger.LogDebug(c, "ping data sent")
				case <-ctx.Done():
					return
				case <-stopChan:
					return
				case <-c.Request.Context().Done():
					// 监听客户端断开连接
					return
				case <-pingTimeout.C:
					logger.LogError(c, "ping goroutine max duration reached")
					return
				}
			}
		})
	}

	dataChan := make(chan string, dataBufferSize)
	var inlineResult *StreamResult
	if inlineDataHandler {
		inlineResult = newStreamResult(info.StreamStatus)
	} else {
		wg.Add(1)
		gopool.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					logger.LogError(c, fmt.Sprintf("data handler goroutine panic: %v", r))
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("handler panic: %v", r))
				}
				stop()
				wg.Done()
			}()
			sr := newStreamResult(info.StreamStatus)
			for data := range dataChan {
				sr.reset()
				func() {
					writeMutex.Lock()
					defer writeMutex.Unlock()
					ExtendWriteDeadline(c)
					defer ClearWriteDeadline(c)
					dataHandler(data, sr)
				}()
				if sr.IsStopped() {
					return
				}
			}
		})
	}

	// Scanner goroutine with improved error handling
	wg.Add(1)
	common.RelayCtxGo(ctx, func() {
		defer func() {
			if !inlineDataHandler {
				close(dataChan)
			}
			if r := recover(); r != nil {
				logger.LogError(c, fmt.Sprintf("scanner goroutine panic: %v", r))
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("scanner panic: %v", r))
			}
			stop()
			logger.LogDebug(c, "scanner goroutine exited")
			wg.Done()
		}()

		for scanner.Scan() {
			// 检查是否需要停止
			var requestDone <-chan struct{}
			if !clientGone.Load() {
				requestDone = c.Request.Context().Done()
			}
			select {
			case <-stopChan:
				return
			case <-ctx.Done():
				return
			case <-requestDone:
				if observeClientGone("scanner_context_done") {
					return
				}
			default:
			}

			ticker.Reset(streamingTimeout)
			if bytesDataHandler != nil {
				line := scanner.Bytes()
				logger.LogDebug(c, "stream scanner data: %s", line)
				if len(line) < 6 {
					continue
				}
				if !bytes.HasPrefix(line, []byte("data:")) && !bytes.HasPrefix(line, []byte("[DONE]")) {
					continue
				}
				data := bytes.TrimSpace(line[5:])
				if len(data) == 0 {
					continue
				}
				if !bytes.HasPrefix(data, []byte("[DONE]")) {
					info.SetFirstResponseTime()
					info.ReceivedResponseCount++
					inlineResult.reset()
					func() {
						writeMutex.Lock()
						defer writeMutex.Unlock()
						ExtendWriteDeadline(c)
						defer ClearWriteDeadline(c)
						bytesDataHandler(data, inlineResult)
					}()
					if inlineResult.IsStopped() {
						return
					}
				} else {
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
					logger.LogDebug(c, "received [DONE], stopping scanner")
					return
				}
				continue
			}
			data := scanner.Text()
			logger.LogDebug(c, "stream scanner data: %s", data)

			if len(data) < 6 {
				continue
			}
			if data[:5] != "data:" && data[:6] != "[DONE]" {
				continue
			}
			data = data[5:]
			data = strings.TrimSpace(data)
			if data == "" {
				continue
			}
			if !strings.HasPrefix(data, "[DONE]") {
				info.SetFirstResponseTime()
				info.ReceivedResponseCount++
				if inlineDataHandler {
					inlineResult.reset()
					func() {
						writeMutex.Lock()
						defer writeMutex.Unlock()
						ExtendWriteDeadline(c)
						defer ClearWriteDeadline(c)
						dataHandler(data, inlineResult)
					}()
					if inlineResult.IsStopped() {
						return
					}
				} else {
					for {
						requestDone = nil
						if !clientGone.Load() {
							requestDone = c.Request.Context().Done()
						}
						select {
						case dataChan <- data:
							goto dataSent
						case <-ctx.Done():
							return
						case <-stopChan:
							return
						case <-requestDone:
							if observeClientGone("scanner_enqueue_context_done") {
								return
							}
						}
					}
				dataSent:
				}
			} else {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
				logger.LogDebug(c, "received [DONE], stopping scanner")
				return
			}
		}

		if err := scanner.Err(); err != nil {
			if err != io.EOF {
				if ctx.Err() == nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonNone {
					logger.LogError(c, "scanner error: "+err.Error())
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
				}
			}
		}
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	})

	// 主循环等待完成或超时
	select {
	case <-ticker.C:
		writeMutex.Lock()
		if info.StreamStatus.EndReason == relaycommon.StreamEndReasonNone {
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, nil)
		}
		writeMutex.Unlock()
	case <-stopChan:
		// EndReason already set by the goroutine that triggered stopChan
	case <-c.Request.Context().Done():
		if observeClientGone("main_context_done") {
			break
		}
		graceTimer := time.NewTimer(clientGoneGracePeriod)
		select {
		case <-stopChan:
			logger.LogInfo(c, StreamDiagnostic(c, info, "client_gone_grace_ended_by_stream"))
		case <-graceTimer.C:
			logger.LogError(c, StreamDiagnostic(c, info, fmt.Sprintf("client_gone_grace_expired duration=%s", clientGoneGracePeriod)))
		}
		if !graceTimer.Stop() {
			select {
			case <-graceTimer.C:
			default:
			}
		}
	}

	cleanup()
	if info.StreamStatus.IsNormalEnd() && !info.StreamStatus.HasErrors() {
		logger.LogInfo(c, fmt.Sprintf("%s summary=%s", StreamDiagnostic(c, info, "stream_end"), info.StreamStatus.Summary()))
	} else {
		logger.LogError(c, fmt.Sprintf("%s summary=%s", StreamDiagnostic(c, info, "stream_end"), info.StreamStatus.Summary()))
	}
}
