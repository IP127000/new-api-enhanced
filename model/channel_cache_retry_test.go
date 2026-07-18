package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGetRandomSatisfiedChannelUsesAllPriorityLevelsAcrossThreeRetries(t *testing.T) {
	const (
		groupName = "svip-retry-test"
		modelName = "gpt-5.5-retry-test"
	)

	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	channelSyncLock.Lock()
	oldGroup2Model2Channels := group2model2channels
	oldChannelsIDM := channelsIDM
	oldChannel2AdvancedCustomConfig := channel2advancedCustomConfig

	common.MemoryCacheEnabled = true
	zeroWeight := uint(0)
	highPriority := int64(30)
	middlePriority := int64(20)
	lowPriority := int64(10)
	group2model2channels = map[string]map[string][]int{
		groupName: {
			modelName: {9101, 9102, 9103},
		},
	}
	channelsIDM = map[int]*Channel{
		9101: {Id: 9101, Weight: &zeroWeight, Priority: &highPriority},
		9102: {Id: 9102, Weight: &zeroWeight, Priority: &middlePriority},
		9103: {Id: 9103, Weight: &zeroWeight, Priority: &lowPriority},
	}
	channel2advancedCustomConfig = nil
	channelSyncLock.Unlock()

	t.Cleanup(func() {
		channelSyncLock.Lock()
		group2model2channels = oldGroup2Model2Channels
		channelsIDM = oldChannelsIDM
		channel2advancedCustomConfig = oldChannel2AdvancedCustomConfig
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		channelSyncLock.Unlock()
	})

	tests := []struct {
		name       string
		retry      int
		expectedID int
	}{
		{name: "first attempt uses highest priority", retry: 0, expectedID: 9101},
		{name: "first retry uses middle priority", retry: 1, expectedID: 9102},
		{name: "second retry uses lowest priority", retry: 2, expectedID: 9103},
		{name: "extra retries stay on lowest priority", retry: 3, expectedID: 9103},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel, err := GetRandomSatisfiedChannel(groupName, modelName, tt.retry, "/v1/responses")
			require.NoError(t, err)
			require.NotNil(t, channel)
			require.Equal(t, tt.expectedID, channel.Id)
		})
	}
}

func TestGetRandomSatisfiedChannelSkipsExcludedChannelsAcrossThreeCandidates(t *testing.T) {
	const (
		groupName = "svip-exclude-test"
		modelName = "gpt-5.5-exclude-test"
	)

	restore := installRetrySelectionCacheFixture(t, groupName, modelName)
	t.Cleanup(restore)

	tests := []struct {
		name        string
		excludedIDs []int
		expectedID  int
		wantNil     bool
	}{
		{name: "no excluded channel selects highest priority", expectedID: 9101},
		{name: "excluded highest selects middle priority", excludedIDs: []int{9101}, expectedID: 9102},
		{name: "excluded highest and middle selects lowest priority", excludedIDs: []int{9101, 9102}, expectedID: 9103},
		{name: "excluded all candidates returns nil", excludedIDs: []int{9101, 9102, 9103}, wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel, err := GetRandomSatisfiedChannel(groupName, modelName, 0, "/v1/responses", tt.excludedIDs...)
			require.NoError(t, err)
			if tt.wantNil {
				require.Nil(t, channel)
				return
			}
			require.NotNil(t, channel)
			require.Equal(t, tt.expectedID, channel.Id)
		})
	}
}

