package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRankingCacheKeyIncludesScope(t *testing.T) {
	config := rankingPeriodConfig{id: "week"}

	globalKey := rankingCacheKey(config, RankingScope{Global: true})
	selfKey := rankingCacheKey(config, RankingScope{UserID: 42})

	require.Equal(t, "week:global", globalKey)
	require.Equal(t, "week:user:42", selfKey)
	require.NotEqual(t, globalKey, selfKey)
}

func TestGetRankingsSnapshotRejectsMissingUserScope(t *testing.T) {
	_, err := GetRankingsSnapshot("week", RankingScope{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires a user")
}
