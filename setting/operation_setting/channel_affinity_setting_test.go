package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCodexCliParamOverrideTemplateAddsOfficialDefault(t *testing.T) {
	t.Parallel()

	rule := ChannelAffinityRule{}
	require.True(t, normalizeCodexCliParamOverrideTemplate(&rule))

	operations, ok := rule.ParamOverrideTemplate["operations"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, operations, 1)
	require.Equal(t, "pass_headers", operations[0]["mode"])
	require.Equal(t, constant.CodexClientPassThroughHeaders(), operations[0]["value"])
	require.Equal(t, true, operations[0]["keep_origin"])
}

func TestNormalizeCodexCliParamOverrideTemplateMigratesUnsafeLegacySync(t *testing.T) {
	t.Parallel()

	rule := ChannelAffinityRule{
		ParamOverrideTemplate: map[string]interface{}{
			"operations": []interface{}{
				map[string]interface{}{
					"mode": "sync_fields",
					"from": "header:session-id",
					"to":   "json:prompt_cache_key",
				},
			},
		},
	}
	require.True(t, normalizeCodexCliParamOverrideTemplate(&rule))
	require.False(t, hasLegacyCodexPromptCacheSync(rule.ParamOverrideTemplate))
}

func TestNormalizeCodexCliParamOverrideTemplatePreservesCustomConfig(t *testing.T) {
	t.Parallel()

	custom := map[string]interface{}{"temperature": 0.2}
	rule := ChannelAffinityRule{ParamOverrideTemplate: custom}
	require.False(t, normalizeCodexCliParamOverrideTemplate(&rule))
	require.Equal(t, custom, rule.ParamOverrideTemplate)
}
