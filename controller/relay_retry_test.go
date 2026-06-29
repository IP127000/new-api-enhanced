package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
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
