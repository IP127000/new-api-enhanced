package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/common/limiter"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const (
	ModelRequestRateLimitCountMark        = "MRRL"
	ModelRequestRateLimitSuccessCountMark = "MRRLS"
)

// ModelRequestRateLimitReservation represents one request admitted by the
// model request limiter. Complete must be called exactly once after the
// request reaches a terminal outcome so successful-request limits retain the
// same semantics for HTTP requests and long-lived transports.
type ModelRequestRateLimitReservation struct {
	once          sync.Once
	recordSuccess func()
}

// Complete records a successful terminal outcome. A failed request still
// consumes the total-request allowance reserved by BeginModelRequestRateLimit,
// but does not consume the successful-request allowance.
func (r *ModelRequestRateLimitReservation) Complete(success bool) {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if success && r.recordSuccess != nil {
			r.recordSuccess()
		}
	})
}

// ModelRequestRateLimitError describes a rejected limiter reservation. The
// response-envelope detail is intentionally private: it preserves the legacy
// HTTP difference between Redis and in-memory limiter failures while exposing
// a transport-neutral status and message to WebSocket callers.
type ModelRequestRateLimitError struct {
	StatusCode     int
	Message        string
	openAIEnvelope bool
}

func (e *ModelRequestRateLimitError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ModelRequestRateLimitError) writeHTTP(c *gin.Context) {
	if e == nil {
		return
	}
	if e.openAIEnvelope {
		abortWithOpenAiMessage(c, e.StatusCode, e.Message)
		return
	}
	c.Status(e.StatusCode)
	c.Abort()
}

// 检查Redis中的请求限制
func checkRedisRateLimit(ctx context.Context, rdb *redis.Client, key string, maxCount int, duration int64) (bool, error) {
	// 如果maxCount为0，表示不限制
	if maxCount == 0 {
		return true, nil
	}

	// 获取当前计数
	length, err := rdb.LLen(ctx, key).Result()
	if err != nil {
		return false, err
	}

	// 如果未达到限制，允许请求
	if length < int64(maxCount) {
		return true, nil
	}

	// 检查时间窗口
	oldTimeStr, _ := rdb.LIndex(ctx, key, -1).Result()
	oldTime, err := time.Parse(timeFormat, oldTimeStr)
	if err != nil {
		return false, err
	}

	nowTimeStr := time.Now().Format(timeFormat)
	nowTime, err := time.Parse(timeFormat, nowTimeStr)
	if err != nil {
		return false, err
	}
	// 如果在时间窗口内已达到限制，拒绝请求
	subTime := nowTime.Sub(oldTime).Seconds()
	if int64(subTime) < duration {
		rdb.Expire(ctx, key, time.Duration(setting.ModelRequestRateLimitDurationMinutes)*time.Minute)
		return false, nil
	}

	return true, nil
}

// 记录Redis请求
func recordRedisRequest(ctx context.Context, rdb *redis.Client, key string, maxCount int) {
	// 如果maxCount为0，不记录请求
	if maxCount == 0 {
		return
	}

	now := time.Now().Format(timeFormat)
	rdb.LPush(ctx, key, now)
	rdb.LTrim(ctx, key, 0, int64(maxCount-1))
	rdb.Expire(ctx, key, time.Duration(setting.ModelRequestRateLimitDurationMinutes)*time.Minute)
}

func modelRequestRateLimitParameters(c *gin.Context) (duration int64, totalMaxCount, successMaxCount int) {
	duration = int64(setting.ModelRequestRateLimitDurationMinutes * 60)
	totalMaxCount = setting.ModelRequestRateLimitCount
	successMaxCount = setting.ModelRequestRateLimitSuccessCount

	group := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	if group == "" {
		group = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	}
	if groupTotalCount, groupSuccessCount, found := setting.GetGroupRateLimit(group); found {
		totalMaxCount = groupTotalCount
		successMaxCount = groupSuccessCount
	}
	return
}

