package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/stretchr/testify/require"
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
	got, err := sanitizeCodexOriginalResponsesBody(body, false)
	require.NoError(t, err)

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
	got, err := sanitizeCodexOriginalResponsesBody(body, true)
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, common.Unmarshal(got, &decoded))
	require.Equal(t, true, decoded["store"])
	require.Equal(t, 0.7, decoded["temperature"])
	require.EqualValues(t, 100, decoded["max_output_tokens"])
	require.Equal(t, "", decoded["instructions"])
	require.Contains(t, decoded, "tools")
}
