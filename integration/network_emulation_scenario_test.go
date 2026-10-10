package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/hscontrol/xray"
	"github.com/tomiwebpro/stealthscale/integration/dockertestutil"
	"github.com/tomiwebpro/stealthscale/integration/hsic"
	"github.com/tomiwebpro/stealthscale/integration/integrationutil"
	"github.com/tomiwebpro/stealthscale/integration/tsic"
	"golang.org/x/net/http2"
	"tailscale.com/control/controlbase"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/netmap"
)

// TestVLESSRealityRegistrationUnderNetem exercises the deployable
// stealth transport promise from docs/stealthscale/overview.md
// ("VLESS+Reality must work in real, degraded networks"): a full
// Reality handshake, noise handshake and machine registration are
// performed through a node's VLESS endpoint while the control
// server container is behind injected latency, jitter and packet
// loss (dockertestutil.NetworkEmulation).
//
// One node bootstraps over the standard control path first —
// first-time discovery is allowed to be non-stealth (see
// docs/stealthscale/clients.md, "Discovery vs stealth") — which
// also brings up the per-node VLESS listener the second
// registration dials.
func TestVLESSRealityRegistrationUnderNetem(t *testing.T) {
	IntegrationSkip(t)
	t.Parallel()

	scenario, err := NewScenario(ScenarioSpec{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		NodesPerUser: 1,
		Users:        []string{"user1"},
	})
	require.NoError(t, err)

	defer scenario.ShutdownAssertNoPanics(t)

	err = scenario.CreateStealthScaleEnv(
		[]tsic.Option{},
		hsic.WithTestName("vlessnetem"),
		hsic.WithConfigEnv(map[string]string{
			"STEALTHSCALE_XRAY_SECURITY":     "reality_xtls",
			"STEALTHSCALE_XRAY_REALITY_DEST": "www.cloudflare.com:443",
			// viper splits env-var lists on whitespace, not
			// commas — ServerNames must be space-separated.
			"STEALTHSCALE_XRAY_REALITY_SERVER_NAMES": "www.cloudflare.com www.microsoft.com cloudflare.com microsoft.com",
			"STEALTHSCALE_XRAY_UTLS_FINGERPRINT":     "chrome",
			"STEALTHSCALE_XRAY_STEALTH_ENFORCE":      "true",
			// The bootstrap node is a stock tailscale client,
			// which registers over the legacy /ts2021 Noise
			// path (first-time discovery is allowed to be
			// non-stealth — see docs/stealthscale/clients.md,
			// "Discovery vs stealth"). enforce_control=true
			// would remove that endpoint and only allow
			// VLESS-capable clients to register at all.
			"STEALTHSCALE_XRAY_STEALTH_ENFORCE_CONTROL": "false",
		}),
	)
	requireNoErrStealthScaleEnv(t, err)

	stealthscale, err := scenario.StealthScale()
	requireNoErrGetStealthScale(t, err)

	allClients, err := scenario.ListTailscaleClients()
	requireNoErrListClients(t, err)
	require.Len(t, allClients, 1)
	bootstrap := allClients[0]

	// The bootstrap node registered over the standard control path,
	// so its per-node VLESS listener is now serving.
	var bootstrapID string

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		nodes, err := stealthscale.ListNodes()
		assert.NoError(c, err)
		assert.Len(c, nodes, 1)

		for _, node := range nodes {
			if node.Name == bootstrap.Hostname() {
				bootstrapID = node.Id
			}
		}

		assert.NotEmpty(c, bootstrapID, "bootstrap node should be registered")
	}, integrationutil.ScaledTimeout(30*time.Second), time.Second, "bootstrap node should be registered")

	// The VLESS endpoint for the bootstrap node, computed
	// deterministically by the coordinator (stscale nodes vless).
	vlessOut, err := stealthscale.Execute(
		[]string{"stealthscale", "nodes", "vless", bootstrapID, "--output", "json"},
	)
	require.NoError(t, err)

	var vlessInfo struct {
		URI string `json:"uri"`
	}
	require.NoError(t, json.Unmarshal([]byte(vlessOut), &vlessInfo))
	require.NotEmpty(t, vlessInfo.URI, "nodes vless --output json should return a uri")

	cfg, err := xray.ParseVLESSURI(vlessInfo.URI)
	require.NoError(t, err)
	require.Equal(t, "reality_xtls", cfg.Security)
	require.NotEmpty(t, cfg.PublicKey, "Reality public key must be in URI")
	require.NotEmpty(t, cfg.ShortID)

	network, err := scenario.Network("default")
	require.NoError(t, err)

	// Degrade the path to the control server in both directions:
	// 200ms base latency, 50ms jitter and 5% packet loss.
	emul := dockertestutil.NewNetworkEmulation(
		scenario.Pool(), network, stealthscale.GetHostname(),
	)
	require.NoError(t, emul.Apply(dockertestutil.NetworkConditions{
		Latency:      200 * time.Millisecond,
		Jitter:       50 * time.Millisecond,
		DropPct:      5,
		DuplicatePct: 0,
		CorrPct:      0,
	}))
	t.Cleanup(emul.ResetAll)

	// The URI advertises the container-internal listen address
	// (0.0.0.0); dial the server's address on the test network
	// instead, exactly like a client handed the endpoint would.
	cfg.Address = stealthscale.GetIPInNetwork(network)
	cfg.Timeout = 60 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Reality handshake over the degraded network.
	conn, err := xray.DialVLESS(ctx, cfg)
	require.NoError(t, err, "Reality handshake must succeed under 200ms latency, 50ms jitter and 5% loss")

	defer conn.Close()

	machineKey := key.NewMachine()
	nodeKey := key.NewNode()

	// The server's noise public key, fetched the way a real client
	// fetches it — through the degraded network.
	httpClient := stealthscaleHTTPClient(t, stealthscale)
	controlKey, err := fetchNoiseKey(ctx, httpClient, stealthscale.GetEndpoint())
	require.NoError(t, err)

	// A fresh pre-auth key for the registration.
	var userID uint64

	users, err := stealthscale.ListUsers()
	require.NoError(t, err)

	for _, user := range users {
		if user.Name == "user1" {
			userID = mustParseID(user.Id)
		}
	}

	require.NotZero(t, userID, "user1 should exist")

	pak, err := scenario.CreatePreAuthKey(userID, true, false)
	require.NoError(t, err)

	// Full registration through the VLESS stream: noise handshake,
	// then the machine/register API over HTTP/2.
	resp := registerOverVLESS(t, ctx, conn, machineKey, controlKey, tailcfg.RegisterRequest{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		Version: tailcfg.CurrentCapabilityVersion,
		NodeKey: nodeKey.Public(),
		Auth:    &tailcfg.RegisterResponseAuth{AuthKey: pak.Key}, //nolint:exhaustruct_v5 // test fixture
		Hostinfo: &tailcfg.Hostinfo{ //nolint:exhaustruct_v5 // test fixture
			Hostname: "vless-netem-node",
		},
	})
	require.True(t, resp.MachineAuthorized, "registration over VLESS under degraded network must be authorized")
	require.False(t, resp.NodeKeyExpired)
	require.Empty(t, resp.Error)

	// The registered node is visible to the control plane.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		nodes, err := stealthscale.ListNodes()
		assert.NoError(c, err)
		assert.Len(c, nodes, 2)

		found := false

		for _, node := range nodes {
			if node.Name == "vless-netem-node" {
				found = true
			}
		}

		assert.True(c, found, "node registered over VLESS should appear in the node list")
	}, integrationutil.ScaledTimeout(30*time.Second), time.Second, "VLESS-registered node should be visible")
}

