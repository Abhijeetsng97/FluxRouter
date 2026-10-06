package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abhijeet/fluxrouter/internal/config"
	"github.com/abhijeet/fluxrouter/internal/jev"
	"github.com/abhijeet/fluxrouter/internal/routing"
	"github.com/abhijeet/fluxrouter/internal/types"
)

// runTrace mirrors old/cli/trace.ts: full decision walkthrough for one prompt.
func runTrace(args []string) int {
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
	if len(prompts) == 0 || prompts[0] == "" {
		fmt.Fprintln(os.Stderr, `usage: fluxrouter trace "<prompt>" [--offline ...]`)
		return 2
	}
	prompt := prompts[0]

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
		result, err := jev.Classify(context.Background(), state, jev.Options{
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

	out := routing.RouteRequest(routing.Input{
		Classification: cls,
		RequestTokens:  routing.EstimateTokens([]routing.Message{{Content: prompt}}),
		StickyTier:     nil,
		Tiers:          cfg.Tiers,
		FallbackTier:   &cfg.Jev.FallbackTier,
		Config: routing.PolicyConfig{
			MinConfidence:               cfg.Jev.MinConfidence,
			TrivialNoul:                 cfg.Jev.TrivialNoul,
			EscalateOnlyAboveComplexity: cfg.Jev.EscalateOnlyAboveComplexity,
			ComplexityToTier:            cfg.Policy.Default.ComplexityToTier,
			Overrides:                   routingOverrides(cfg.Policy.Overrides),
			PerRequestCapUsd:            cfg.Cost.PerRequestCapUsd,
			StickyEnabled:               cfg.Sticky.Enabled,
			EscapeConfidence:            cfg.Sticky.EscapeConfidence,
		},
	})

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"state":          state,
			"classification": out,
			"decision": map[string]any{
				"tier":       out.Tier,
				"reason":     out.Reason,
				"category":   out.Category,
				"complexity": out.Complexity,
				"confidence": out.Confidence,
			},
		})
		return 0
	}

	tierObj := routing.FindTier(cfg.Tiers, out.Tier)
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
	return 0
}

func routingOverrides(ovs []config.ConfigOverride) []routing.Override {
	out := make([]routing.Override, 0, len(ovs))
	for _, ov := range ovs {
		out = append(out, routing.Override{Category: ov.Category, MinComplexity: ov.MinComplexity, Tier: ov.Tier})
	}
	return out
}

func tierName(cfg config.Config, id types.TierId) string {
	if t := routing.FindTier(cfg.Tiers, id); t != nil {
		return t.Name
	}
	return "?"
}

func atof(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%g", &f)
	return f
}