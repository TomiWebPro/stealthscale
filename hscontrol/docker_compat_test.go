// Copyright (c) 2026 TomiWebPro <TomiWebPro@gmail.com>
// SPDX-License-Identifier: BSD-3-Clause

package hscontrol_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
)

// TestDockerGoreleaserKosEnabled ensures the Docker compatibility layer for alpha.4
// is not regressed: .goreleaser.yml must publish ghcr.io/tomiwebpro/stealthscale
// via ko (distroless) for linux/amd64 and linux/arm64.
func TestDockerGoreleaserKosEnabled(t *testing.T) {
	data, err := os.ReadFile("../.goreleaser.yml")
	require.NoError(t, err, "read .goreleaser.yml")
	text := string(data)

	// kos must be enabled (not commented) for alpha.4
	require.Contains(t, text, "kos:")
	assert.NotContains(t, text, "# kos:", "kos must not be commented for alpha.4")
	require.Contains(t, text, "ghcr.io/tomiwebpro/stealthscale")
	require.Contains(t, text, "bare: true")
	require.Contains(t, text, "gcr.io/distroless/base-debian13")
	require.Contains(t, text, "linux/amd64")
	require.Contains(t, text, "linux/arm64")
	require.Contains(t, text, "sha-{{ .ShortCommit }}")
	// prerelease handling for alpha (unstable)
	require.Contains(t, text, `stable{{ else }}unstable`)
}

// TestDockerGoreleaserBuildsAllPlatforms ensures goreleaser still builds all 11 targets
// including linux_arm_6/7 for Raspberry Pi, plus darwin/windows/freebsd.
func TestDockerGoreleaserBuildsAllPlatforms(t *testing.T) {
	data, err := os.ReadFile("../.goreleaser.yml")
	require.NoError(t, err)
	text := string(data)
	for _, target := range []string{
		"linux_amd64", "linux_arm64", "linux_arm_6", "linux_arm_7", "linux_arm",
		"darwin_amd64", "darwin_arm64", "windows_amd64", "windows_arm64", "freebsd_amd64", "freebsd_arm64",
	} {
		assert.Contains(t, text, target, "goreleaser must build %s", target)
	}
}

// TestDockerfileProduction verifies the production Dockerfile exists and is Docker-compatible.
func TestDockerfileProduction(t *testing.T) {
	data, err := os.ReadFile("../Dockerfile")
	require.NoError(t, err, "Dockerfile must exist for alpha.4 docker compatibility")
	text := string(data)
	assert.Contains(t, text, "FROM", "Dockerfile must have a base image")
	assert.Contains(t, text, "stscale", "Dockerfile must build stscale binary")
	assert.Contains(t, text, "ln -sf", "Dockerfile must create stealthscale symlink for compat")
	assert.Contains(t, text, "HEALTHCHECK", "Dockerfile must have HEALTHCHECK with stscale health")
	assert.Contains(t, text, "8080", "Dockerfile must expose 8080")
	assert.Contains(t, text, "9090", "Dockerfile must expose 9090")
	assert.Contains(t, text, "10001", "Dockerfile must expose VLESS range 10001-10100")
	assert.Contains(t, text, "VOLUME", "Dockerfile must declare VOLUME for /var/lib/stealthscale")
	assert.Contains(t, text, "/var/lib/stealthscale", "Dockerfile must handle persistent state")
}

// TestDockerfileIntegrationCompat verifies Dockerfile.integration is Docker-compatible and
// includes the stscale symlink, VLESS ports, and healthcheck required for integration tests.
func TestDockerfileIntegrationCompat(t *testing.T) {
	data, err := os.ReadFile("../Dockerfile.integration")
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, "/usr/local/bin/stscale")
	assert.Contains(t, text, "stealthscale", "must keep symlink for legacy integration tests")
	assert.Contains(t, text, "10001", "must expose VLESS range")
	assert.Contains(t, text, "HEALTHCHECK")
	assert.Contains(t, text, "VOLUME")
}

// TestDockerfileIntegrationCICompat verifies Dockerfile.integration-ci matches production expectations.
func TestDockerfileIntegrationCICompat(t *testing.T) {
	data, err := os.ReadFile("../Dockerfile.integration-ci")
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, "/usr/local/bin/stscale")
	assert.Contains(t, text, "10001")
}

// TestDockerConfigEnvPrefix ensures the config loader uses STEALTHSCALE_ prefix and
// maps dots to underscores, so docker-compose `environment: STEALTHSCALE_XRAY_SECURITY` works.
func TestDockerConfigEnvPrefix(t *testing.T) {
	// Verify LoadConfig sets STEALTHSCALE prefix (read the source, not runtime env)
	data, err := os.ReadFile("types/config.go")
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, `SetEnvPrefix`, "must use STEALTHSCALE prefix for docker env")
	assert.Contains(t, text, `stealthscale`, "must use stealthscale prefix")
	assert.Contains(t, text, `SetEnvKeyReplacer`, "must map dots to underscores for env")
	assert.Contains(t, text, `AutomaticEnv`, "must enable AutomaticEnv for docker")

	// Also verify that the essential VLESS/XRay envs are handled via viper defaults
	assert.Contains(t, text, `xray.enabled`)
	assert.Contains(t, text, `xray.listen_port`)
	assert.Contains(t, text, `xray.secret`)
}

