// Replay golden HuJSON captures under testdata/sshtest_results/*.hujson:
// the 200 path requires stealthscale's evaluateSSHTests to pass; the
// non-200 path requires stealthscale to reject the same input with the
// captured error body as a substring. Divergences are listed in
// knownSSHTesterDivergences with the engine gap each represents.

package v2

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/types/testcapture"
)

// knownSSHTesterDivergences names the engine gap for each capture where
// stealthscale and upstream disagree.
var knownSSHTesterDivergences = map[string]string{
	"sshtest-malformed-dst-bare-ipv6": "bare-IPv6 sshTests dst: upstream parse-accepts then engine-rejects; stealthscale accepts (IPv4 mirror passes both sides)",
}

func TestSSHTesterCompat(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("testdata", "sshtest_results", "*.hujson"))
	require.NoError(t, err, "failed to glob test files")

	if len(files) == 0 {
		t.Skip("no sshtest captures yet")
	}

	users := setupSSHDataCompatUsers()

	for _, file := range files {
		c, err := testcapture.Read(file)
		require.NoError(t, err, "reading %s", file)

		t.Run(c.TestID, func(t *testing.T) {
			t.Parallel()

			if reason, skip := knownSSHTesterDivergences[c.TestID]; skip {
				t.Skip(reason)
			}

			// Each capture pins its own topology IPs; build nodes
			// from the capture so host-alias dsts resolve.
			nodes := buildGrantsNodesFromCapture(users, c)

			policyJSON := []byte(c.Input.FullPolicy)

			pm, parseErr := NewPolicyManager(policyJSON, users, nodes.ViewSlice())

			if c.Input.APIResponseCode == 200 {
				require.NoError(t, parseErr,
					"tailscale accepted this policy; stealthscale must parse it")

				_, setErr := pm.SetPolicy(policyJSON)
				require.NoError(t, setErr,
					"tailscale accepted this policy; stealthscale sshTests must pass")

				return
			}

			var got error

			switch {
			case parseErr != nil:
				got = parseErr
			default:
				_, setErr := pm.SetPolicy(policyJSON)
				got = setErr
			}

			require.Error(t, got, "tailscale rejected; stealthscale must reject too")

			if c.Input.APIResponseBody == nil || c.Input.APIResponseBody.Message == "" {
				return
			}

			want := c.Input.APIResponseBody.Message
			if !strings.Contains(got.Error(), want) {
				t.Errorf("error body mismatch\n  tailscale wants: %q\n  stealthscale got:   %q", want, got.Error())
			}
		})
	}
}
