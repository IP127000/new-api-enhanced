package model

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetLogRecordTestTables(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM logs").Error)
	require.NoError(t, DB.Exec("DELETE FROM users").Error)
	require.NoError(t, DB.Exec("DELETE FROM quota_data").Error)
}

func countPersistedLogs(t *testing.T) int64 {
	t.Helper()
	var count int64
	require.NoError(t, LOG_DB.Model(&Log{}).Count(&count).Error)
	return count
}

func TestOperationalAndErrorLogsAreNotPersisted(t *testing.T) {
	resetLogRecordTestTables(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	RecordLog(1, LogTypeSystem, "system event")
	RecordLogWithAdminInfo(1, LogTypeManage, "manage event", map[string]interface{}{"operator": "admin"})
	RecordLoginLog(1, "alice", "login", "127.0.0.1", "login", nil, nil)
	RecordOperationAuditLog(1, "setting update", "127.0.0.1", "setting.update", nil, nil, nil)
	RecordErrorLog(c, 1, 2, "gpt-test", "codex", "upstream unavailable", 3, 1, true, "svip", nil)

	assert.Equal(t, int64(0), countPersistedLogs(t))
}

func TestConsumeLogsStillPersistAndDriveStats(t *testing.T) {
	resetLogRecordTestTables(t)
	require.NoError(t, DB.Create(&User{Id: 1, Username: "alice", Status: common.UserStatusEnabled}).Error)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("username", "alice")
	c.Set(common.RequestIdKey, "req-success")
	c.Set(common.UpstreamRequestIdKey, "up-success")

	RecordConsumeLog(c, 1, RecordConsumeLogParams{
		ChannelId:        7,
		PromptTokens:     100,
		CompletionTokens: 20,
		ModelName:        "gpt-test",
		TokenName:        "codex",
		Quota:            123,
		Content:          "success",
		TokenId:          3,
		UseTimeSeconds:   1,
		IsStream:         true,
		Group:            "svip",
		Other:            map[string]interface{}{"cache_tokens": 40},
	})

	assert.Equal(t, int64(1), countPersistedLogs(t))

	stat, err := SumUsedQuota(
		LogTypeConsume,
		time.Now().Add(-time.Minute).Unix(),
		time.Now().Add(time.Minute).Unix(),
		"gpt-test",
		"alice",
		"codex",
		7,
		"svip",
		"req-success",
		"up-success",
	)
	require.NoError(t, err)
	assert.Equal(t, 1, stat.RequestCount)
	assert.Equal(t, 123, stat.Quota)
	assert.Equal(t, 120, stat.TotalTokens)
	assert.Equal(t, 40, stat.CacheTokens)
}

func TestConsumeLogModelRequestCountsUseSuccessfulConsumeLogs(t *testing.T) {
	resetLogRecordTestTables(t)
	now := time.Now().Unix()
	logs := []*Log{
		{UserId: 1, Username: "alice", CreatedAt: now, Type: LogTypeConsume, ModelName: "gpt-a"},
		{UserId: 1, Username: "alice", CreatedAt: now, Type: LogTypeConsume, ModelName: "gpt-a"},
		{UserId: 1, Username: "alice", CreatedAt: now, Type: LogTypeError, ModelName: "gpt-a"},
		{UserId: 2, Username: "bob", CreatedAt: now, Type: LogTypeConsume, ModelName: "gpt-b"},
		{UserId: 1, Username: "alice", CreatedAt: now - 3600, Type: LogTypeConsume, ModelName: "gpt-old"},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	counts, err := GetConsumeLogModelRequestCounts(now-10, now+10, "alice")
	require.NoError(t, err)
	require.Len(t, counts, 1)
	assert.Equal(t, "gpt-a", counts[0].ModelName)
	assert.Equal(t, int64(2), counts[0].RequestCount)
}
