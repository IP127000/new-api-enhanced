package constant

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPath2RelayModeCodexWebSearch(t *testing.T) {
	t.Parallel()
	require.Equal(t, RelayModeCodexWebSearch, Path2RelayMode("/v1/alpha/search"))
}
