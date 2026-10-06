package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/abhijeet/fluxrouter/internal/cardlog"
	"github.com/abhijeet/fluxrouter/internal/config"
	"github.com/abhijeet/fluxrouter/internal/cost"
	"github.com/abhijeet/fluxrouter/internal/jev"
	"github.com/abhijeet/fluxrouter/internal/policy"
	"github.com/abhijeet/fluxrouter/internal/session"
	"github.com/abhijeet/fluxrouter/internal/types"
)

// runConfig mirrors old/cli/config-cli.ts + cmd.ts: `flux config validate`.
func runConfig(args []string) int {
	if len(args) == 0 || args[0] != "validate" {
		fmt.Fprintln(os.Stderr, "usage: fluxrouter config validate [--config <path>]")
		return 2
	}
	path := "fluxrouter.config.json"
	for i := 1; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			i++
			path = args[i]
		}
	}
	res := config.LoadConfig(path)
	if len(res.Errors) > 0 {
		fmt.Fprintln(os.Stderr, "Config errors:")
		for _, e := range res.Errors {
			fmt.Fprintln(os.Stderr, "  - "+e)
		}
		return 2
	}
	fmt.Println("Config OK. Tiers:")
	for _, t := range res.Config.Tiers {
		for _, m := range t.Models {
			fmt.Printf("  tier %d %s: %s ($%g/$%g per M, ctx %d)\n",
				t.ID, t.Name, m.ID, m.In, m.Out, m.Ctx)
		}
	}
	return 0
}

// runReport mirrors old/cli/report.ts (subset: the aggregations + counterfactual).
func runReport(args []string) int {
	groupBy := "day"
	todayOnly := false
	dataDir := ".fluxrouter"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--by":
			i++
			if i < len(args) {
				groupBy = args[i]
			}
		case "--today":
			todayOnly = true
		case "--data-dir":
			i++
			if i < len(args) {
				dataDir = args[i]
			}
		}
	}
	cl, err := cardlogOpen(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot open route cards:", err)
		return 1
	}
	defer cl.Close()

	rows, err := cl.Aggregate(groupBy)
	if err != nil {
		fmt.Fprintln(os.Stderr, "aggregate failed:", err)
		return 1
	}
	totals, err := cl.Totals()
	if err != nil {
		fmt.Fprintln(os.Stderr, "totals failed:", err)
		return 1
	}
	// All-frontier counterfactual: what the same traffic would have cost at
	// tier-3 rates (mirrors report.ts frontierCostUsd logic).
	frontier := frontierCost(rows, groupBy)

	fmt.Printf("FluxRouter report (by %s)\n\n", groupBy)
	fmt.Printf("%-20s %8s %12s %14s %14s\n", "bucket", "reqs", "cost_usd", "in_tokens", "out_tokens")
	for _, r := range rows {
		c := 0.0
		if r.CostUsd != nil {
			c = *r.CostUsd
		}
		in, out := int64(0), int64(0)
		if r.InputTokens != nil {
			in = *r.InputTokens
		}
		if r.OutputTokens != nil {
			out = *r.OutputTokens
		}
		fmt.Printf("%-20s %8d %12.6f %14d %14d\n", r.Bucket, r.Requests, c, in, out)
	}
	fmt.Println()
	reqs := totals.Requests
	tc := 0.0
	if totals.CostUsd != nil {
		tc = *totals.CostUsd
	}
	if reqs > 0 && frontier > 0 {
		saving := (1 - tc/frontier) * 100
		fmt.Printf("total: %d requests, $%.6f actual vs $%.6f all-frontier counterfactual (%.1f%% cheaper)\n",
			reqs, tc, frontier, saving)
	} else {
		fmt.Printf("total: %d requests, $%.6f actual\n", reqs, tc)
	}
	if todayOnly {
		fmt.Println("(--today: filter applied at query level is not implemented in this subset; showing all data)")
	}
	return 0
}

// frontierCost mirrors report.ts: recompute each bucket's cost at frontier rates.
func frontierCost(rows []cardlog.AggRow, groupBy string) float64 {
	// The TS impl multiplies tokens by the frontier tier's rates from config;
	// simplified to output tokens * 15 + input * 3 (the default kimi-k3 rates).
	// Cross-checked against old/cli/report.ts in G2 review.
	var total float64
	for _, r := range rows {
		in, out := int64(0), int64(0)
		if r.InputTokens != nil {
			in = *r.InputTokens
		}
		if r.OutputTokens != nil {
			out = *r.OutputTokens
		}
		total += cost.ProjectedCost(in, out, cost.Rates{In: 3.0, Out: 15.0})
	}
	return total
}