func beginRedisModelRequestRateLimit(
	c *gin.Context,
	duration int64,
	totalMaxCount int,
	successMaxCount int,
) (*ModelRequestRateLimitReservation, *ModelRequestRateLimitError) {
	userId := strconv.Itoa(c.GetInt("id"))
	ctx := context.Background()
	rdb := common.RDB

	successKey := fmt.Sprintf("rateLimit:%s:%s", ModelRequestRateLimitSuccessCountMark, userId)
	allowed, err := checkRedisRateLimit(ctx, rdb, successKey, successMaxCount, duration)
	if err != nil {
		fmt.Println("检查成功请求数限制失败:", err.Error())
		return nil, &ModelRequestRateLimitError{
			StatusCode:     http.StatusInternalServerError,
			Message:        "rate_limit_check_failed",
			openAIEnvelope: true,
		}
	}
	if !allowed {
		return nil, &ModelRequestRateLimitError{
			StatusCode: http.StatusTooManyRequests,
			Message: fmt.Sprintf(
				"您已达到请求数限制：%d分钟内最多请求%d次",
				setting.ModelRequestRateLimitDurationMinutes,
				successMaxCount,
			),
			openAIEnvelope: true,
		}
	}

	if totalMaxCount > 0 {
		totalKey := fmt.Sprintf("rateLimit:%s", userId)
		tb := limiter.New(ctx, rdb)
		allowed, err = tb.Allow(
			ctx,
			totalKey,
			limiter.WithCapacity(int64(totalMaxCount)*duration),
			limiter.WithRate(int64(totalMaxCount)),
			limiter.WithRequested(duration),
		)
		if err != nil {
			fmt.Println("检查总请求数限制失败:", err.Error())
			return nil, &ModelRequestRateLimitError{
				StatusCode:     http.StatusInternalServerError,
				Message:        "rate_limit_check_failed",
				openAIEnvelope: true,
			}
		}
		if !allowed {
			return nil, &ModelRequestRateLimitError{
				StatusCode: http.StatusTooManyRequests,
				Message: fmt.Sprintf(
					"您已达到总请求数限制：%d分钟内最多请求%d次，包括失败次数，请检查您的请求是否正确",
					setting.ModelRequestRateLimitDurationMinutes,
					totalMaxCount,
				),
				openAIEnvelope: true,
			}
		}
	}

	return &ModelRequestRateLimitReservation{
		recordSuccess: func() {
			recordRedisRequest(ctx, rdb, successKey, successMaxCount)
		},
	}, nil
}

func beginMemoryModelRequestRateLimit(
	c *gin.Context,
	duration int64,
	totalMaxCount int,
	successMaxCount int,
) (*ModelRequestRateLimitReservation, *ModelRequestRateLimitError) {
	inMemoryRateLimiter.Init(time.Duration(setting.ModelRequestRateLimitDurationMinutes) * time.Minute)
	userId := strconv.Itoa(c.GetInt("id"))
	totalKey := ModelRequestRateLimitCountMark + userId
	successKey := ModelRequestRateLimitSuccessCountMark + userId

	if totalMaxCount > 0 && !inMemoryRateLimiter.Request(totalKey, totalMaxCount, duration) {
		return nil, &ModelRequestRateLimitError{
			StatusCode: http.StatusTooManyRequests,
			Message: fmt.Sprintf(
				"您已达到总请求数限制：%d分钟内最多请求%d次，包括失败次数，请检查您的请求是否正确",
				setting.ModelRequestRateLimitDurationMinutes,
				totalMaxCount,
			),
		}
	}

	// Preserve the existing in-memory limiter's check key. It has historically
	// counted admitted attempts independently from the terminal-success key.
	checkKey := successKey + "_check"
	if !inMemoryRateLimiter.Request(checkKey, successMaxCount, duration) {
		return nil, &ModelRequestRateLimitError{
			StatusCode: http.StatusTooManyRequests,
			Message: fmt.Sprintf(
				"您已达到请求数限制：%d分钟内最多请求%d次",
				setting.ModelRequestRateLimitDurationMinutes,
				successMaxCount,
			),
		}
	}

	return &ModelRequestRateLimitReservation{
		recordSuccess: func() {
			inMemoryRateLimiter.Request(successKey, successMaxCount, duration)
		},
	}, nil
}

// BeginModelRequestRateLimit reserves one model request without depending on
// an HTTP response lifecycle. Long-lived transports use the returned
// reservation to count each logical request while recording success only when
// that request reaches its successful terminal event.
func BeginModelRequestRateLimit(c *gin.Context) (*ModelRequestRateLimitReservation, *ModelRequestRateLimitError) {
	if !setting.ModelRequestRateLimitEnabled {
		return nil, nil
	}
	duration, totalMaxCount, successMaxCount := modelRequestRateLimitParameters(c)
	if common.RedisEnabled {
		return beginRedisModelRequestRateLimit(c, duration, totalMaxCount, successMaxCount)
	}
	return beginMemoryModelRequestRateLimit(c, duration, totalMaxCount, successMaxCount)
}

// ModelRequestRateLimit 模型请求限流中间件
func ModelRequestRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		reservation, limitErr := BeginModelRequestRateLimit(c)
		if limitErr != nil {
			limitErr.writeHTTP(c)
			return
		}

		c.Next()
		reservation.Complete(c.Writer.Status() < http.StatusBadRequest)
	}
}
