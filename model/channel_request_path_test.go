package model

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

func TestChannelSupportsRequestPathRestrictsCodexWebSearch(t *testing.T) {
	t.Parallel()

	require.True(t, ChannelSupportsRequestPath(&Channel{Type: constant.ChannelTypeCodex}, "/v1/alpha/search", "gpt-5.6-sol"))
	require.False(t, ChannelSupportsRequestPath(&Channel{Type: constant.ChannelTypeOpenAI}, "/v1/alpha/search", "gpt-5.6-sol"))
	require.True(t, ChannelSupportsRequestPath(&Channel{Type: constant.ChannelTypeOpenAI}, "/v1/responses", "gpt-5.6-sol"))
}

func TestChannelSupportsRequestPathRestrictsCodexModelsCatalog(t *testing.T) {
	t.Parallel()

	require.True(t, ChannelSupportsRequestPath(&Channel{Type: constant.ChannelTypeCodex}, "/v1/models", "gpt-5.6-sol"))
	require.False(t, ChannelSupportsRequestPath(&Channel{Type: constant.ChannelTypeOpenAI}, "/v1/models", "gpt-5.6-sol"))
	require.True(t, ChannelSupportsRequestPath(&Channel{Type: constant.ChannelTypeOpenAI}, "/v1/models/gemini-2.5-pro:generateContent", "gemini-2.5-pro"))
}
