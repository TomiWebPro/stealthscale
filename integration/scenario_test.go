package integration

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tomiwebpro/stealthscale/integration/dockertestutil"
	"github.com/tomiwebpro/stealthscale/integration/hsic"
	"github.com/tomiwebpro/stealthscale/integration/tsic"
)

// This file is intended to "test the test framework", by proxy it will also test
// some StealthScale/Tailscale stuff, but mostly in very simple ways.

func IntegrationSkip(t *testing.T) {
	t.Helper()

	if !dockertestutil.IsRunningInContainer() {
		t.Skip("not running in docker, skipping")
	}

	if testing.Short() {
		t.Skip("skipping integration tests due to short flag")
	}
}

// If subtests are parallel, then they will start before setup is run.
// This might mean we approach setup slightly wrong, but for now, ignore
// the linter
// nolint:tparallel
func TestStealthScale(t *testing.T) {
	IntegrationSkip(t)

	var err error

	user := "test-space"

	scenario, err := NewScenario(ScenarioSpec{})

	require.NoError(t, err)
	defer scenario.ShutdownAssertNoPanics(t)

	t.Run("start-stealthscale", func(t *testing.T) {
		stealthscale, err := scenario.StealthScale(hsic.WithTestName("scenariohs"))
		if err != nil {
			t.Fatalf("failed to create start stealthscale: %s", err)
		}

		err = stealthscale.WaitForRunning()
		if err != nil {
			t.Fatalf("stealthscale failed to become ready: %s", err)
		}
	})

	t.Run("create-user", func(t *testing.T) {
		_, err := scenario.CreateUser(user)
		if err != nil {
			t.Fatalf("failed to create user: %s", err)
		}

		if _, ok := scenario.users[user]; !ok {
			t.Fatalf("user is not in scenario")
		}
	})

	t.Run("create-auth-key", func(t *testing.T) {
		_, err := scenario.CreatePreAuthKey(1, true, false)
		if err != nil {
			t.Fatalf("failed to create preauthkey: %s", err)
		}
	})
}

// If subtests are parallel, then they will start before setup is run.
// This might mean we approach setup slightly wrong, but for now, ignore
// the linter
// nolint:tparallel
func TestTailscaleNodesJoiningStealthScale(t *testing.T) {
	IntegrationSkip(t)

	var err error

	user := "join-node-test"

	count := 1

	scenario, err := NewScenario(ScenarioSpec{})

	require.NoError(t, err)
	defer scenario.ShutdownAssertNoPanics(t)

	t.Run("start-stealthscale", func(t *testing.T) {
		stealthscale, err := scenario.StealthScale(hsic.WithTestName("scenariojoin"))
		if err != nil {
			t.Fatalf("failed to create start stealthscale: %s", err)
		}

		err = stealthscale.WaitForRunning()
		if err != nil {
			t.Fatalf("stealthscale failed to become ready: %s", err)
		}
	})

	t.Run("create-user", func(t *testing.T) {
		_, err := scenario.CreateUser(user)
		if err != nil {
			t.Fatalf("failed to create user: %s", err)
		}

		if _, ok := scenario.users[user]; !ok {
			t.Fatalf("user is not in scenario")
		}
	})

	t.Run("create-tailscale", func(t *testing.T) {
		err := scenario.CreateTailscaleNodesInUser(user, "unstable", count, tsic.WithNetwork(scenario.networks[scenario.testDefaultNetwork]))
		if err != nil {
			t.Fatalf("failed to add tailscale nodes: %s", err)
		}

		if clients := len(scenario.users[user].Clients); clients != count {
			t.Fatalf("wrong number of tailscale clients: %d != %d", clients, count)
		}
	})

	t.Run("join-stealthscale", func(t *testing.T) {
		key, err := scenario.CreatePreAuthKey(1, true, false)
		if err != nil {
			t.Fatalf("failed to create preauthkey: %s", err)
		}

		stealthscale, err := scenario.StealthScale()
		if err != nil {
			t.Fatalf("failed to create start stealthscale: %s", err)
		}

		err = scenario.RunTailscaleUp(
			user,
			stealthscale.GetEndpoint(),
			key.Key,
		)
		if err != nil {
			t.Fatalf("failed to login: %s", err)
		}
	})

	t.Run("get-ips", func(t *testing.T) {
		ips, err := scenario.GetIPs(user)
		if err != nil {
			t.Fatalf("failed to get tailscale ips: %s", err)
		}

		if len(ips) != count*2 {
			t.Fatalf("got the wrong amount of tailscale ips, %d != %d", len(ips), count*2)
		}
	})
}