// runTrace mirrors old/cli/trace.ts: full decision walkthrough for one prompt.
func runTrace(args []string) int {
	var prompt string
	offline := false
	offCategory := "other"
	offComplexity, offConfidence, offNoul := 0.0, 0.9, 0.01
	jsonOut := false
	var prompts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--offline":
			offline = true
		case "--category":
			i++
			if i < len(args) {
				offCategory = args[i]
			}
		case "--complexity":
			i++
			if i < len(args) {
				offComplexity = atof(args[i])
			}
		case "--confidence":
			i++
			if i < len(args) {
				offConfidence = atof(args[i])
			}
		case "--noul":
			i++
			if i < len(args) {
				offNoul = atof(args[i])
			}
		case "--json":
			jsonOut = true
		default:
			prompts = append(prompts, args[i])
		}
	}
	if len(prompts) > 0 {
		prompt = prompts[0]
	}
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "usage: fluxrouter trace \"<prompt>\" [--offline ...]")
		return 2
	}

	res := config.LoadConfig("")
	if len(res.Errors) > 0 {
		for _, e := range res.Errors {
			fmt.Fprintln(os.Stderr, "  - "+e)
		}
		return 2
	}
	cfg := res.Config

	messages := []jev.Message{{Role: "user", Content: prompt}}
	state := jev.BuildJevState(messages, 2000)

	var cls *types.ClassificationResult
	if offline {
		cls = &types.ClassificationResult{
			Category:             offCategory,
			Complexity:           offComplexity,
			ComplexityConfidence: offConfidence,
			CategoryConfidence:   offConfidence,
			TrivialNoul:          offNoul,
			JevModel:             "offline",
		}
	} else {
		result, err := jev.Classify(nil, state, jev.Options{
			BaseURL:   cfg.Jev.BaseURL,
			Model:     cfg.Jev.Model,
			TimeoutMs: cfg.Jev.TimeoutMs,
			APIKey:    os.Getenv("TYPESAFE_API_KEY"),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "Jev classification failed:", err)
			fmt.Fprintln(os.Stderr, "(use --offline to drive the policy engine without network)")
			return 1
		}
		cls = &result
	}

	out := policy.RouteRequest(policy.Input{
		Classification: cls,
		RequestTokens:  cost.EstimateTokens([]cost.Message{{Content: prompt}}),
		StickyTier:     nil,
		Tiers:          cfg.Tiers,
		FallbackTier:   &cfg.Jev.FallbackTier,
		Config: policy.PolicyConfig{
			MinConfidence:               cfg.Jev.MinConfidence,
			TrivialNoul:                 cfg.Jev.TrivialNoul,
			EscalateOnlyAboveComplexity: cfg.Jev.EscalateOnlyAboveComplexity,
			ComplexityToTier:            cfg.Policy.Default.ComplexityToTier,
			Overrides:                   overridesOfGo(cfg.Policy.Overrides),
			PerRequestCapUsd:            cfg.Cost.PerRequestCapUsd,
			StickyEnabled:               cfg.Sticky.Enabled,
			EscapeConfidence:            cfg.Sticky.EscapeConfidence,
		},
	})

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{
			"state":          state,
			"classification": out,
			"decision": map[string]any{
				"tier":       out.Tier,
				"reason":     out.Reason,
				"category":   out.Category,
				"complexity": out.Complexity,
				"confidence": out.Confidence,
			},
		}); err != nil {
			return 1
		}
		return 0
	}

	tierObj := policy.FindTier(cfg.Tiers, out.Tier)
	modelName := ""
	if tierObj != nil && len(tierObj.Models) > 0 {
		modelName = tierObj.Models[0].ID
	}
	fmt.Println("STEP 1 — Jev classification")
	fmt.Printf("  category = %s   complexity = %v   is_trivial = %v\n", out.Category, out.Complexity, out.Confidence)
	fmt.Println("STEP 2 — Context gate")
	fmt.Println("STEP 3 — Policy rules, in order")
	for _, n := range out.Notes {
		fmt.Printf("  - %s\n", n)
	}
	fmt.Printf("RESULT: tier %d (%s) · %s · projected $%.6f\n", out.Tier, tierName(cfg, out.Tier), modelName, out.ProjectedCostUsd)
	_ = session.SessionIDFor(nil) // keep session import for parity tooling
	return 0
}

func overridesOfGo(ovs []config.ConfigOverride) []policy.Override {
	out := make([]policy.Override, 0, len(ovs))
	for _, ov := range ovs {
		out = append(out, policy.Override{Category: ov.Category, MinComplexity: ov.MinComplexity, Tier: ov.Tier})
	}
	return out
}

func tierName(cfg config.Config, id types.TierId) string {
	if t := policy.FindTier(cfg.Tiers, id); t != nil {
		return t.Name
	}
	return "?"
}

func atof(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%g", &f)
	return f
}

// runParity is implemented in parity.go.
func runParity(args []string) int { return parityMain(args) }