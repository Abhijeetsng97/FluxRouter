// Policy parity tests: every case is a decision recorded from the live TS
// engine (old/scripts/gen-vectors.mjs → testdata/vectors.json). The Go engine
// must reproduce tier, reason, category, complexity, confidence, and notes
// exactly, and projectedCostUsd within 1e-9.
//
// External test package (policy_test) so it may import internal/config,
// which itself imports policy.
package routing_test

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/abhijeet/fluxrouter/internal/config"
	"github.com/abhijeet/fluxrouter/internal/routing"
	"github.com/abhijeet/fluxrouter/internal/types"
)

// vectorsShape mirrors old/scripts/vectors.json.
type vectorsShape struct {
	SessionIds     []string `json:"sessionIds"`
	EstimateTokens []int64  `json:"estimateTokens"`
	JevStates      []string `json:"jevStates"`
	Policy         []policyVector `json:"policy"`
	ComplexityToTier []int64 `json:"complexityToTier"`
	ApplyOverrides   []any   `json:"applyOverrides"`
	StickyEscape     []*int  `json:"stickyEscape"`
}

// policyVector is one recorded routeRequest decision.
type policyVector struct {
	Name             string   `json:"name"`
	Tier             int64    `json:"tier"`
	Reason           string   `json:"reason"`
	Category         string   `json:"category"`
	Complexity       float64  `json:"complexity"`
	Confidence       float64  `json:"confidence"`
	ProjectedCostUsd float64  `json:"projectedCostUsd"`
	Notes            []string `json:"notes"`
}

func loadVectors(t *testing.T) vectorsShape {
	t.Helper()
	data, err := os.ReadFile("../testdata/vectors.json")
	if err != nil {
		t.Fatalf("vectors.json missing — run: node old/scripts/gen-vectors.mjs (%v)", err)
	}
	var v vectorsShape
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("vectors.json malformed: %v", err)
	}
	return v
}

// cls mirrors the generator's cls() helper.
func cls(over func(*types.ClassificationResult)) types.ClassificationResult {
	c := types.ClassificationResult{
		Category:             "other",
		Complexity:           0,
		ComplexityConfidence: 0.9,
		CategoryConfidence:   0.9,
		TrivialNoul:          0.01,
		JevModel:             "jev-test",
		JevUsage:             types.JevUsage{InputTokens: 100, OutputTokens: 10},
	}
	if over != nil {
		over(&c)
	}
	return c
}

// mkConfig mirrors the generator's mkInput().config for the default config.
func mkConfig() routing.PolicyConfig {
	cfg := config.DefaultConfig()
	return routing.PolicyConfig{
		MinConfidence:               cfg.Jev.MinConfidence,
		TrivialNoul:                 cfg.Jev.TrivialNoul,
		EscalateOnlyAboveComplexity: cfg.Jev.EscalateOnlyAboveComplexity,
		ComplexityToTier:            cfg.Policy.Default.ComplexityToTier,
		Overrides:                   policyOverrides(cfg.Policy.Overrides),
		PerRequestCapUsd:            cfg.Cost.PerRequestCapUsd,
		StickyEnabled:               cfg.Sticky.Enabled,
		EscapeConfidence:            cfg.Sticky.EscapeConfidence,
	}
}

func policyOverrides(ovs []config.ConfigOverride) []routing.Override {
	out := make([]routing.Override, 0, len(ovs))
	for _, ov := range ovs {
		out = append(out, routing.Override{Category: ov.Category, MinComplexity: ov.MinComplexity, Tier: ov.Tier})
	}
	return out
}

// mkInput mirrors the generator's mkInput().
func mkInput(over func(*routing.Input)) routing.Input {
	cfg := config.DefaultConfig()
	in := routing.Input{
		Classification:  ptrCls(cls(nil)),
		RequestTokens:   1000,
		StickyTier:      nil,
		Tiers:           cfg.Tiers,
		FallbackTier:    nil,
		BudgetTokensOut: 0,
		Config:          mkConfig(),
	}
	if over != nil {
		over(&in)
	}
	return in
}