// TestPartitionControlPlaneReconnect exercises the
// control-plane disconnect failure mode under a network
// partition: a node whose egress to the test network is
// blackholed (dockertestutil.Partition) cannot exchange
// packets with the coordinator or its peer — its half of
// every tailnet connection is severed.
//
// The control plane only learns of the disconnect when its
// kernel gives up retransmitting to the now half-open
// long-poll (TCP retransmission exhaustion, ~15 min — the
// server's keepalive writes are still delivered, only the
// node's ACKs are dropped). Within the test window the node
// therefore stays "online" in the control plane, the same
// physics the HA cable-pull scenario documents
// (TestHASubnetRouterFailoverBothOfflineCablePull: "server's
// noise long-poll is wedged in a half-open TCP connection").
// The scenario verifies the partition's effect on the tailnet
// data plane and that nodes converge after reconnection
// (dockertestutil.RestorePartition): the severed paths
// recover and both clients see each other online again.
func TestPartitionControlPlaneReconnect(t *testing.T) {
	IntegrationSkip(t)
	t.Parallel()

	scenario, err := NewScenario(ScenarioSpec{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		NodesPerUser: 2,
		Users:        []string{"user1"},
	})
	require.NoError(t, err)

	defer scenario.ShutdownAssertNoPanics(t)

	err = scenario.CreateStealthScaleEnv(
		[]tsic.Option{},
		hsic.WithTestName("partition"),
		hsic.WithConfigEnv(map[string]string{
			// Stock tailscale clients bootstrap over the legacy
			// /ts2021 Noise path. enforce_control=true (the
			// viper default) removes that endpoint and only
			// VLESS-capable clients can register at all.
			"STEALTHSCALE_XRAY_STEALTH_ENFORCE_CONTROL": "false",
		}),
	)
	requireNoErrStealthScaleEnv(t, err)

	allClients, err := scenario.ListTailscaleClients()
	requireNoErrListClients(t, err)
	require.Len(t, allClients, 2)

	stealthscale, err := scenario.StealthScale()
	requireNoErrGetStealthScale(t, err)

	err = scenario.WaitForTailscaleSync()
	requireNoErrSync(t, err)

	partitioned := allClients[0]
	peer := allClients[1]

	// Baseline: both nodes are online in the control plane
	// and the tailnet data plane works between them.
	requireAllClientsOnline(
		t, stealthscale, collectExpectedNodeIDs(t, allClients), true,
		"both nodes online before partition", integrationutil.StatusReadyTimeout,
	)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, _, err := peer.Execute([]string{
			"tailscale", "ping", "--c=1", "--timeout=5s", "--until-direct=false",
			partitioned.Hostname(),
		})
		assert.NoError(c, err, "peer should reach the partitioned node before the partition")
	}, integrationutil.ScaledTimeout(60*time.Second), 2*time.Second, "baseline ping should succeed")

	network, err := scenario.Network("default")
	require.NoError(t, err)

	// Blackhole the partitioned node's egress to the test
	// network: it can reach neither the control plane nor
	// its peer, and nothing can reach it back — the
	// control-plane disconnect failure mode.
	targets := []string{partitioned.Hostname()}

	require.NoError(t, dockertestutil.Partition(scenario.Pool(), network, targets, time.Minute))
	t.Cleanup(func() {
		_ = dockertestutil.RestorePartition(scenario.Pool(), network, targets)
	})

	// The partitioned node cannot leave the test network:
	// its tailnet traffic (disco pings) is blackholed.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, _, err := partitioned.Execute([]string{
			"tailscale", "ping", "--c=1", "--timeout=5s", "--until-direct=false",
			peer.Hostname(),
		})
		assert.Error(c, err, "partitioned node must not reach its peer while partitioned")
	}, integrationutil.ScaledTimeout(60*time.Second), 2*time.Second, "partitioned node should not reach its peer")

	// The peer cannot reach the partitioned node either:
	// its pings arrive but the replies are blackholed.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, _, err := peer.Execute([]string{
			"tailscale", "ping", "--c=1", "--timeout=5s", "--until-direct=false",
			partitioned.Hostname(),
		})
		assert.Error(c, err, "peer must not reach the partitioned node while partitioned")
	}, integrationutil.ScaledTimeout(60*time.Second), 2*time.Second, "peer should not reach the partitioned node")

	// Lift the partition: the node's traffic reaches the test
	// network again and the tailnet must reconverge.
	require.NoError(t, dockertestutil.RestorePartition(scenario.Pool(), network, targets))

	// The control plane kept both nodes online throughout (the
	// half-open long-poll only times out at kernel TCP
	// retransmission exhaustion, ~15 min — beyond the test
	// window), so the convergence check is the data plane.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		nodes, err := stealthscale.ListNodes()
		assert.NoError(c, err)
		assert.Len(c, nodes, 2)

		for _, node := range nodes {
			assert.True(c, node.Online, "both nodes should remain online in the control plane")
		}
	}, integrationutil.ScaledTimeout(60*time.Second), 2*time.Second, "both nodes should be online after the partition is lifted")

	// Data plane restored: pings succeed in both directions.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, _, err := peer.Execute([]string{
			"tailscale", "ping", "--c=1", "--timeout=5s", "--until-direct=false",
			partitioned.Hostname(),
		})
		assert.NoError(c, err, "peer should reach the partitioned node after the partition is lifted")
	}, integrationutil.ScaledTimeout(150*time.Second), 2*time.Second, "peer should reach the partitioned node again")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, _, err := partitioned.Execute([]string{
			"tailscale", "ping", "--c=1", "--timeout=5s", "--until-direct=false",
			peer.Hostname(),
		})
		assert.NoError(c, err, "partitioned node should reach its peer after the partition is lifted")
	}, integrationutil.ScaledTimeout(150*time.Second), 2*time.Second, "partitioned node should reach its peer again")

	// Convergence: both clients see each other online again.
	for _, client := range allClients {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			status, err := client.Status()
			assert.NoError(c, err)
			assert.Len(c, status.Peer, 1)

			for _, peerStatus := range status.Peer {
				assert.True(c, peerStatus.Online, "client %s should see its peer online after reconvergence", client.Hostname())
			}
		}, integrationutil.ScaledTimeout(150*time.Second), 2*time.Second, "clients should converge after the partition is lifted")
	}
}

