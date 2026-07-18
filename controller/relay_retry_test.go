package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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