func TestGetRandomSatisfiedChannelSkipsExcludedChannelsAcrossFiveCandidates(t *testing.T) {
	const (
		groupName = "svip-exclude-five-test"
		modelName = "gpt-5.5-exclude-five-test"
	)

	restore := installRetrySelectionCacheFixtureWithPriorities(t, groupName, modelName, []int64{50, 40, 30, 20, 10})
	t.Cleanup(restore)

	tests := []struct {
		name        string
		excludedIDs []int
		expectedID  int
		wantNil     bool
	}{
		{name: "selects first candidate", expectedID: 9101},
		{name: "selects second candidate after first exhausted", excludedIDs: []int{9101}, expectedID: 9102},
		{name: "selects third candidate after first two exhausted", excludedIDs: []int{9101, 9102}, expectedID: 9103},
		{name: "selects fourth candidate after first three exhausted", excludedIDs: []int{9101, 9102, 9103}, expectedID: 9104},
		{name: "selects fifth candidate after first four exhausted", excludedIDs: []int{9101, 9102, 9103, 9104}, expectedID: 9105},
		{name: "returns nil after all candidates exhausted", excludedIDs: []int{9101, 9102, 9103, 9104, 9105}, wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel, err := GetRandomSatisfiedChannel(groupName, modelName, 0, "/v1/responses", tt.excludedIDs...)
			require.NoError(t, err)
			if tt.wantNil {
				require.Nil(t, channel)
				return
			}
			require.NotNil(t, channel)
			require.Equal(t, tt.expectedID, channel.Id)
		})
	}
}

func TestGetRandomSatisfiedChannelContinuesAfterAffinityStartsFromSecondCandidate(t *testing.T) {
	const (
		groupName = "svip-affinity-second-test"
		modelName = "gpt-5.5-affinity-second-test"
	)

	restore := installRetrySelectionCacheFixtureWithPriorities(t, groupName, modelName, []int64{50, 40, 30, 20, 10})
	t.Cleanup(restore)

	tests := []struct {
		name        string
		excludedIDs []int
		expectedID  int
		wantNil     bool
	}{
		{name: "after second candidate is exhausted selects first candidate", excludedIDs: []int{9102}, expectedID: 9101},
		{name: "after second and first are exhausted selects third candidate", excludedIDs: []int{9102, 9101}, expectedID: 9103},
		{name: "continues to fourth candidate", excludedIDs: []int{9102, 9101, 9103}, expectedID: 9104},
		{name: "continues to fifth candidate", excludedIDs: []int{9102, 9101, 9103, 9104}, expectedID: 9105},
		{name: "returns nil after all candidates exhausted", excludedIDs: []int{9102, 9101, 9103, 9104, 9105}, wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel, err := GetRandomSatisfiedChannel(groupName, modelName, 0, "/v1/responses", tt.excludedIDs...)
			require.NoError(t, err)
			if tt.wantNil {
				require.Nil(t, channel)
				return
			}
			require.NotNil(t, channel)
			require.Equal(t, tt.expectedID, channel.Id)
		})
	}
}

func installRetrySelectionCacheFixture(t *testing.T, groupName string, modelName string) func() {
	t.Helper()

	return installRetrySelectionCacheFixtureWithPriorities(t, groupName, modelName, []int64{30, 20, 10})
}

func installRetrySelectionCacheFixtureWithPriorities(t *testing.T, groupName string, modelName string, priorities []int64) func() {
	t.Helper()

	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	channelSyncLock.Lock()
	oldGroup2Model2Channels := group2model2channels
	oldChannelsIDM := channelsIDM
	oldChannel2AdvancedCustomConfig := channel2advancedCustomConfig

	common.MemoryCacheEnabled = true
	zeroWeight := uint(0)
	channelIDs := make([]int, 0, len(priorities))
	nextChannelsIDM := make(map[int]*Channel, len(priorities))
	for i, priority := range priorities {
		channelID := 9101 + i
		channelIDs = append(channelIDs, channelID)
		priorityCopy := priority
		nextChannelsIDM[channelID] = &Channel{Id: channelID, Weight: &zeroWeight, Priority: &priorityCopy}
	}
	group2model2channels = map[string]map[string][]int{
		groupName: {
			modelName: channelIDs,
		},
	}
	channelsIDM = nextChannelsIDM
	channel2advancedCustomConfig = nil
	channelSyncLock.Unlock()

	return func() {
		channelSyncLock.Lock()
		group2model2channels = oldGroup2Model2Channels
		channelsIDM = oldChannelsIDM
		channel2advancedCustomConfig = oldChannel2AdvancedCustomConfig
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		channelSyncLock.Unlock()
	}
}
