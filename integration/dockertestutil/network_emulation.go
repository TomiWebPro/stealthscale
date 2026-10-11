package dockertestutil

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ory/dockertest/v3"
)

// TCType enumerates the kind of traffic-control primitive being applied.
type TCType int

const (
	_              TCType = iota
	TC_Latency            // delay + jitter
	TC_Loss               // packet loss
	TC_Duplication        // duplicate packets
	TC_Corruption         // bit corruption
	TC_Reordering         // out-of-order delivery
)

func (t TCType) String() string {
	switch t {
	case TC_Latency:
		return "latency"
	case TC_Loss:
		return "loss"
	case TC_Duplication:
		return "duplication"
	case TC_Corruption:
		return "corruption"
	case TC_Reordering:
		return "reordering"
	default:
		return "unknown"
	}
}

// defaultReorderDelay is the minimal base delay synthesized when
// ReorderPct is set without Latency: the kernel rejects
// "netem reorder" without a delay ("reordering not possible
// without specifying some delay"), so a reorder-only condition
// falls back to this — mirroring the Jitter default.
const defaultReorderDelay = time.Millisecond

// NetworkConditions carries the full set of degraded-network parameters to
// apply to containers via [NetworkEmulation]. Zero values mean "unspecified"
// (the corresponding primitive is not applied).
type NetworkConditions struct {
	Latency      time.Duration // base round-trip delay (e.g. 200ms)
	Jitter       time.Duration // randomness around the delay; defaults to Latency/4
	DropPct      float64       // percentage of packets to drop (0–100)
	DuplicatePct float64       // percentage of packets to duplicate (0–100)
	CorrPct      float64       // percentage of packets to corrupt (0–100)
	// ReorderPct is the percentage of packets to deliver out of
	// order (0–100). It requires a base delay; when Latency is
	// zero a minimal defaultReorderDelay is synthesized, because
	// the kernel rejects "netem reorder" without one.
	ReorderPct float64
}

func (c NetworkConditions) isZero() bool {
	return c.Latency == 0 && c.Jitter == 0 &&
		c.DropPct == 0 && c.DuplicatePct == 0 && c.CorrPct == 0 &&
		c.ReorderPct == 0
}

// NetworkEmulation attaches tc/netem traffic-control rules to the
// network interfaces of containers in a Docker network so integration
// tests can exercise degraded-network scenarios (latency, loss, partitions,
// etc.).
//
// Typical usage inside a test:
//
//	emul := NewNetworkEmulation(pool, network, "client", "server", "derp")
//	require.NoError(t, emul.Apply(conditions))
//	t.Cleanup(emul.ResetAll)
//
// All calls are scoped to the containers named at construction; other
// containers on the same network are unaffected.
type NetworkEmulation struct {
	pool       *dockertest.Pool
	network    *dockertest.Network
	containers []string
	preset     NetworkConditions // optional pre-set conditions from constructor helpers
	hasPreset  bool
}

// NewNetworkEmulation returns a new emitter operating on the named
// containers within [network]. Containers not currently running (not yet
// attached to [network]) are silently skipped — Apply will log a warning
// and continue with the remaining containers.
func NewNetworkEmulation(
	pool *dockertest.Pool,
	network *dockertest.Network,
	containerNames ...string,
) *NetworkEmulation {
	return &NetworkEmulation{
		pool:       pool,
		network:    network,
		containers: containerNames,
	}
}

// Apply adds tc/netem rules to every managed container's network
// interface matching [conds]. If a prior rule already exists on an
// interface it is replaced (idempotent w.r.t. successive Apply calls).
// When the receiver was created by [FlakyDERP] (hasPreset == true),
// the preset conditions are used if [conds] is the zero value.
//
// Returns nil on success. Errors from individual containers are logged
// rather than returned so partial failures don't abort the test setup.
// Inspect stderr logs for diagnostics.
func (ne *NetworkEmulation) Apply(conds NetworkConditions) error {
	if ne.hasPreset && conds.isZero() {
		conds = ne.preset
	}

	for _, name := range ne.containers {
		if err := ne.applyOne(name, conds); err != nil {
			log.Printf("emulation[apply]: %s: %v", name, err)
		}
	}

	return nil
}

