// Package config tests: port config tests from old/tests/unit.test.ts and
// assert verbatim TS error-message parity.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigIsValid(t *testing.T) {
	errs := ValidateConfig(DefaultConfig())
	if len(errs) != 0 {
		t.Fatalf("default config invalid: %#v", errs)
	}
}

func TestValidateCatchesBadTierAndRates(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Tiers[0].Models[0].In = -1
	// The typed struct cannot hold an unknown category, so replicate the TS
	// test's second error through the map API:
	m := toMap(cfg)
	pol := m["policy"].(map[string]any)
	ovs := pol["overrides"].([]any)
	ovs = append(ovs, map[string]any{"category": "bogus", "tier": 0.0})
	pol["overrides"] = ovs
	errs := ValidateMap(m)
	if len(errs) < 2 {
		t.Fatalf("expected >=2 errors, got %#v", errs)
	}
	want1 := `tier 0 model nemotron-3-nano:30b: rates must be >= 0`
	want2 := `policy override category "bogus" is not a known category`
	found1, found2 := false, false
	for _, e := range errs {
		if e == want1 {
			found1 = true
		}
		if e == want2 {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Fatalf("verbatim messages missing:\n got %#v\n want1 %q\n want2 %q", errs, want1, want2)
	}
}

func TestMergeConfigUserWinsDefaultsPreserved(t *testing.T) {
	user := map[string]any{
		"cost":   map[string]any{"perRequestCapUsd": 5.0},
		"server": map[string]any{"port": 9999.0},
	}
	merged := mergeMapsOverDefaults(user)
	if got := merged.Cost.PerRequestCapUsd; got != 5 {
		t.Fatalf("perRequestCapUsd = %v, want 5", got)
	}
	if got := merged.Server.Port; got != 9999 {
		t.Fatalf("port = %v, want 9999", got)
	}
	if got := merged.Jev.Model; got != "jev-1.13.0" {
		t.Fatalf("jev.model = %q, want default jev-1.13.0", got)
	}
	if len(merged.Tiers) != 4 {
		t.Fatalf("tiers = %d, want 4 default", len(merged.Tiers))
	}
}

func TestArraysReplacedNotMerged(t *testing.T) {
	// TS deepMerge replaces arrays wholesale — user tiers array wins entirely.
	user := map[string]any{
		"policy": map[string]any{
			"overrides": []any{
				map[string]any{"category": "summarize", "tier": 1.0},
			},
		},
	}
	m := toMap(DefaultConfig())
	deepMergeMaps(m, user)
	pol := m["policy"].(map[string]any)
	ovs := pol["overrides"].([]any)
	if len(ovs) != 1 {
		t.Fatalf("overrides len = %d, want 1 (array replaced, not merged)", len(ovs))
	}
	ov := ovs[0].(map[string]any)
	if ov["category"] != "summarize" {
		t.Fatalf("override category = %v, want summarize", ov["category"])
	}
}

func TestLoadConfigMissingFileReportsError(t *testing.T) {
	res := LoadConfig("does-not-exist.json")
	if len(res.Errors) != 1 {
		t.Fatalf("expected 1 error, got %#v", res.Errors)
	}
	want := "cannot read config file does-not-exist.json:"
	if len(res.Errors) > 0 && indexOf(res.Errors[0], want) != 0 {
		t.Fatalf("error %q does not start with %q", res.Errors[0], want)
	}
}

func TestLoadConfigNoPathUsesDefaults(t *testing.T) {
	res := LoadConfig("")
	if len(res.Errors) != 0 {
		t.Fatalf("expected no errors, got %#v", res.Errors)
	}
	if res.Config.Jev.Model != "jev-1.13.0" {
		t.Fatalf("jev.model = %q, want jev-1.13.0", res.Config.Jev.Model)
	}
}

func TestLoadConfigRealFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	_ = os.WriteFile(path, []byte(`{"cost":{"perRequestCapUsd":0.5}}`), 0o644)
	res := LoadConfig(path)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %#v", res.Errors)
	}
	if res.Config.Cost.PerRequestCapUsd != 0.5 {
		t.Fatalf("cap = %v, want 0.5", res.Config.Cost.PerRequestCapUsd)
	}
	if len(res.Config.Tiers) != 4 {
		t.Fatalf("tiers = %d, want 4", len(res.Config.Tiers))
	}
}

func TestRoundTripKeepsRepoConfigValid(t *testing.T) {
	// The repo's own fluxrouter.config.json must keep validating.
	data, err := os.ReadFile("../../fluxrouter.config.json")
	if err != nil {
		t.Skip("repo config not present")
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("repo config malformed: %v", err)
	}
	base := toMap(DefaultConfig())
	deepMergeMaps(base, m)
	errs := ValidateMap(base)
	if len(errs) != 0 {
		t.Fatalf("repo config invalid: %#v", errs)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// mergeMapsOverDefaults mirrors mergeConfig for map inputs.
func mergeMapsOverDefaults(user map[string]any) Config {
	m := toMap(DefaultConfig())
	deepMergeMaps(m, user)
	cfg, err := compileMap(m)
	if err != nil {
		panic(err)
	}
	return cfg
}