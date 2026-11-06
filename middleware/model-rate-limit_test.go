package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var modelRequestRateLimitTestUserSequence atomic.Int64

func nextModelRequestRateLimitTestUserID() int {
	return 950000 + int(modelRequestRateLimitTestUserSequence.Add(1))
}

type modelRequestRateLimitTestSettings struct {
	enabled      bool
	duration     int
	total        int
	success      int
	group        map[string][2]int
	redisEnabled bool
}

func configureMemoryModelRequestRateLimitTest(t *testing.T, total, success int) {
	t.Helper()

	setting.ModelRequestRateLimitMutex.Lock()
	original := modelRequestRateLimitTestSettings{
		enabled:      setting.ModelRequestRateLimitEnabled,
		duration:     setting.ModelRequestRateLimitDurationMinutes,
		total:        setting.ModelRequestRateLimitCount,
		success:      setting.ModelRequestRateLimitSuccessCount,
		group:        setting.ModelRequestRateLimitGroup,
		redisEnabled: common.RedisEnabled,
	}
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	setting.ModelRequestRateLimitCount = total
	setting.ModelRequestRateLimitSuccessCount = success
	setting.ModelRequestRateLimitGroup = map[string][2]int{}
	setting.ModelRequestRateLimitMutex.Unlock()
	common.RedisEnabled = false
	inMemoryRateLimiter.Init(time.Hour)

	t.Cleanup(func() {
		setting.ModelRequestRateLimitMutex.Lock()
		setting.ModelRequestRateLimitEnabled = original.enabled
		setting.ModelRequestRateLimitDurationMinutes = original.duration
		setting.ModelRequestRateLimitCount = original.total
		setting.ModelRequestRateLimitSuccessCount = original.success
		setting.ModelRequestRateLimitGroup = original.group
		setting.ModelRequestRateLimitMutex.Unlock()
		common.RedisEnabled = original.redisEnabled
	})
}

func newModelRequestRateLimitTestContext(userID int, group string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", http.NoBody)
	c.Set("id", userID)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, group)
	return c
}

func TestModelRequestRateLimitHTTPMiddlewareRetainsPerRequestBehavior(t *testing.T) {
	gin.SetMode(gin.TestMode)
	configureMemoryModelRequestRateLimitTest(t, 1, 10)

	handlerCalls := 0
	userID := nextModelRequestRateLimitTestUserID()
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("id", userID)
		common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
	})
	engine.Use(ModelRequestRateLimit())
	engine.POST("/v1/responses", func(c *gin.Context) {
		handlerCalls++
		c.Status(http.StatusNoContent)
	})

	first := httptest.NewRecorder()
	engine.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/v1/responses", http.NoBody))
	require.Equal(t, http.StatusNoContent, first.Code)

	second := httptest.NewRecorder()
	engine.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/v1/responses", http.NoBody))
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	require.Contains(t, second.Body.String(), `"error"`)
	require.Contains(t, second.Body.String(), "total request limit")
	require.Equal(t, 1, handlerCalls)
}
