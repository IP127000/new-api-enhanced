package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestHandleConfigUpdateNormalizesLegacyCodexAffinityRules(t *testing.T) {
	cfg := config.GlobalConfig.Get("channel_affinity_setting")
	require.NotNil(t, cfg)

	originalConfigMap, err := config.ConfigToMap(cfg)
	require.NoError(t, err)

	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		require.NoError(t, config.UpdateConfigFromMap(cfg, originalConfigMap))
		operation_setting.NormalizeChannelAffinitySetting()
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	legacyRules := `[{"name":"codex cli trace","model_regex":["^gpt-.*$"],"path_regex":["/v1/responses"],"key_sources":[{"type":"gjson","path":"prompt_cache_key"}],"param_override_template":{"operations":[{"mode":"sync_fields","from":"header:session-id","to":"json:prompt_cache_key"},{"mode":"sync_fields","from":"header:session_id","to":"json:prompt_cache_key"},{"mode":"pass_headers","value":["Originator","Session_id"],"keep_origin":true}]},"ttl_seconds":0,"skip_retry_on_failure":true,"include_using_group":true,"include_model_name":false,"include_rule_name":true}]`

	require.True(t, handleConfigUpdate("channel_affinity_setting.rules", legacyRules))

	common.OptionMapRWMutex.RLock()
	normalizedRules := common.OptionMap["channel_affinity_setting.rules"]
	common.OptionMapRWMutex.RUnlock()

	require.NotContains(t, normalizedRules, "sync_fields")
	require.Contains(t, normalizedRules, "pass_headers")
	require.Contains(t, normalizedRules, "X-Codex-Turn-State")
	require.NotContains(t, normalizedRules, "Authorization")
	require.Contains(t, normalizedRules, "Session-Id")
	require.Contains(t, normalizedRules, "Session_id")
	require.Contains(t, normalizedRules, "Thread-Id")
	require.Contains(t, normalizedRules, "X-Client-Request-Id")
	require.Contains(t, normalizedRules, "request_header")
}
