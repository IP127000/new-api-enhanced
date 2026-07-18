package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithoutCompactModelSuffix(t *testing.T) {
	t.Parallel()

	base, ok := WithoutCompactModelSuffix("gpt-5.6-sol-openai-compact")
	require.True(t, ok)
	require.Equal(t, "gpt-5.6-sol", base)

	base, ok = WithoutCompactModelSuffix("gpt-5.6-sol")
	require.False(t, ok)
	require.Equal(t, "gpt-5.6-sol", base)
}
