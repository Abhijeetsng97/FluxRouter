// parity.go: `fluxrouter parity <fixtures.jsonl>` — the G1/G3 acceptance
// harness. Reads golden fixtures (messages + recorded Jev answers + config
// overrides), runs the Go policy engine, and diffs against the recorded
// decisions. Exit 0 = 100% parity; exit 1 = at least one divergence.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"

	"github.com/abhijeet/fluxrouter/internal/config"
	"github.com/abhijeet/fluxrouter/internal/cost"
	"github.com/abhijeet/fluxrouter/internal/policy"
	"github.com/abhijeet/fluxrouter/internal/types"
)

// fixture mirrors one line of fixtures/golden.jsonl (written by
// old/scripts/gen-fixtures.mjs from the TS engine).
type fixture struct {
	Name          string `json:"name"`
	Messages      []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Classification *struct {
		Category             string  `json:"category"`
		Complexity           float64 `json:"complexity"`
		ComplexityConfidence float64 `json:"complexityConfidence"`
		CategoryConfidence   float64 `json:"categoryConfidence"`
		TrivialNoul          float64 `json:"trivialNoul"`
	} `json:"classification"`
	StickyTier     *int    `json:"stickyTier"`
	RequestTokens  int64   `json:"requestTokens"`
	FallbackTier   *int    `json:"fallbackTier"`
	BudgetTokensOut int64  `json:"budgetTokensOut"`
	ConfigPatch    map[string]any `json:"configPatch,omitempty"`
	Expected       struct {
		Tier             int64   `json:"tier"`
		Reason           string  `json:"reason"`
		Category         string  `json:"category"`
		Complexity       float64 `json:"complexity"`
		Confidence       float64 `json:"confidence"`
		ProjectedCostUsd float64 `json:"projectedCostUsd"`
		Notes            []string `json:"notes"`
	} `json:"expected"`
}

func parityMain(args []string) int {
	path := "fixtures/golden.jsonl"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--fixtures":
			i++
			if i < len(args) {
				path = args[i]
			}
		default:
			// Bare positional path (first non-flag argument wins).
			if path == "fixtures/golden.jsonl" && args[i] != "" && args[i][0] != '-' {
				path = args[i]
			}
		}
	}
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open fixtures %s: %v\n", path, err)
		fmt.Fprintln(os.Stderr, "generate first: node old/scripts/gen-fixtures.mjs")
		return 2
	}
	defer f.Close()

	base := config.DefaultConfig()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	total, passed, diverged := 0, 0, 0
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := sc.Text()
		if line == "" {
			continue
		}
		var fx fixture
		if err := json.Unmarshal([]byte(line), &fx); err != nil {
			fmt.Fprintf(os.Stderr, "fixture line %d malformed: %v\n", lineNo, err)
			return 2
		}
		// Build the input with configPatch applied.
		cfg := base
		if fx.ConfigPatch != nil {
			cfg = applyConfigPatch(base, fx.ConfigPatch)
		}
		in := policy.Input{
			RequestTokens:  fx.RequestTokens,
			StickyTier:     tierIdPtr(fx.StickyTier),
			Tiers:          cfg.Tiers,
			FallbackTier:   tierIdPtr(fx.FallbackTier),
			BudgetTokensOut: fx.BudgetTokensOut,
			Config: policy.PolicyConfig{
				MinConfidence:               cfg.Jev.MinConfidence,
				TrivialNoul:                 cfg.Jev.TrivialNoul,
				EscalateOnlyAboveComplexity: cfg.Jev.EscalateOnlyAboveComplexity,
				ComplexityToTier:            cfg.Policy.Default.ComplexityToTier,
				Overrides:                   overridesOfFix(cfg.Policy.Overrides),
				PerRequestCapUsd:            cfg.Cost.PerRequestCapUsd,
				StickyEnabled:               cfg.Sticky.Enabled,
				EscapeConfidence:            cfg.Sticky.EscapeConfidence,
			},
		}
		if fx.Classification != nil {
			in.Classification = &types.ClassificationResult{
				Category:             fx.Classification.Category,
				Complexity:           fx.Classification.Complexity,
				ComplexityConfidence: fx.Classification.ComplexityConfidence,
				CategoryConfidence:   fx.Classification.CategoryConfidence,
				TrivialNoul:          fx.Classification.TrivialNoul,
				JevModel:             "fixture",
			}
		}
		out := policy.RouteRequest(in)
		total++
		ok := out.Tier == types.TierId(fx.Expected.Tier) &&
			string(out.Reason) == fx.Expected.Reason &&
			math.Abs(out.ProjectedCostUsd-fx.Expected.ProjectedCostUsd) <= 1e-9
		if ok {
			passed++
		} else {
			diverged++
			fmt.Printf("DIVERGENCE %q (line %d):\n  got  tier=%d reason=%s cost=%.9f\n  want tier=%d reason=%s cost=%.9f\n",
				fx.Name, lineNo, out.Tier, out.Reason, out.ProjectedCostUsd,
				fx.Expected.Tier, fx.Expected.Reason, fx.Expected.ProjectedCostUsd)
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "read error:", err)
		return 2
	}
	fmt.Printf("parity: %d/%d fixtures match (%.1f%%)\n", passed, total, 100*float64(passed)/math.Max(1, float64(total)))
	if diverged > 0 {
		return 1
	}
	return 0
}

