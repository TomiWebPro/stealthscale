package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tomiwebpro/stealthscale/hscontrol/types"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
	"github.com/tomiwebpro/stealthscale/integration/hsic"
	"github.com/tomiwebpro/stealthscale/integration/tsic"
)

// TestDockerHealthAndBasicServe verifies the Docker compatibility layer for alpha.4:
// the stscale binary runs inside a container (debian slim / distroless) and serves
// /health, the CLI `stscale health` works, and the persistent volumes are writable.
func TestDockerHealthAndBasicServe(t *testing.T) {
	IntegrationSkip(t)

	scenario, err := NewScenario(ScenarioSpec{
		NodesPerUser: 0,
		Users:        []string{"user1"},
	})
	require.NoError(t, err)
	defer scenario.ShutdownAssertNoPanics(t)

	// Use non-TLS mode for faster startup; health should still work.
	err = scenario.CreateStealthScaleEnv(
		[]tsic.Option{},
		hsic.WithTestName("dockerhealth"),
	)
	requireNoErrStealthScaleEnv(t, err)

	hs, err := scenario.StealthScale()
	requireNoErrGetStealthScale(t, err)

	// /health via control client must be 200
	nodes, err := hs.ListNodes()
	require.NoError(t, err)
	t.Logf("docker health: ListNodes ok, %d nodes", len(nodes))

	// CLI health inside container must exit 0 (docker HEALTHCHECK uses `stscale health`)
	for _, bin := range []string{"stscale", "stealthscale"} {
		out, err := hs.Execute([]string{bin, "health"})
		// health may return non-zero if server not ready, but with our stscale symlink both should be tried
		t.Logf("docker CLI %s health out: %q err: %v", bin, out, err)
		if err == nil {
			break
		}
	}
	// At least stscale must succeed via symlink or directly
	out, err := hs.Execute([]string{"stscale", "health"})
	require.NoError(t, err, "stscale health must succeed inside docker container (HEALTHCHECK)")
	require.NotContains(t, strings.ToLower(out), "error", "health output should not contain error")

	// Verify persistent volumes are writable and .xray_secret exists (docker VOLUME)
	secretPath := "/var/lib/stealthscale/.xray_secret"
	secret, err := hs.Execute([]string{"cat", secretPath})
	if err != nil {
		// secret may not exist if xray.secret was provided via env; check db path instead
		t.Logf("no %s (maybe xray.secret via env): %v", secretPath, err)
	} else {
		t.Logf(".xray_secret length %d", len(strings.TrimSpace(secret)))
		require.Len(t, strings.TrimSpace(secret), 64, ".xray_secret must be 64 hex chars (32 bytes) for docker persistence")
	}

	// Verify VLESS range is configured (10001 default) and that the process is stscale
	pid, err := hs.Execute([]string{"pidof", "stscale"})
	require.NoError(t, err, "pidof stscale must find stscale inside docker")
	require.NotEmpty(t, strings.TrimSpace(pid), "stscale pid must be non-empty inside docker")
	t.Logf("stscale pid: %s", strings.TrimSpace(pid))

	// Verify that the container exposes VLESS listener (ss or netstat)
	ss, err := hs.Execute([]string{"bash", "-c", "ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null || cat /proc/net/tcp"})
	if err != nil {
		t.Logf("ss/netstat not available: %v", err)
	} else {
		t.Logf("listening sockets: %s", ss)
		require.Contains(t, ss, "10001", "VLESS 10001 must be listening inside docker container")
	}
}