// TestFlakyDERPStealthGate verifies the stealth-gated DERP
// fail-closed path under a degraded relay. DERP fallback is gated on
// stealth readiness (hscontrol/stealth.Checker ->
// hscontrol/derp.ShouldIncludeDERP -> MapResponseBuilder.WithDERPMap),
// not on relay health: while the VLESS+Reality transport is serving,
// the netmap keeps carrying the DERP region even when the relay
// itself is slow and lossy, and the control plane keeps registering
// nodes.
func TestFlakyDERPStealthGate(t *testing.T) {
	IntegrationSkip(t)
	t.Parallel()

	scenario, err := NewScenario(ScenarioSpec{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		NodesPerUser: 2,
		Users:        []string{"user1"},
	})
	require.NoError(t, err)

	defer scenario.ShutdownAssertNoPanics(t)

	// A dedicated DERP relay container so the degraded network can be
	// scoped to the relay alone, leaving the control plane (and its
	// VLESS+Reality transport) untouched.
	derper, err := scenario.CreateDERPServer("head")
	require.NoError(t, err)

	derpRegion := tailcfg.DERPRegion{ //nolint:exhaustruct_v5 // test fixture: custom relay region
		RegionCode: "flaky-derp",
		RegionName: "FlakyDERP",
		Nodes: []*tailcfg.DERPNode{
			{ //nolint:exhaustruct_v5 // test fixture: custom relay node
				Name:             "flaky-derp",
				RegionID:         900,
				HostName:         derper.GetHostname(),
				STUNPort:         derper.GetSTUNPort(),
				STUNOnly:         false,
				DERPPort:         derper.GetDERPPort(),
				InsecureForTests: true,
			},
		},
	}
	derpMap := tailcfg.DERPMap{ //nolint:exhaustruct_v5 // test fixture: single custom region
		Regions: map[int]*tailcfg.DERPRegion{
			900: &derpRegion,
		},
	}

	err = scenario.CreateStealthScaleEnv(
		[]tsic.Option{tsic.WithCACert(derper.GetCert())},
		hsic.WithTestName("flakyderp"),
		hsic.WithDERPConfig(derpMap),
		hsic.WithConfigEnv(map[string]string{
			"STEALTHSCALE_XRAY_SECURITY":     "reality_xtls",
			"STEALTHSCALE_XRAY_REALITY_DEST": "www.cloudflare.com:443",
			// viper splits env-var lists on whitespace, not
			// commas — ServerNames must be space-separated.
			"STEALTHSCALE_XRAY_REALITY_SERVER_NAMES": "www.cloudflare.com www.microsoft.com cloudflare.com microsoft.com",
			"STEALTHSCALE_XRAY_UTLS_FINGERPRINT":     "chrome",
			"STEALTHSCALE_XRAY_STEALTH_ENFORCE":      "true",
			// Stock tailscale clients bootstrap over the
			// legacy /ts2021 Noise path; only DERP
			// fallback (not registration) is gated on
			// stealth readiness here.
			"STEALTHSCALE_XRAY_STEALTH_ENFORCE_CONTROL": "false",
		}),
	)
	requireNoErrStealthScaleEnv(t, err)

	stealthscale, err := scenario.StealthScale()
	requireNoErrGetStealthScale(t, err)

	allClients, err := scenario.ListTailscaleClients()
	requireNoErrListClients(t, err)
	require.Len(t, allClients, 2)

	err = scenario.WaitForTailscaleSync()
	requireNoErrSync(t, err)

	// Stealth is satisfied (the Reality transport is serving), so the
	// netmap carries the custom DERP region — fail-closed is not
	// engaged.
	for _, client := range allClients {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			nm, err := client.Netmap()
			assert.NoError(c, err)
			requireDERPRegionWithCollect(c, nm, 900)
		}, integrationutil.StatusReadyTimeout, 2*time.Second, "netmap should carry the custom DERP region while stealth is satisfied")
	}

	network, err := scenario.Network("default")
	require.NoError(t, err)

	// Degrade only the relay: 300ms latency and 15% packet loss.
	flaky := dockertestutil.FlakyDERP(
		scenario.Pool(), network, derper.GetHostname(),
		300*time.Millisecond, 15,
	)
	// The preset conditions (set by FlakyDERP) carry the
	// degradation; the Apply override adds nothing on top.
	require.NoError(t, flaky.Apply(dockertestutil.NetworkConditions{})) //nolint:exhaustruct_v5 // empty override: preset carries the conditions
	t.Cleanup(flaky.ResetAll)

	// The degraded relay must not trip fail-closed: the netmap still
	// carries the DERP region, because gating keys on stealth
	// readiness rather than relay health.
	for _, client := range allClients {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			nm, err := client.Netmap()
			assert.NoError(c, err)
			requireDERPRegionWithCollect(c, nm, 900)
		}, integrationutil.ScaledTimeout(30*time.Second), 2*time.Second, "netmap must keep carrying the DERP region while the relay is degraded")
	}

	// The control plane is unaffected: a new node registers and the
	// tailnet converges while the relay is degraded.
	var userID uint64

	users, err := stealthscale.ListUsers()
	require.NoError(t, err)

	for _, user := range users {
		if user.Name == "user1" {
			userID = mustParseID(user.Id)
		}
	}

	require.NotZero(t, userID, "user1 should exist")

	pak, err := scenario.CreatePreAuthKey(userID, true, false)
	require.NoError(t, err)

	err = scenario.CreateTailscaleNodesInUser(
		"user1", "head", 1,
		tsic.WithNetwork(scenario.networks[scenario.testDefaultNetwork]),
	)
	require.NoError(t, err)

	var joined []TailscaleClient

	joined, err = scenario.ListTailscaleClients()
	requireNoErrListClients(t, err)
	require.Len(t, joined, 3)

	var newClient TailscaleClient

	for _, client := range joined {
		known := false

		for _, existing := range allClients {
			if client.Hostname() == existing.Hostname() {
				known = true
				break
			}
		}

		if !known {
			newClient = client
		}
	}

	require.NotNil(t, newClient, "newly created client should be found")

	err = newClient.Login(stealthscale.GetEndpoint(), pak.Key)
	require.NoError(t, err, "registration must succeed while the DERP relay is degraded")

	err = newClient.WaitForRunning(integrationutil.PeerSyncTimeout())
	require.NoError(t, err)

	// Convergence: every client sees the other two.
	err = scenario.WaitForTailscaleSync()
	requireNoErrSync(t, err)
}

