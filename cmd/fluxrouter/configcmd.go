package main

import (
	"fmt"
	"os"

	"github.com/abhijeet/fluxrouter/internal/config"
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