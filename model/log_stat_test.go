package model

import (
	"os"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSumUsedQuotaIncludesTokenAndCacheStats(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			previousDB, previousLogDB := DB, LOG_DB
			previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
			DB, LOG_DB = db, db
			common.SetDatabaseTypes(common.DatabaseType(dialect), common.DatabaseType(dialect))
			initCol()
			t.Cleanup(func() {
				_ = db.Migrator().DropTable(&Log{}, &QuotaData{})
				DB, LOG_DB = previousDB, previousLogDB
				common.SetDatabaseTypes(previousMainType, previousLogType)
				initCol()
				_ = sqlDB.Close()
			})
			require.NoError(t, db.AutoMigrate(&Log{}, &QuotaData{}))
			require.NoError(t, db.AutoMigrate(&Log{}, &QuotaData{}))
			var version string
			versionSQL := "SELECT version()"
			if dialect == "sqlite" {
				versionSQL = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Logf("database version: %s", version)
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

			counts, err := GetConsumeLogModelRequestCounts(now-10, now+10, "alice")
			require.NoError(t, err)
			require.Len(t, counts, 1)
			assert.EqualValues(t, 2, counts[0].RequestCount)
			require.NoError(t, DB.Create(&QuotaData{Username: "alice", ModelName: "gpt-5.5", CreatedAt: 1000, TokenUsed: 120}).Error)
			earliest, err := GetEarliestQuotaDataTime("alice")
			require.NoError(t, err)
			assert.EqualValues(t, 1000, earliest)
			// Existing usage survives a second migration.
			require.NoError(t, db.AutoMigrate(&Log{}, &QuotaData{}))
			var rows int64
			require.NoError(t, db.Model(&Log{}).Count(&rows).Error)
			assert.EqualValues(t, 2, rows)
		})
	}
}