// registerOverVLESS runs the Tailscale noise handshake over an
// established VLESS stream and submits a machine registration
// through the HTTP/2 machine API, mirroring what a stealth-capable
// client (stscale up) does. Modeled on hscontrol/servertest's
// registerViaVLESS.
func registerOverVLESS(
	tb testing.TB,
	ctx context.Context,
	conn net.Conn,
	machineKey key.MachinePrivate,
	serverPub key.MachinePublic,
	regReq tailcfg.RegisterRequest,
) tailcfg.RegisterResponse {
	tb.Helper()

	noiseConn, err := controlbase.Client(ctx, conn, machineKey, serverPub, uint16(tailcfg.CurrentCapabilityVersion))
	require.NoError(tb, err, "noise handshake over VLESS")

	tr := &http2.Transport{ //nolint:exhaustruct_v5 // test fixture: dialer is overridden, rest default
		AllowHTTP: true,
		DialTLSContext: func(context.Context, string, string, *tls.Config) (net.Conn, error) {
			return noiseConn, nil
		},
	}
	defer tr.CloseIdleConnections()

	body, err := json.Marshal(regReq)
	require.NoError(tb, err)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"http://control/machine/register",
		bytes.NewReader(body),
	)
	require.NoError(tb, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := tr.RoundTrip(req)
	require.NoError(tb, err)

	defer resp.Body.Close()

	require.Equal(tb, http.StatusOK, resp.StatusCode)

	var regResp tailcfg.RegisterResponse
	require.NoError(tb, json.NewDecoder(resp.Body).Decode(&regResp))
	require.Empty(tb, regResp.Error, "register response should not carry an error")

	return regResp
}

// requireDERPRegionWithCollect asserts that the netmap carries the
// given DERP region. For use inside assert.EventuallyWithT blocks.
func requireDERPRegionWithCollect(c *assert.CollectT, nm *netmap.NetworkMap, regionID int) {
	c.Helper()

	if nm == nil {
		assert.Fail(c, "netmap is nil")
		return
	}

	if nm.DERPMap == nil {
		assert.Fail(c, fmt.Sprintf("netmap DERPMap is nil — fail-closed suppressed the relay map (region %d expected)", regionID))
		return
	}

	_, ok := nm.DERPMap.Regions[regionID]
	assert.True(c, ok, "netmap DERPMap should contain region %d", regionID)
}