// ResetAll removes all tc/netem qdiscs that were added by [Apply] on
// every managed container, restoring them to a clean-slate state. Safe
// to call even if no rules were ever applied or if a container has
// already been removed by Docker.
func (ne *NetworkEmulation) ResetAll() {
	for _, name := range ne.containers {
		if err := ne.resetOne(name); err != nil {
			log.Printf("emulation[reset]: %s: %v", name, err)
		}
	}
}

// ---------- internal helpers ------------------------------------------------

func (ne *NetworkEmulation) applyOne(container string, conds NetworkConditions) error {
	iface, err := findContainerInterface(ne.pool, container)
	if err != nil {
		return fmt.Errorf("interface lookup: %w", err)
	}

	netemArgs := buildNetemFilter(conds)
	// Ensure exactly one root qdisc: replace old, install new (empty == remove).
	if err := ne.replaceQdisc(iface, container, netemArgs); err != nil {
		return err
	}

	return nil
}

func (ne *NetworkEmulation) resetOne(container string) error {
	iface, err := findContainerInterface(ne.pool, container)
	if err != nil {
		// Container may already be gone; silent exit is fine.
		return nil
	}

	return ne.replaceQdisc(iface, container, "")
}

// replaceQdisc ensures exactly one root qdisc exists on [iface]: either
// the provided netem filter arguments or nothing (clean slate). When
// args is empty the interface's root qdisc is simply removed.
func (ne *NetworkEmulation) replaceQdisc(iface, container, netemArgs string) error {
	// Remove any existing root qdisc regardless of type (tc errors harmlessly if none).
	_, _ = execStdout(ne.pool, container, []string{"tc", "qdisc", "del", "dev", iface, "root"})

	if netemArgs == "" {
		return nil
	}

	cmd := []string{"tc", "-s", "qdisc", "add", "dev", iface, "root", "netem", netemArgs}
	if _, err := execStdout(ne.pool, container, cmd); err != nil {
		return fmt.Errorf("tc qdisc add: %w (cmd: %v)", err, cmd)
	}

	return nil
}

// findContainerInterface discovers the primary (non-loopback) Ethernet
// interface inside [container] by parsing the output of `ip -4 addr show`.
func findContainerInterface(pool *dockertest.Pool, container string) (string, error) {
	out, err := execStdout(pool, container, []string{"ip", "-4", "addr", "show"})
	if err != nil {
		return "", fmt.Errorf("ip addr show: %w", err)
	}

	lines := strings.Split(out, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		// Match lines starting with "<number>:" format (e.g. "1:")
		// and skip loopback ("lo:").
		if strings.HasSuffix(fields[0], ":") && !strings.Contains(line, "lo:") {
			name := strings.TrimSuffix(fields[1], ":")
			// Veth pair notation: eth0@if74 → keep only "eth0"
			if idx := strings.Index(name, "@"); idx >= 0 {
				name = name[:idx]
			}
			return name, nil
		}
	}

	return "", fmt.Errorf("no non-loopback interface found in container %s", container)
}

// buildNetemFilter converts [NetworkConditions] into a tc netem argument
// string (the space-separated tail end of "tc qdisc add ... netem").
// Returns "" when all condition fields are zero (nothing to apply).
func buildNetemFilter(conds NetworkConditions) string {
	var parts []string

	if conds.Latency > 0 {
		delay := conds.Latency.Round(time.Microsecond).String()
		jitter := conds.Jitter
		if jitter == 0 {
			jitter = conds.Latency / 4
		}
		parts = append(parts, "delay", delay, jitter.Round(time.Microsecond).String())
	} else if conds.ReorderPct > 0 {
		// Reorder-only conditions get the minimal base delay:
		// the kernel rejects "netem reorder" without one.
		parts = append(parts, "delay", defaultReorderDelay.String())
	}

	if conds.DropPct > 0 {
		parts = append(parts, "drop", fmt.Sprintf("%.2f%%", conds.DropPct))
	}

	if conds.DuplicatePct > 0 {
		parts = append(parts, "duplicate", fmt.Sprintf("%.2f%%", conds.DuplicatePct))
	}

	if conds.CorrPct > 0 {
		parts = append(parts, "corrupt", fmt.Sprintf("%.2f%%", conds.CorrPct))
	}

	if conds.ReorderPct > 0 {
		parts = append(parts, "reorder", fmt.Sprintf("%.2f%%", conds.ReorderPct))
	}

	return strings.Join(parts, " ")
}