// TestDockerXRaySecretRequiredForPostgres ensures validation that prevents silent identity loss
// when running in docker with postgres (no local .xray_secret file).
func TestDockerXRaySecretRequiredForPostgres(t *testing.T) {
	data, err := os.ReadFile("types/config.go")
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, `database.type`) // must gate on postgres
	assert.Contains(t, text, `xray.secret is required when database.type is postgres`)
	assert.Contains(t, text, `openssl rand -hex 32`)
}

// TestDockerVolumesAndStateDir ensures the types handle stateDir from sqlite path
// and that docker's /var/lib/stealthscale is the default, with proper permissions handling.
func TestDockerVolumesAndStateDir(t *testing.T) {
	// Check GetStealthScaleConfig and loadOrCreateSecret logic
	data, err := os.ReadFile("types/config.go")
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, "loadOrCreateSecret")
	assert.Contains(t, text, ".xray_secret", "secret must be persisted next to DB for volume")
	assert.Contains(t, text, "MkdirAll", "must create stateDir for docker volumes")
	assert.Contains(t, text, "/var/lib/stealthscale/db.sqlite", "default sqlite path must be in /var/lib/stealthscale for docker")

	// Ensure the secret handling covers both sqlite and postgres paths
	assert.Contains(t, text, `if scfg.Database.Type == "sqlite3"`)
}

// TestDockerUnixSocket ensures the unix socket path is compatible with docker's tmpfs
// and that EnsureDir is called (permissions handled for container breakout).
func TestDockerUnixSocket(t *testing.T) {
	data, err := os.ReadFile("app_unix.go")
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, "ensureUnixSocketIsAbsent")
	assert.Contains(t, text, "listenSocket")
	assert.Contains(t, text, "EnsureDir")
	// Check that socket path default is /var/run/stealthscale for linux
	cfgData, err := os.ReadFile("types/config.go")
	require.NoError(t, err)
	assert.Contains(t, string(cfgData), "/var/run/stealthscale/stealthscale.sock")
}

// TestDockerHealthEndpoint ensures the health endpoint is exposed for docker HEALTHCHECK.
func TestDockerHealthEndpoint(t *testing.T) {
	data, err := os.ReadFile("app.go")
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, "/health", "app must expose /health for docker HEALTHCHECK")

	cliData, err := os.ReadFile("../cmd/stealthscale/cli/health.go")
	require.NoError(t, err)
	assert.Contains(t, string(cliData), "health", "CLI must have health command for HEALTHCHECK")

	// Dockerfile healthcheck must use stscale health (not curl)
	dockerfile, err := os.ReadFile("../Dockerfile")
	require.NoError(t, err)
	assert.Contains(t, string(dockerfile), `stscale", "health"`)
}

// TestDockerComposeExample ensures docs and packaging provide correct docker-compose guidance
// including VLESS port range, volumes, and xray.secret handling.
func TestDockerComposeExample(t *testing.T) {
	// Check container.md docs still have valid docker run with VLESS range
	data, err := os.ReadFile("../docs/setup/install/container.md")
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, "10001-10100", "container docs must expose VLESS range")
	assert.Contains(t, text, "ghcr.io/tomiwebpro/stealthscale", "docs must reference ghcr")
	assert.Contains(t, text, "/var/lib/stealthscale", "docs must document persistent volume")
	// After alpha.4, the warning about kos disabled must be gone or updated to say enabled
	assert.NotContains(t, text, "kos disabled", "alpha.4 enables kos, warning must be removed or updated")
	// Check that xray.secret handling is documented
	assert.Contains(t, strings.ToLower(text), "xray.secret", "must document xray.secret for postgres in docker")
}

// TestDockerLabelsAndEnv ensures hsic integration harness uses correct binary for docker.
func TestDockerLabelsAndEnv(t *testing.T) {
	data, err := os.ReadFile("../integration/hsic/hsic.go")
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, `binStealthScale                  = "stscale"`, "hsic must use stscale binary for docker")
	assert.Contains(t, text, "/usr/local/bin/stscale serve", "hsic entrypoint must use stscale")
	// Postgres env should not use binary name for user
	assert.Contains(t, text, `"stealthscale"`, "postgres user should be stealthscale literal, not binary")
}

// TestDockerVLESSDefaults ensures default VLESS Reality config is sensible for docker.
func TestDockerVLESSDefaults(t *testing.T) {
	// Load defaults via LoadConfig with empty path
	err := types.LoadConfig("", false)
	require.NoError(t, err)
	// Check defaults are set in source (not via viper env)
	data, err := os.ReadFile("types/config.go")
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, `viper.SetDefault("xray.listen_addr", "0.0.0.0")`)
	assert.Contains(t, text, `viper.SetDefault("xray.listen_port", 10001)`)
	assert.Contains(t, text, `viper.SetDefault("xray.max_listen_port", 10100)`)
	assert.Contains(t, text, `viper.SetDefault("xray.security", "reality_xtls")`)
	assert.Contains(t, text, `viper.SetDefault("xray.reality.dest", "www.cloudflare.com:443")`)
}