func tierIdPtr(p *int) *types.TierId {
	if p == nil {
		return nil
	}
	t := types.TierId(*p)
	return &t
}

// applyConfigPatch applies the fixture's tier shrinking encoding:
// { tiers: [{index: N, model: {ctx: X}}, ...] }.
func applyConfigPatch(base config.Config, patch map[string]any) config.Config {
	cfg := base // slices are copied below before mutation
	tiersAny, _ := patch["tiers"].([]any)
	if len(tiersAny) > 0 {
		newTiers := make([]types.Tier, len(cfg.Tiers))
		copy(newTiers, cfg.Tiers)
		for _, ta := range tiersAny {
		 tm, _ := ta.(map[string]any)
		 if tm == nil {
			 continue
		 }
		 idx := int(numField(tm["index"]))
		 mm, _ := tm["model"].(map[string]any)
		 if idx < 0 || idx >= len(newTiers) || mm == nil {
			 continue
		 }
		 modelsCopy := make([]types.TierModel, len(newTiers[idx].Models))
		 copy(modelsCopy, newTiers[idx].Models)
		 if ctx, ok := numAny(mm["ctx"]); ok {
			 modelsCopy[0].Ctx = int64(ctx)
		 }
		 newTiers[idx].Models = modelsCopy
		}
		cfg.Tiers = newTiers
	}
	return cfg
}

func numField(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return -1
}

func numAny(v any) (float64, bool) {
	if f, ok := v.(float64); ok {
		return f, true
	}
	return 0, false
}

func overridesOfFix(ovs []config.ConfigOverride) []policy.Override {
	out := make([]policy.Override, 0, len(ovs))
	for _, ov := range ovs {
		out = append(out, policy.Override{Category: ov.Category, MinComplexity: ov.MinComplexity, Tier: ov.Tier})
	}
	return out
}

// configMapOf / deepMergeForParity / configFromMap reuse config internals
// indirectly (map round-trip), keeping parity self-contained.
func configMapOf(c config.Config) map[string]any {
	b, _ := json.Marshal(c)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func deepMergeForParity(target, src map[string]any) map[string]any {
	for k, v := range src {
		vm, vObj := v.(map[string]any)
		tm, tObj := target[k].(map[string]any)
		if vObj && tObj {
			target[k] = deepMergeForParity(tm, vm)
		} else {
			target[k] = v
		}
	}
	return target
}

func configFromMap(m map[string]any) (config.Config, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return config.Config{}, err
	}
	var c config.Config
	if err := json.Unmarshal(b, &c); err != nil {
		return config.Config{}, err
	}
	return c, nil
}

var _ = cost.ProjectedCost // referenced by report frontier-cost math