func ptrCls(c types.ClassificationResult) *types.ClassificationResult { return &c }

func tierPtr(t types.TierId) *types.TierId { return &t }

// namedCases builds the Go-side inputs for the 9 named policy vectors, in the
// exact order the generator pushed them.
func namedCases(cfg config.Config) []struct {
	name string
	in   routing.Input
} {
	return []struct {
		name string
		in   routing.Input
	}{
		{"trivial bypass", mkInput(func(i *routing.Input) {
			c := cls(func(c *types.ClassificationResult) { c.TrivialNoul = 0.97; c.Complexity = 2 })
			i.Classification = &c
		})},
		{"no bypass below threshold", mkInput(func(i *routing.Input) {
			c := cls(func(c *types.ClassificationResult) { c.TrivialNoul = 0.5 })
			i.Classification = &c
		})},
		{"greeting override", mkInput(func(i *routing.Input) {
			c := cls(func(c *types.ClassificationResult) { c.Category = "greeting_chitchat"; c.Complexity = 0.2 })
			i.Classification = &c
		})},
		{"math override", mkInput(func(i *routing.Input) {
			c := cls(func(c *types.ClassificationResult) { c.Category = "math"; c.Complexity = 1.3 })
			i.Classification = &c
		})},
		{"low conf escalates", mkInput(func(i *routing.Input) {
			c := cls(func(c *types.ClassificationResult) {
				c.Category = "other"; c.Complexity = 0.9; c.CategoryConfidence = 0.2; c.ComplexityConfidence = 0.3
			})
			i.Classification = &c
		})},
		{"low conf no escalate trivial", mkInput(func(i *routing.Input) {
			c := cls(func(c *types.ClassificationResult) {
				c.Complexity = 0.02; c.CategoryConfidence = 0.46; c.ComplexityConfidence = 0.46
			})
			i.Classification = &c
		})},
		{"sticky reuse", mkInput(func(i *routing.Input) {
			c := cls(func(c *types.ClassificationResult) { c.Complexity = 0.1 })
			i.Classification = &c
			i.StickyTier = tierPtr(2)
		})},
		{"jev unavailable default", mkInput(func(i *routing.Input) { i.Classification = nil })},
		{"jev unavailable fallback tier 2", mkInput(func(i *routing.Input) {
			i.Classification = nil
			i.FallbackTier = tierPtr(2)
		})},
		// Remaining 3 vectors (context gate, cost guard, jev fallback context)
		// have mutated configs — asserted in their dedicated tests below.
	}
}

func TestPolicyParityNamedVectors(t *testing.T) {
	v := loadVectors(t)
	if len(v.Policy) < 12 {
		t.Fatalf("expected >=12 policy vectors, got %d — regenerate with node old/scripts/gen-vectors.mjs", len(v.Policy))
	}
	cases := namedCases(config.DefaultConfig())
	for idx, tc := range cases {
		vec := v.Policy[idx]
		if vec.Name != tc.name {
			t.Fatalf("vector order mismatch at %d: generator=%q test=%q — regenerate fixtures", idx, vec.Name, tc.name)
		}
		t.Run(tc.name, func(t *testing.T) {
			out := routing.RouteRequest(tc.in)
			if out.Tier != types.TierId(vec.Tier) {
				t.Fatalf("tier = %d, want %d", out.Tier, vec.Tier)
			}
			if string(out.Reason) != vec.Reason {
				t.Fatalf("reason = %q, want %q", out.Reason, vec.Reason)
			}
			if out.Category != vec.Category {
				t.Fatalf("category = %q, want %q", out.Category, vec.Category)
			}
			if out.Complexity != vec.Complexity {
				t.Fatalf("complexity = %v, want %v", out.Complexity, vec.Complexity)
			}
			if math.Abs(out.Confidence-vec.Confidence) > 1e-9 {
				t.Fatalf("confidence = %v, want %v", out.Confidence, vec.Confidence)
			}
			if math.Abs(out.ProjectedCostUsd-vec.ProjectedCostUsd) > 1e-9 {
				t.Fatalf("projectedCostUsd = %v, want %v", out.ProjectedCostUsd, vec.ProjectedCostUsd)
			}
			if len(out.Notes) != len(vec.Notes) {
				t.Fatalf("notes = %#v, want %#v", out.Notes, vec.Notes)
			}
			for i := range vec.Notes {
				if i < len(out.Notes) && out.Notes[i] != vec.Notes[i] {
					t.Fatalf("notes[%d] = %q, want %q", i, out.Notes[i], vec.Notes[i])
				}
			}
		})
	}
}