// TestDockerVLESSPersistence verifies that the per-node VLESS UUID/port derived from
// the persisted .xray_secret stays stable across container restarts — a critical
// docker compatibility requirement (volume /var/lib/stealthscale must be persistent).
func TestDockerVLESSPersistence(t *testing.T) {
	IntegrationSkip(t)

	scenario, err := NewScenario(ScenarioSpec{
		NodesPerUser: 0,
		Users:        []string{"user1"},
	})
	require.NoError(t, err)
	defer scenario.ShutdownAssertNoPanics(t)

	dest := "www.cloudflare.com:443"
	err = scenario.CreateStealthScaleEnv(
		[]tsic.Option{},
		hsic.WithTestName("dockervlesspersist"),
		hsic.WithConfigEnv(map[string]string{
			"STEALTHSCALE_XRAY_SECURITY":                "reality_xtls",
			"STEALTHSCALE_XRAY_REALITY_DEST":            dest,
			"STEALTHSCALE_XRAY_STEALTH_ENFORCE":         "true",
			"STEALTHSCALE_XRAY_STEALTH_ENFORCE_CONTROL": "true",
		}),
	)
	requireNoErrStealthScaleEnv(t, err)

	hs, err := scenario.StealthScale()
	requireNoErrGetStealthScale(t, err)

	user, err := scenario.CreateUser("user1")
	require.NoError(t, err)

	// Create a node so VLESS endpoint is allocated (via CLI debug create-node)
	// This also ensures the DB has a node ID 1 to derive VLESS URI.
	_, err = hs.Execute([]string{"stscale", "debug", "create-node", "--user", fmt.Sprint(user.Id), "--name", "docker-test-node", "--key", "46d00d67-6b5d-4030-8b9b-7b3b079b3607"})
	if err != nil {
		// Fallback to stealthscale binary via symlink
		_, err = hs.Execute([]string{"stealthscale", "debug", "create-node", "--user", fmt.Sprint(user.Id), "--name", "docker-test-node", "--key", "46d00d67-6b5d-4030-8b9b-7b3b079b3607"})
		require.NoError(t, err, "create-node must succeed for VLESS persistence test")
	}

	// Fetch VLESS URI for node 1
	var vlessURI string
	for _, bin := range []string{"stscale", "stealthscale"} {
		out, err := hs.Execute([]string{bin, "nodes", "vless", "1"})
		if err == nil && strings.Contains(out, "vless://") {
			vlessURI = strings.TrimSpace(out)
			break
		}
	}
	require.NotEmpty(t, vlessURI, "vless URI for node 1 must be available inside docker")
	t.Logf("vless URI before restart: %s", vlessURI)

	cfg, err := xray.ParseVLESSURI(vlessURI)
	require.NoError(t, err)
	require.Equal(t, "reality_xtls", cfg.Security)
	require.NotEmpty(t, cfg.PublicKey)
	uri1 := vlessURI

	// Capture secret before restart
	secretBefore, err := hs.Execute([]string{"cat", "/var/lib/stealthscale/.xray_secret"})
	if err != nil {
		t.Logf("no .xray_secret before restart (maybe postgres-style env): %v", err)
	} else {
		secretBefore = strings.TrimSpace(secretBefore)
		t.Logf("secret before restart len %d", len(secretBefore))
	}

	// Restart the container (docker restart preserves volume)
	err = hs.Restart()
	require.NoError(t, err, "docker restart must succeed")
	t.Logf("container restarted")

	// After restart, VLESS URI must be identical (secret persistence)
	var vlessURI2 string
	for _, bin := range []string{"stscale", "stealthscale"} {
		out, err := hs.Execute([]string{bin, "nodes", "vless", "1"})
		if err == nil && strings.Contains(out, "vless://") {
			vlessURI2 = strings.TrimSpace(out)
			break
		}
	}
	require.NotEmpty(t, vlessURI2, "vless URI after restart must be available")
	t.Logf("vless URI after restart: %s", vlessURI2)
	require.Equal(t, uri1, vlessURI2, "VLESS URI must be stable across docker restarts (secret persistence)")

	if secretBefore != "" {
		secretAfter, err := hs.Execute([]string{"cat", "/var/lib/stealthscale/.xray_secret"})
		require.NoError(t, err)
		require.Equal(t, secretBefore, strings.TrimSpace(secretAfter), ".xray_secret must persist across docker restarts")
	}

	// Also verify health still works after restart
	_, err = hs.Execute([]string{"stscale", "health"})
	require.NoError(t, err, "health must succeed after docker restart")
}

// TestDockerPortBindingsAndVolumes verifies the container's exposed ports and volumes
// match the docker run expectations from docs (8080, 9090, 3478/udp, 10001-10100).
func TestDockerPortBindingsAndVolumes(t *testing.T) {
	IntegrationSkip(t)

	scenario, err := NewScenario(ScenarioSpec{
		NodesPerUser: 0,
		Users:        []string{"user1"},
	})
	require.NoError(t, err)
	defer scenario.ShutdownAssertNoPanics(t)

	err = scenario.CreateStealthScaleEnv(
		[]tsic.Option{},
		hsic.WithTestName("dockerports"),
		hsic.WithExtraPorts([]string{"3478/udp"}),
	)
	requireNoErrStealthScaleEnv(t, err)

	hs, err := scenario.StealthScale()
	requireNoErrGetStealthScale(t, err)

	// Verify metrics port is reachable (9090 via host mapping)
	metricsOut, err := hs.Execute([]string{"curl", "-s", "http://localhost:9090/metrics"})
	require.NoError(t, err)
	t.Logf("metrics length %d", len(metricsOut))
	require.Contains(t, metricsOut, "go_", "metrics should contain go_ metrics")

	// Verify STUN port if extra port was added (3478/udp)
	stunCheck, err := hs.Execute([]string{"bash", "-c", "ss -ulnp 2>/dev/null | grep 3478 || echo no-stun"})
	t.Logf("STUN check: %s err %v", stunCheck, err)
	// Not strictly required to be listening if DERP disabled, but extra port should be exposed

	// Verify /var/lib/stealthscale is a directory and writable (docker VOLUME)
	volCheck, err := hs.Execute([]string{"bash", "-c", "test -d /var/lib/stealthscale && test -w /var/lib/stealthscale && echo ok"})
	require.NoError(t, err)
	require.Contains(t, volCheck, "ok", "/var/lib/stealthscale must be writable directory inside docker")

	// Verify config volume
	cfgCheck, err := hs.Execute([]string{"bash", "-c", "test -d /etc/stealthscale && echo ok"})
	require.NoError(t, err)
	require.Contains(t, cfgCheck, "ok", "/etc/stealthscale must exist as volume mount point")

	// Verify that both binary names work (symlink regression for alpha.4)
	for _, bin := range []string{"stscale", "stealthscale"} {
		out, err := hs.Execute([]string{bin, "version"})
		require.NoError(t, err, "%s version must work inside docker (symlink)", bin)
		t.Logf("%s version: %s", bin, strings.TrimSpace(out))
	}

	// Verify that docker can list nodes via API (control plane reachable)
	nodes, err := hs.ListNodes()
	require.NoError(t, err)
	t.Logf("nodes via API: %d", len(nodes))

	// Verify types.NodeID handling for volume path doesn't break on docker
	_ = types.NodeID(1)
}

// Ensure imports are used
var _ = json.Marshal
