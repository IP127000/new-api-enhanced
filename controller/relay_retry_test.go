package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRelayAttemptsPerChannelUsesRetryTimesAsChannelAttemptLimit(t *testing.T) {
	oldRetryTimes := common.RetryTimes
	t.Cleanup(func() {
		common.RetryTimes = oldRetryTimes
	})

	common.RetryTimes = 3
	require.Equal(t, 3, relayAttemptsPerChannel())

	common.RetryTimes = 0
	require.Equal(t, 1, relayAttemptsPerChannel())
}

func TestShouldRetryStopsAfterStreamingRequestIsCommitted(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", http.NoBody)
	common.SetContextKey(c, constant.ContextKeyRelayRetryCommitted, true)
	err := types.NewErrorWithStatusCode(errors.New("upstream stream failed"), types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)

	require.False(t, shouldRetry(c, err, 1))
}

func TestShouldRetryIgnoresChannelAffinitySkipForRelayLoop(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", http.NoBody)

	// Simulate a channel-affinity-bound Codex/Claude request that previously
	// signaled skip-retry-on-failure. The literal mirrors the service package's
	// unexported ginKeyChannelAffinitySkipRetry; the guard assertion below fails
	// loudly if that key ever diverges, keeping this regression from going vacuous.
	c.Set("channel_affinity_skip_retry_on_failure", true)
	require.True(t, service.ShouldSkipRetryAfterChannelAffinityFailure(c),
		"precondition: channel-affinity skip-retry signal must be active")

	// A retryable upstream channel failure must still retry so Codex SSE
	// (POST /v1/responses) and Claude (/v1/messages) cycle to other channels.
	// Codex WebSocket keeps single-channel binding in its own transport handler,
	// not by suppressing retry here.
	err := types.NewError(errors.New("upstream channel timeout"), types.ErrorCodeChannelResponseTimeExceeded)
	require.True(t, shouldRetry(c, err, 1))
}
