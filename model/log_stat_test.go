package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSumUsedQuotaIncludesTokenAndCacheStats(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM logs").Error)

	cacheOther, err := common.Marshal(map[string]int{"cache_tokens": 40})
	require.NoError(t, err)
	ignoredOther, err := common.Marshal(map[string]int{"cache_tokens": 900})
	require.NoError(t, err)

	now := time.Now().Unix()
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:         now,
		Type:              LogTypeConsume,
		Username:          "alice",
		TokenName:         "primary",
		ModelName:         "gpt-5.5",
		Quota:             123,
		PromptTokens:      100,
		CompletionTokens:  20,
		ChannelId:         7,
		Group:             "svip",
		RequestId:         "req-match",
		UpstreamRequestId: "up-match",
		Other:             string(cacheOther),
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		CreatedAt:         now,
		Type:              LogTypeConsume,
		Username:          "alice",
		TokenName:         "primary",
		ModelName:         "gpt-5.5",
		Quota:             999,
		PromptTokens:      900,
		CompletionTokens:  90,
		ChannelId:         7,
		Group:             "svip",
		RequestId:         "req-other",
		UpstreamRequestId: "up-other",
		Other:             string(ignoredOther),
	}).Error)

	stat, err := SumUsedQuota(
		LogTypeConsume,
		now-10,
		now+10,
		"gpt-5.5",
		"alice",
		"primary",
		7,
		"svip",
		"req-match",
		"up-match",
	)

	require.NoError(t, err)
	assert.Equal(t, 123, stat.Quota)
	assert.Equal(t, 100, stat.PromptTokens)
	assert.Equal(t, 20, stat.CompletionTokens)
	assert.Equal(t, 120, stat.TotalTokens)
	assert.Equal(t, 40, stat.CacheTokens)
	assert.Equal(t, 1, stat.RequestCount)
	assert.Equal(t, 1, stat.Rpm)
	assert.Equal(t, 120, stat.Tpm)
	assert.InDelta(t, 0.4, stat.CacheHitRate, 0.0001)
}
