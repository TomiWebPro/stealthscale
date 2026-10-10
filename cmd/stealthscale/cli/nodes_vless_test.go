package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestNodeVLESSCommandHasAddressFlag pins the --address override on
// `stscale nodes vless`: the command's own warning tells operators to "use
// --address override" when xray.listen_addr is 0.0.0.0, so the flag must
// exist or the advice is unactionable.
func TestNodeVLESSCommandHasAddressFlag(t *testing.T) {
	var found *cobra.Command

	for _, c := range nodeCmd.Commands() {
		if c.Name() == "vless" {
			found = c
		}
	}

	require.NotNil(t, found, "nodes must have a vless subcommand")

	flag := found.Flags().Lookup("address")
	require.NotNil(t, flag, "nodes vless must define --address (referenced by its own 0.0.0.0 warning)")
	require.Empty(t, flag.DefValue, "--address must default to empty (fall back to xray.listen_addr)")
}
