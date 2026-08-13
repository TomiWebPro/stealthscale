package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv1 "github.com/tomiwebpro/stealthscale/gen/client/v1"
	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// TestServerInfoCommands covers the standalone server/info commands that do not
// fit the resource CRUD groups: `health`, `version` and
// `generate private-key`. They are exercised against a populated server (one
// user with a node) so `health` reports a real, connected database.
func TestServerInfoCommands(t *testing.T) {
	IntegrationSkip(t)

	scenario, stealthscale := setupCLIScenario(t, "cli-serverinfo", []string{"user1"}, 1)
	defer scenario.ShutdownAssertNoPanics(t)

	t.Run("health", func(t *testing.T) {
		health := assertJSONRoundtrip[*clientv1.HealthResponseBody](t, stealthscale, []string{
			"stealthscale", "health", "--output", "json",
		})
		assert.True(t, health.DatabaseConnectivity, "database should be reachable")
	})

	t.Run("version", func(t *testing.T) {
		info := assertJSONRoundtrip[types.VersionInfo](t, stealthscale, []string{
			"stealthscale", "version", "--output", "json",
		})
		assert.NotEmpty(t, info.Version, "version string should be populated")
		assert.NotEmpty(t, info.Go.Version, "go version should be populated")
	})

	t.Run("generate-private-key", func(t *testing.T) {
		key := assertJSONRoundtrip[map[string]string](t, stealthscale, []string{
			"stealthscale", "generate", "private-key", "--output", "json",
		})

		priv, ok := key["private_key"]
		require.True(t, ok, "output should contain a private_key field")
		assert.NotEmpty(t, priv, "generated private key should not be empty")
		assert.Contains(t, priv, "privkey:", "machine private key should carry the privkey: prefix")
	})
}