// Partition separates [targets] from the rest of the network by installing
// route overrides that send all subnet-bound traffic through the loopback
// device (effectively blackholing peer-to-peer connectivity).
//
// The targets remain on the Docker network (visible to DNS/mdns) but cannot
// reach peers on the same subnets. Callers should handle connection-teardown
// signaling themselves (reconnect waits, timeouts, etc.). For complete
// cable-pull semantics prefer [DisconnectContainerFromNetwork]; Partition is
// for scenarios where you want containers visible in the same network but
// unable to route to specific peers.
func Partition(
	pool *dockertest.Pool,
	network *dockertest.Network,
	targetContainers []string,
	timeout time.Duration,
) error {
	subnets := networkSubnets(network)
	if len(subnets) == 0 {
		return fmt.Errorf("Partition: network %s has no known subnets", network.Network.Name)
	}

	for _, name := range targetContainers {
		containerID, err := lookupContainerID(pool, name)
		if err != nil {
			return fmt.Errorf("partition %s: %w", name, err)
		}

		for _, subnet := range subnets {
			// Route all subnets to lo as a blackhole (invalid via 127.0.0.1).
			cmd := []string{"ip", "route", "replace", subnet, "via", "127.0.0.1", "dev", "lo"}
			if _, err := execStdout(pool, containerID, cmd); err != nil {
				log.Printf("partition: route replace error for %s/%s: %v", name, subnet, err)
			}
		}

		_ = containerID
		log.Printf("partitioned %s: blackholing %d subnets via lo", name, len(subnets))
	}

	return nil
}

// errNoSubnets backs the guard clauses of [Partition] and
// [RestorePartition]: shaping or restoring routes is meaningless
// on a network with no known subnets.
var errNoSubnets = errors.New("network has no known subnets")

// RestorePartition undoes [Partition] by replacing the blackhole
// routes it installed with the original directly-connected routes
// on [network], restoring the targets' reachability to peers.
// Safe to call even if [Partition] was never applied — replacing
// a route that does not exist simply (re)installs the connected
// route.
func RestorePartition(
	pool *dockertest.Pool,
	network *dockertest.Network,
	targetContainers []string,
) error {
	subnets := networkSubnets(network)
	if len(subnets) == 0 {
		return fmt.Errorf("RestorePartition: network %s: %w", network.Network.Name, errNoSubnets)
	}

	for _, name := range targetContainers {
		containerID, err := lookupContainerID(pool, name)
		if err != nil {
			return fmt.Errorf("restore partition %s: %w", name, err)
		}

		iface, err := findContainerInterface(pool, name)
		if err != nil {
			return fmt.Errorf("restore partition %s: %w", name, err)
		}

		for _, subnet := range subnets {
			// Reinstall the directly-connected route that
			// Partition's blackhole replaced.
			cmd := []string{"ip", "route", "replace", subnet, "dev", iface, "scope", "link"}

			_, err = execStdout(pool, containerID, cmd)
			if err != nil {
				log.Printf("restore partition: route replace error for %s/%s: %v", name, subnet, err)
			}
		}

		log.Printf("restored %s: reconnected to %d subnets via %s", name, len(subnets), iface)
	}

	return nil
}

// FlakyDERP creates a [NetworkEmulation] configured to simulate a flaky
// DERP relay — high latency combined with significant packet loss — on
// the given container. Callers must invoke [NetworkEmulation.Apply] /
// [NetworkEmulation.ResetAll] explicitly.
//
// The returned emulator targets only the DERP container; other containers
// on the same network are unaffected. Typical usage:
//
//	derpEmul := dockertestutil.FlakyDERP(pool, net, "hs-derp-abc", 300*time.Millisecond, 15)
//	require.NoError(t, derpEmul.Apply(dockertestutil.NetworkConditions{}))
//	t.Cleanup(derpEmul.ResetAll)
func FlakyDERP(
	pool *dockertest.Pool,
	network *dockertest.Network,
	derpContainer string,
	latency time.Duration,
	lossPct float64,
) *NetworkEmulation {
	emul := NewNetworkEmulation(pool, network, derpContainer)
	emul.preset = NetworkConditions{
		Latency: latency,
		DropPct: lossPct,
	}
	emul.hasPreset = true
	return emul
}
