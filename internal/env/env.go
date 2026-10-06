// Package env mirrors old/src/env.ts: .env loading with JS-like precedence.
//
// Precedence (highest first):
//  1. real process environment (never overridden)
//  2. .env.local
//  3. .env
//  4. explicit --env-file <path> (replaces the default candidates)
//
// TS used process.loadEnvFile, which never overrides set variables and loads
// highest-precedence first. Go has no built-in loader, so this implements
// dotenv parsing with the same never-override rule.
package env

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Result mirrors env.ts DotEnvResult.
type Result struct {
	Loaded []string
}

// Load mirrors env.ts loadDotEnv: candidates in precedence order; a file only
// fills keys not already present (in process env OR loaded by an earlier file).
func Load(explicitPath string) Result {
	candidates := []string{".env.local", ".env"}
	if explicitPath != "" {
		candidates = []string{explicitPath}
	}
	res := Result{Loaded: []string{}}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := loadFile(p); err != nil {
			fmt.Printf("warning: failed to load env file %s: %s\n", p, err.Error())
			continue
		}
		res.Loaded = append(res.Loaded, p)
	}
	return res
}

// loadFile parses KEY=VALUE lines, exporting only keys not already set.
// Supports the same forms Node's loader does: optional `export ` prefix,
// single/double-quoted values, blank lines and # comments.
func loadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "" {
			continue
		}
		// Strip matching quotes.
		if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); exists {
			continue // never override real env or earlier file
		}
		if err := os.Setenv(key, val); err != nil {
			return err
		}
	}
	return sc.Err()
}