package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestSanitizeCodexWebSearchBody(t *testing.T) {
	t.Parallel()

	body := []byte(`{"id":"search-1","model":"gpt-5.6-sol","prompt_cache_key":"cache","prompt_cache_retention":"24h","commands":{"search_query":[{"q":"OpenAI"}]},"future_field":{"keep":true}}`)
	got, err := sanitizeCodexWebSearchBody(body)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, common.Unmarshal(got, &decoded))
	require.NotContains(t, decoded, "prompt_cache_key")
	require.NotContains(t, decoded, "prompt_cache_retention")
	require.Equal(t, map[string]any{"keep": true}, decoded["future_field"])
	require.Equal(t, "search-1", decoded["id"])
}
