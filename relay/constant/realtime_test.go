package constant

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPath2RelayModeRealtimePaths(t *testing.T) {
	require.Equal(t, RelayModeRealtime, Path2RelayMode("/v1/realtime"))
	require.Equal(t, RelayModeRealtime, Path2RelayMode("/v1/live"))
}
