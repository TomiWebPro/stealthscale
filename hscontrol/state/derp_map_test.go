package state

import (
	"testing"

	"tailscale.com/tailcfg"
)

// TestStateDERPMapRuntimePrecedence pins the runtime DERP
// map contract (Web UI runtime apply, goal #4):
// SetDERPMapRuntime stores the operator's map and marks it
// runtime-applied, and a config-derived SetDERPMap is then
// ignored — the periodic auto-updater must not silently
// revert an operator-applied map.
func TestStateDERPMapRuntimePrecedence(t *testing.T) {
	dbPath := t.TempDir() + "/derp-map-runtime.db"
	cfg := persistTestConfig(dbPath)

	s, err := NewState(cfg)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	configMap := &tailcfg.DERPMap{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		Regions: map[int]*tailcfg.DERPRegion{
			1: {RegionID: 1}, //nolint:exhaustruct_v5 // test fixture: only fields under test
		},
	}
	s.SetDERPMap(configMap)

	if s.DERPMapIsRuntime() {
		t.Fatal("fresh state: DERPMapIsRuntime = true, want false")
	}

	runtimeMap := &tailcfg.DERPMap{ //nolint:exhaustruct_v5 // test fixture: only fields under test
		Regions: map[int]*tailcfg.DERPRegion{
			2: {RegionID: 2}, //nolint:exhaustruct_v5 // test fixture: only fields under test
		},
	}
	s.SetDERPMapRuntime(runtimeMap)

	if !s.DERPMapIsRuntime() {
		t.Fatal("after SetDERPMapRuntime: DERPMapIsRuntime = false, want true")
	}

	if got := s.DERPMap().AsStruct(); len(got.Regions) != 1 || got.Regions[2] == nil {
		t.Fatalf("after SetDERPMapRuntime: regions = %v, want only region 2", got.Regions)
	}

	// Config-derived stores are ignored while a runtime map
	// is in effect (the auto-updater must not revert it).
	s.SetDERPMap(configMap)

	if got := s.DERPMap().AsStruct(); len(got.Regions) != 1 || got.Regions[2] == nil {
		t.Fatalf("after config-derived SetDERPMap under runtime precedence: regions = %v, want the runtime map (region 2)", got.Regions)
	}
}

// TestStateDERPMapAugment pins the DERP-map augmenter:
// every stored map — config-derived and operator-applied
// alike — passes through the registered augmenter before
// it is stored, so an operator-applied map cannot drop
// the embedded DERP region.
func TestStateDERPMapAugment(t *testing.T) {
	dbPath := t.TempDir() + "/derp-map-augment.db"
	cfg := persistTestConfig(dbPath)

	s, err := NewState(cfg)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	s.SetDERPMapAugment(func(dm *tailcfg.DERPMap) {
		if dm.Regions == nil {
			dm.Regions = map[int]*tailcfg.DERPRegion{}
		}

		dm.Regions[99] = &tailcfg.DERPRegion{RegionID: 99} //nolint:exhaustruct_v5 // test fixture: only fields under test
	})

	s.SetDERPMap(&tailcfg.DERPMap{}) //nolint:exhaustruct_v5 // test fixture: only fields under test

	if got := s.DERPMap().AsStruct(); got.Regions[99] == nil {
		t.Fatal("config-derived store: augmenter not applied")
	}

	s.SetDERPMapRuntime(&tailcfg.DERPMap{}) //nolint:exhaustruct_v5 // test fixture: only fields under test

	if got := s.DERPMap().AsStruct(); got.Regions[99] == nil {
		t.Fatal("runtime store: augmenter not applied")
	}
}

// TestStateDERPMapChangeListeners pins the DERP-map change
// hook: listeners fire after every store (config-derived
// and operator-applied), which is how the StealthScale app
// broadcasts a Web UI-applied map to connected mapper
// clients.
func TestStateDERPMapChangeListeners(t *testing.T) {
	dbPath := t.TempDir() + "/derp-map-listeners.db"
	cfg := persistTestConfig(dbPath)

	s, err := NewState(cfg)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	fired := 0

	s.OnDERPMapChange(func() { fired++ })

	s.SetDERPMap(&tailcfg.DERPMap{}) //nolint:exhaustruct_v5 // test fixture: only fields under test

	s.SetDERPMapRuntime(&tailcfg.DERPMap{}) //nolint:exhaustruct_v5 // test fixture: only fields under test

	if fired != 2 {
		t.Fatalf("listener fired %d times, want 2 (once per store)", fired)
	}
}
