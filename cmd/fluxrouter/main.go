// Command fluxrouter is the single Go binary: serve | report | trace |
// config | eval | parity (VERSION parity with old/src/version.ts).
package main

import (
	"fmt"
	"os"
)

// VERSION mirrors old/src/version.ts.
const VERSION = "0.1.0"

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && args[0][0] != '-' {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "serve", "start":
		os.Exit(runServe(args))
	case "report":
		os.Exit(runReport(args))
	case "trace":
		os.Exit(runTrace(args))
	case "config":
		os.Exit(runConfig(args))
	case "parity":
		os.Exit(runParity(args))
	case "version", "--version", "-v":
		fmt.Println("fluxrouter v" + VERSION)
		os.Exit(0)
	case "help", "--help", "-h":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`fluxrouter — Jev-classified, tier-based LLM router (Go engine, v0.3)

Usage:
  fluxrouter serve                        start the OpenAI-compatible proxy (default)
      --config, -c <path>                 config file (default: fluxrouter.config.json)
      --port, -p <port>                   override server.port
      --host <host>                       override server.host
      --env-file <path>                   env file (default: .env.local then .env)
  fluxrouter report [--by day|tier|category|model|session] [--today]
  fluxrouter trace <prompt> [--offline --category C --complexity N --confidence N --noul N] [--json]
  fluxrouter config validate              validate fluxrouter.config.json, print tiers
  fluxrouter parity fixtures|<file.jsonl> compare engine decisions against golden fixtures
  fluxrouter version
`)
	os.Exit(0)
}