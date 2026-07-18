package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func clearRankingTestTables(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM logs").Error)
	require.NoError(t, DB.Exec("DELETE FROM quota_data").Error)
}

func seedRankingLog(t *testing.T, log Log) {
	t.Helper()
	require.NoError(t, LOG_DB.Create(&log).Error)
}

func seedRankingQuotaData(t *testing.T, quotaData QuotaData) {
	t.Helper()
	require.NoError(t, DB.Create(&quotaData).Error)
}

func TestGetRankingQuotaTotalsUsesConsumeLogs(t *testing.T) {
	clearRankingTestTables(t)

	seedRankingLog(t, Log{CreatedAt: 1100, Type: LogTypeConsume, ModelName: "gpt-a", PromptTokens: 100, CompletionTokens: 50})
	seedRankingLog(t, Log{CreatedAt: 1200, Type: LogTypeConsume, ModelName: "gpt-a", PromptTokens: 20, CompletionTokens: 30})
	seedRankingLog(t, Log{CreatedAt: 1300, Type: LogTypeConsume, ModelName: "gpt-b", PromptTokens: 10, CompletionTokens: 15})
	seedRankingLog(t, Log{CreatedAt: 1400, Type: LogTypeManage, ModelName: "gpt-a", PromptTokens: 999, CompletionTokens: 999})
	seedRankingLog(t, Log{CreatedAt: 1500, Type: LogTypeConsume, ModelName: "", PromptTokens: 50, CompletionTokens: 50})
	seedRankingLog(t, Log{CreatedAt: 2500, Type: LogTypeConsume, ModelName: "gpt-c", PromptTokens: 500, CompletionTokens: 500})
	seedRankingQuotaData(t, QuotaData{ModelName: "stale-export", CreatedAt: 1100, TokenUsed: 9999})
	seedRankingQuotaData(t, QuotaData{ModelName: "gpt-a", CreatedAt: 1100, TokenUsed: 1})

	rows, err := GetRankingQuotaTotals(1000, 2000, 0)
	require.NoError(t, err)
	require.Equal(t, []RankingQuotaTotal{
		{ModelName: "gpt-a", TotalTokens: 200},
		{ModelName: "gpt-b", TotalTokens: 25},
	}, rows)
}

func TestGetRankingQuotaBucketsUsesConsumeLogs(t *testing.T) {
	clearRankingTestTables(t)

	seedRankingLog(t, Log{CreatedAt: 3661, Type: LogTypeConsume, ModelName: "gpt-a", PromptTokens: 10, CompletionTokens: 5})
	seedRankingLog(t, Log{CreatedAt: 3700, Type: LogTypeConsume, ModelName: "gpt-a", PromptTokens: 20, CompletionTokens: 5})
	seedRankingLog(t, Log{CreatedAt: 7200, Type: LogTypeConsume, ModelName: "gpt-b", PromptTokens: 7, CompletionTokens: 3})
	seedRankingQuotaData(t, QuotaData{ModelName: "stale-export", CreatedAt: 3600, TokenUsed: 9999})

	rows, err := GetRankingQuotaBuckets(3600, 7200, 3600, 0)
	require.NoError(t, err)
	require.Equal(t, []RankingQuotaBucket{
		{ModelName: "gpt-a", Bucket: 3600, Tokens: 40},
		{ModelName: "gpt-b", Bucket: 7200, Tokens: 10},
	}, rows)
}

func TestGetRankingQuotaTotalsScopesToUser(t *testing.T) {
	clearRankingTestTables(t)

	seedRankingLog(t, Log{UserId: 1, CreatedAt: 1100, Type: LogTypeConsume, ModelName: "gpt-a", PromptTokens: 100, CompletionTokens: 50})
	seedRankingLog(t, Log{UserId: 2, CreatedAt: 1200, Type: LogTypeConsume, ModelName: "gpt-a", PromptTokens: 900, CompletionTokens: 100})
	seedRankingLog(t, Log{UserId: 1, CreatedAt: 1300, Type: LogTypeConsume, ModelName: "gpt-b", PromptTokens: 10, CompletionTokens: 15})

	rows, err := GetRankingQuotaTotals(1000, 2000, 1)
	require.NoError(t, err)
	require.Equal(t, []RankingQuotaTotal{
		{ModelName: "gpt-a", TotalTokens: 150},
		{ModelName: "gpt-b", TotalTokens: 25},
	}, rows)
}