func TestPolicyParityContextGate(t *testing.T) {
	v := loadVectors(t)
	vec := findVector(v, "context gate escalates")
	cfg := config.DefaultConfig()
	cfg.Tiers[0].Models[0].Ctx = 8000
	cfg.Tiers[1].Models[0].Ctx = 8000
	in := mkInput(func(i *routing.Input) {
		c := cls(func(c *types.ClassificationResult) { c.Complexity = 0.2 })
		i.Classification = &c
		i.RequestTokens = 50000
		i.Tiers = cfg.Tiers
	})
	out := routing.RouteRequest(in)
	if out.Tier != types.TierId(vec.Tier) || string(out.Reason) != vec.Reason {
		t.Fatalf("got %d/%s, want %d/%s", out.Tier, out.Reason, vec.Tier, vec.Reason)
	}
	assertNotes(t, out.Notes, vec.Notes)
}

func TestPolicyParityCostGuard(t *testing.T) {
	v := loadVectors(t)
	vec := findVector(v, "cost guard downgrades")
	cfg := config.DefaultConfig()
	cfg.Tiers[3].Models[0] = types.TierModel{Upstream: types.UpstreamOllama, ID: "expensive", Ctx: 1000000, In: 75, Out: 75}
	cfg.Policy.Overrides = append(cfg.Policy.Overrides, config.ConfigOverride{Category: "tool_planning", Tier: 3})
	base := mkConfig()
	in := mkInput(func(i *routing.Input) {
		c := cls(func(c *types.ClassificationResult) { c.Category = "tool_planning"; c.Complexity = 0.3; c.TrivialNoul = 0.1 })
		i.Classification = &c
		i.RequestTokens = 300000
		i.Tiers = cfg.Tiers
		i.BudgetTokensOut = 2000
		mut := base
		mut.Overrides = append(mut.Overrides, routing.Override{Category: "tool_planning", Tier: 3})
		i.Config = mut
	})
	out := routing.RouteRequest(in)
	if out.Tier != types.TierId(vec.Tier) || string(out.Reason) != vec.Reason {
		t.Fatalf("got %d/%s, want %d/%s", out.Tier, out.Reason, vec.Tier, vec.Reason)
	}
	assertNotes(t, out.Notes, vec.Notes)
}

func TestPolicyParityJevFallbackContextGate(t *testing.T) {
	v := loadVectors(t)
	vec := findVector(v, "jev fallback context gate")
	cfg := config.DefaultConfig()
	cfg.Tiers[1].Models[0].Ctx = 4000
	in := mkInput(func(i *routing.Input) {
		i.Classification = nil
		i.RequestTokens = 50000
		i.Tiers = cfg.Tiers
		i.FallbackTier = tierPtr(1)
	})
	out := routing.RouteRequest(in)
	if out.Tier != types.TierId(vec.Tier) || string(out.Reason) != vec.Reason {
		t.Fatalf("got %d/%s, want %d/%s", out.Tier, out.Reason, vec.Tier, vec.Reason)
	}
	assertNotes(t, out.Notes, vec.Notes)
}

