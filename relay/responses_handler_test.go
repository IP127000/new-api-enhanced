package relay

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestShouldUseCodexOriginalResponsesBodyRequiresCodexChannel(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:     appconstant.APITypeCodex,
			ChannelType: appconstant.ChannelTypeCodex,
		},
	}
	require.True(t, shouldUseCodexOriginalResponsesBody(info))

	info.ChannelType = appconstant.ChannelTypeOpenAI
	require.False(t, shouldUseCodexOriginalResponsesBody(info))

	info.ChannelType = appconstant.ChannelTypeCodex
	info.ApiType = appconstant.APITypeOpenAI
	require.False(t, shouldUseCodexOriginalResponsesBody(info))
}

func TestSanitizeCodexOriginalResponsesBodyAddsRequiredFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.6-sol","input":"hello","store":true,"temperature":0.7,"max_output_tokens":100,"future_field":{"keep":true}}`)
	got, changed, err := sanitizeCodexOriginalResponsesBody(body, false, "")
	require.NoError(t, err)
	require.True(t, changed)

	var decoded map[string]interface{}
	require.NoError(t, common.Unmarshal(got, &decoded))
	require.Equal(t, false, decoded["store"])
	require.Equal(t, "", decoded["instructions"])
	require.NotContains(t, decoded, "temperature")
	require.NotContains(t, decoded, "max_output_tokens")
	require.Equal(t, map[string]interface{}{"keep": true}, decoded["future_field"])
}

func TestSanitizeCodexOriginalResponsesBodyKeepsCompactParityFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.6-sol","store":true,"temperature":0.7,"max_output_tokens":100,"tools":[{"type":"custom"}]}`)
	got, changed, err := sanitizeCodexOriginalResponsesBody(body, true, "")
	require.NoError(t, err)
	require.True(t, changed)

	var decoded map[string]interface{}
	require.NoError(t, common.Unmarshal(got, &decoded))
	require.Equal(t, true, decoded["store"])
	require.Equal(t, 0.7, decoded["temperature"])
	require.EqualValues(t, 100, decoded["max_output_tokens"])
	require.Equal(t, "", decoded["instructions"])
	require.Contains(t, decoded, "tools")
}

func TestSanitizeCodexOriginalResponsesBodyReturnsOriginalWhenCompliant(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.6-sol","input":"hello","instructions":"","store":false,"future_field":{"keep":true}}`)
	got, changed, err := sanitizeCodexOriginalResponsesBody(body, false, "")
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)
	require.Same(t, &body[0], &got[0], "no-op sanitization must retain the original backing array")
}

func TestSanitizeCodexOriginalResponsesBodyAppliesMappedModel(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-client","input":"hello","instructions":"","store":false}`)
	got, changed, err := sanitizeCodexOriginalResponsesBody(body, false, "gpt-upstream")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "gpt-upstream", gjson.GetBytes(got, "model").String())
}

func TestSanitizeCodexOriginalResponsesBodyNoOpAllocationBound(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.6-sol","input":"` + strings.Repeat("x", 2<<20) + `","instructions":"","store":false}`)
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			got, changed, err := sanitizeCodexOriginalResponsesBody(body, false, "")
			if err != nil || changed || len(got) != len(body) {
				b.Fatalf("unexpected sanitization result: changed=%v err=%v", changed, err)
			}
		}
	})
	t.Logf("no-op sanitizer allocated %d bytes/op for a %d-byte request", result.AllocedBytesPerOp(), len(body))
	require.Less(t, result.AllocedBytesPerOp(), int64(64<<10),
		"no-op sanitization must not allocate in proportion to request size")
}
