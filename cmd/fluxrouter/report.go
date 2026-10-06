package main

import (
	"fmt"
	"os"

	"github.com/abhijeet/fluxrouter/internal/routing"
	"github.com/abhijeet/fluxrouter/internal/telemetry"
)

// runReport mirrors old/cli/report.ts (subset: aggregations + counterfactual).
func runReport(args []string) int {
	groupBy := "day"
	dataDir := ".fluxrouter"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--by":
			i++
			if i < len(args) {
				groupBy = args[i]
			}
		case "--today":
			// --today filter lands with the SQL WHERE clause; parsed for compat
		case "--data-dir":
			i++
			if i < len(args) {
				dataDir = args[i]
			}
		}
	}
	cl, err := telemetry.NewCardLog(dataDir)
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

	// All-frontier counterfactual: same traffic at the frontier tier's rates
	// (mirrors report.ts frontierCostUsd with the default kimi-k3 rates).
	var frontier float64
	for _, r := range rows {
		var in, out int64
		if r.InputTokens != nil {
			in = *r.InputTokens
		}
		if r.OutputTokens != nil {
			out = *r.OutputTokens
		}
		frontier += routing.ProjectedCost(in, out, routing.Rates{In: 3.0, Out: 15.0})
	}

	fmt.Printf("FluxRouter report (by %s)\n\n", groupBy)
	fmt.Printf("%-20s %8s %12s %14s %14s\n", "bucket", "reqs", "cost_usd", "in_tokens", "out_tokens")
	for _, r := range rows {
		c := 0.0
		if r.CostUsd != nil {
			c = *r.CostUsd
		}
		var in, out int64
		if r.InputTokens != nil {
			in = *r.InputTokens
		}
		if r.OutputTokens != nil {
			out = *r.OutputTokens
		}
		fmt.Printf("%-20s %8d %12.6f %14d %14d\n", r.Bucket, r.Requests, c, in, out)
	}
	fmt.Println()
	tc := 0.0
	if totals.CostUsd != nil {
		tc = *totals.CostUsd
	}
	if totals.Requests > 0 && frontier > 0 {
		fmt.Printf("total: %d requests, $%.6f actual vs $%.6f all-frontier counterfactual (%.1f%% cheaper)\n",
			totals.Requests, tc, frontier, (1-tc/frontier)*100)
	} else {
		fmt.Printf("total: %d requests, $%.6f actual\n", totals.Requests, tc)
	}
	return 0
}