func TestComplexityToTierParity(t *testing.T) {
	v := loadVectors(t)
	bands := config.DefaultConfig().Policy.Default.ComplexityToTier
	inputs := []float64{0, 0.7, 0.9, 1.7, 2.0, 50}
	for i, c := range inputs {
		got := routing.ComplexityToTier(c, bands)
		if int64(got) != v.ComplexityToTier[i] {
			t.Fatalf("complexityToTier(%v) = %d, want %d", c, int64(got), v.ComplexityToTier[i])
		}
	}
}

func TestApplyOverridesParity(t *testing.T) {
	v := loadVectors(t)
	ovs := policyOverrides(config.DefaultConfig().Policy.Overrides)
	cases := []struct {
		cat    string
		comp   float64
		want   any // float64 or nil
		vIndex int
	}{
		{"math", 1.3, float64(2), 0},
		{"math", 0.5, nil, 1},
		{"greeting_chitchat", 0, float64(0), 2},
		{"other", 2, nil, 3},
	}
	for _, tc := range cases {
		got := routing.ApplyOverrides(tc.cat, tc.comp, ovs)
		switch want := tc.want.(type) {
		case nil:
			if got != nil {
				t.Fatalf("applyOverrides(%s) = %v, want nil", tc.cat, *got)
			}
			// JSON null decodes as untyped nil; the generator writes either
			// literal null (from ?? on null) or the string "null".
			switch vv := v.ApplyOverrides[tc.vIndex].(type) {
			case nil:
			case string:
				if vv != "null" {
					t.Fatalf("vector[%d] = %q, want \"null\"", tc.vIndex, vv)
				}
			default:
				t.Fatalf("vector[%d] = %#v, want null", tc.vIndex, vv)
			}
		case float64:
			if got == nil || *got != types.TierId(want) {
				t.Fatalf("applyOverrides(%s) = %v, want %v", tc.cat, got, want)
			}
			f, ok := v.ApplyOverrides[tc.vIndex].(float64)
			if !ok || int64(f) != int64(want) {
				t.Fatalf("vector[%d] = %v, want %v", tc.vIndex, v.ApplyOverrides[tc.vIndex], want)
			}
		}
	}
}

func TestStickyEscapeParity(t *testing.T) {
	v := loadVectors(t)
	if len(v.StickyEscape) != 3 {
		t.Fatalf("expected 3 stickyEscape vectors, got %d", len(v.StickyEscape))
	}
	cfg := config.DefaultConfig()
	bands := cfg.Policy.Default.ComplexityToTier
	ovs := policyOverrides(cfg.Policy.Overrides)
	esc := routing.StickyEscape(
		1,
		cls(func(c *types.ClassificationResult) { c.Category = "math"; c.Complexity = 1.5 }),
		bands, ovs, cfg.Sticky.EscapeConfidence,
	)
	assertTierPtr(t, esc, v.StickyEscape[0], "escape high confidence")
	esc = routing.StickyEscape(
		1,
		cls(func(c *types.ClassificationResult) { c.Category = "math"; c.Complexity = 1.5; c.CategoryConfidence = 0.3; c.ComplexityConfidence = 0.3 }),
		bands, ovs, cfg.Sticky.EscapeConfidence,
	)
	assertTierPtr(t, esc, v.StickyEscape[1], "escape low confidence")
	esc = routing.StickyEscape(
		2,
		cls(func(c *types.ClassificationResult) { c.Complexity = 0.1 }),
		bands, ovs, cfg.Sticky.EscapeConfidence,
	)
	assertTierPtr(t, esc, v.StickyEscape[2], "no downgrade on sticky escape")
}

// --- helpers ---

func findVector(v vectorsShape, name string) policyVector {
	for _, p := range v.Policy {
		if p.Name == name {
			return p
		}
	}
	panic("vector not found: " + name)
}

func assertNotes(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("notes = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("notes[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func assertTierPtr(t *testing.T, got *types.TierId, want *int, label string) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Fatalf("%s: got %d, want nil", label, *got)
	case want != nil && (got == nil || *got != types.TierId(*want)):
		t.Fatalf("%s: got %v, want %d", label, got, *want)
	}
}

var _ = fmt.Sprintf // keep fmt for future assertion messages