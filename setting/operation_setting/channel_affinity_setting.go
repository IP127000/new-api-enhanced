package operation_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/config"
)

type ChannelAffinityKeySource struct {
	Type string `json:"type"` // context_int, context_string, request_header, gjson
	Key  string `json:"key,omitempty"`
	Path string `json:"path,omitempty"`
}

type ChannelAffinityRule struct {
	Name             string                     `json:"name"`
	ModelRegex       []string                   `json:"model_regex"`
	PathRegex        []string                   `json:"path_regex"`
	UserAgentInclude []string                   `json:"user_agent_include,omitempty"`
	KeySources       []ChannelAffinityKeySource `json:"key_sources"`

	ValueRegex string `json:"value_regex"`
	TTLSeconds int    `json:"ttl_seconds"`

	ParamOverrideTemplate map[string]interface{} `json:"param_override_template,omitempty"`

	SkipRetryOnFailure bool `json:"skip_retry_on_failure"`

	IncludeUsingGroup bool `json:"include_using_group"`
	IncludeModelName  bool `json:"include_model_name"`
	IncludeRuleName   bool `json:"include_rule_name"`
}

type ChannelAffinitySetting struct {
	Enabled               bool                  `json:"enabled"`
	SwitchOnSuccess       bool                  `json:"switch_on_success"`
	KeepOnChannelDisabled bool                  `json:"keep_on_channel_disabled"`
	MaxEntries            int                   `json:"max_entries"`
	DefaultTTLSeconds     int                   `json:"default_ttl_seconds"`
	Rules                 []ChannelAffinityRule `json:"rules"`
}

var claudeCliPassThroughHeaders = []string{
	"X-Stainless-Arch",
	"X-Stainless-Lang",
	"X-Stainless-Os",
	"X-Stainless-Package-Version",
	"X-Stainless-Retry-Count",
	"X-Stainless-Runtime",
	"X-Stainless-Runtime-Version",
	"X-Stainless-Timeout",
	"User-Agent",
	"X-App",
	"Anthropic-Beta",
	"Anthropic-Dangerous-Direct-Browser-Access",
	"Anthropic-Version",
}

func buildPassHeaderTemplate(headers []string) map[string]interface{} {
	clonedHeaders := make([]string, 0, len(headers))
	clonedHeaders = append(clonedHeaders, headers...)
	return map[string]interface{}{
		"operations": []map[string]interface{}{
			{
				"mode":        "pass_headers",
				"value":       clonedHeaders,
				"keep_origin": true,
			},
		},
	}
}

func ensureCodexCliKeySources(rule *ChannelAffinityRule) bool {
	changed := false
	requiredSources := []ChannelAffinityKeySource{
		{Type: "gjson", Path: "prompt_cache_key"},
		{Type: "request_header", Key: "Session-Id"},
		{Type: "request_header", Key: "Session_id"},
		{Type: "request_header", Key: "Thread-Id"},
		{Type: "request_header", Key: "X-Client-Request-Id"},
	}
	for _, required := range requiredSources {
		found := false
		for _, src := range rule.KeySources {
			if strings.EqualFold(strings.TrimSpace(src.Type), required.Type) &&
				strings.EqualFold(strings.TrimSpace(src.Key), required.Key) &&
				strings.EqualFold(strings.TrimSpace(src.Path), required.Path) {
				found = true
				break
			}
		}
		if !found {
			rule.KeySources = append(rule.KeySources, required)
			changed = true
		}
	}
	return changed
}

func hasLegacyCodexPromptCacheSync(template map[string]interface{}) bool {
	rawOperations, ok := template["operations"]
	if !ok {
		return false
	}

	operations := make([]map[string]interface{}, 0)
	switch values := rawOperations.(type) {
	case []map[string]interface{}:
		operations = values
	case []interface{}:
		for _, value := range values {
			operation, ok := value.(map[string]interface{})
			if ok {
				operations = append(operations, operation)
			}
		}
	}

	for _, operation := range operations {
		mode, _ := operation["mode"].(string)
		if !strings.EqualFold(strings.TrimSpace(mode), "sync_fields") {
			continue
		}
		fromValue, _ := operation["from"].(string)
		toValue, _ := operation["to"].(string)
		from := strings.ToLower(strings.TrimSpace(fromValue))
		to := strings.ToLower(strings.TrimSpace(toValue))
		if to == "json:prompt_cache_key" && (from == "header:session-id" || from == "header:session_id") {
			return true
		}
	}
	return false
}

func normalizeCodexCliParamOverrideTemplate(rule *ChannelAffinityRule) bool {
	if len(rule.ParamOverrideTemplate) > 0 && !hasLegacyCodexPromptCacheSync(rule.ParamOverrideTemplate) {
		return false
	}
	rule.ParamOverrideTemplate = buildPassHeaderTemplate(constant.CodexClientPassThroughHeaders())
	return true
}

func NormalizeChannelAffinitySetting() bool {
	changed := false
	for i := range channelAffinitySetting.Rules {
		rule := &channelAffinitySetting.Rules[i]
		if !strings.EqualFold(strings.TrimSpace(rule.Name), "codex cli trace") {
			continue
		}
		if ensureCodexCliKeySources(rule) {
			changed = true
		}
		if normalizeCodexCliParamOverrideTemplate(rule) {
			changed = true
		}
	}
	return changed
}

var channelAffinitySetting = ChannelAffinitySetting{
	Enabled:               true,
	SwitchOnSuccess:       true,
	KeepOnChannelDisabled: false,
	MaxEntries:            100_000,
	DefaultTTLSeconds:     3600,
	Rules: []ChannelAffinityRule{
		{
			Name:       "codex cli trace",
			ModelRegex: []string{"^gpt-.*$"},
			PathRegex:  []string{"/v1/responses"},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "prompt_cache_key"},
				{Type: "request_header", Key: "Session-Id"},
				{Type: "request_header", Key: "Session_id"},
				{Type: "request_header", Key: "Thread-Id"},
				{Type: "request_header", Key: "X-Client-Request-Id"},
			},
			ValueRegex: "",
			TTLSeconds: 0,
			ParamOverrideTemplate: buildPassHeaderTemplate(
				constant.CodexClientPassThroughHeaders(),
			),
			SkipRetryOnFailure: true,
			IncludeUsingGroup:  true,
			IncludeRuleName:    true,
			UserAgentInclude:   nil,
		},
		{
			Name:       "claude cli trace",
			ModelRegex: []string{"^claude-.*$"},
			PathRegex:  []string{"/v1/messages"},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "metadata.user_id"},
			},
			ValueRegex:            "",
			TTLSeconds:            0,
			ParamOverrideTemplate: buildPassHeaderTemplate(claudeCliPassThroughHeaders),
			SkipRetryOnFailure:    true,
			IncludeUsingGroup:     true,
			IncludeRuleName:       true,
			UserAgentInclude:      nil,
		},
	},
}

func init() {
	config.GlobalConfig.Register("channel_affinity_setting", &channelAffinitySetting)
}

func GetChannelAffinitySetting() *ChannelAffinitySetting {
	return &channelAffinitySetting
}
