package service

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/gin-gonic/gin"
	"github.com/samber/hot"
)

// RealtimeCallBinding contains routing metadata, never upstream credentials.
type RealtimeCallBinding struct {
	ChannelID int    `json:"channel_id"`
	Model     string `json:"model"`
	KeyIndex  int    `json:"key_index"`
	AccountID string `json:"account_id"`
}

var realtimeCalls = sync.OnceValue(func() *cachex.HybridCache[RealtimeCallBinding] {
	return cachex.NewHybridCache[RealtimeCallBinding](cachex.HybridCacheConfig[RealtimeCallBinding]{
		Namespace:    cachex.Namespace("realtime_calls"),
		Redis:        common.RDB,
		RedisEnabled: func() bool { return common.RedisEnabled && common.RDB != nil },
		RedisCodec:   cachex.JSONCodec[RealtimeCallBinding]{},
		Memory: func() *hot.HotCache[string, RealtimeCallBinding] {
			return hot.NewHotCache[string, RealtimeCallBinding](hot.LRU, 10000).
				WithTTL(2 * time.Hour).WithJanitor().Build()
		},
	})
})

// RememberRealtimeCall binds a WebRTC call to the token and subscription channel
// that created it. The sideband must join that same upstream account.
func RememberRealtimeCall(c *gin.Context, location string, binding RealtimeCallBinding) error {
	u, err := url.Parse(location)
	if err != nil {
		return fmt.Errorf("invalid realtime call location")
	}
	var id string
	for _, prefix := range []string{"/v1/realtime/calls/", "/v1/live/"} {
		if value, ok := strings.CutPrefix(u.Path, prefix); ok {
			id = value
			break
		}
	}
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/?#") || len(id) > 256 {
		return fmt.Errorf("invalid realtime call identifier")
	}
	key := fmt.Sprintf("%d:%d:%s", c.GetInt("id"), c.GetInt("token_id"), id)
	return realtimeCalls().SetWithTTL(key, binding, 2*time.Hour)
}

func LookupRealtimeCall(c *gin.Context, id string) (RealtimeCallBinding, bool, error) {
	key := fmt.Sprintf("%d:%d:%s", c.GetInt("id"), c.GetInt("token_id"), id)
	return realtimeCalls().Get(key)
}
