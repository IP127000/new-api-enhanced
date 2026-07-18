package perfmetrics

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

func resetHotBucketsForTest() {
	hotBuckets.Range(func(key, _ any) bool {
		hotBuckets.Delete(key)
		return true
	})
}

func TestRecordSkipsFailureSamples(t *testing.T) {
	resetHotBucketsForTest()
	t.Cleanup(resetHotBucketsForTest)
	common.RedisEnabled = false

	Record(Sample{
		Model:     "gpt-test",
		Group:     "svip",
		LatencyMs: 100,
		Success:   false,
	})

	hotBuckets.Range(func(_, _ any) bool {
		t.Fatal("failure sample should not create a performance bucket")
		return false
	})

	Record(Sample{
		Model:        "gpt-test",
		Group:        "svip",
		LatencyMs:    100,
		Success:      true,
		OutputTokens: 20,
		GenerationMs: 1000,
	})

	var snapshots []counters
	hotBuckets.Range(func(_, value any) bool {
		snapshots = append(snapshots, value.(*atomicBucket).snapshot())
		return true
	})

	if assert.Len(t, snapshots, 1) {
		assert.Equal(t, int64(1), snapshots[0].requestCount)
		assert.Equal(t, int64(1), snapshots[0].successCount)
		assert.Equal(t, int64(100), snapshots[0].totalLatencyMs)
	}
}
