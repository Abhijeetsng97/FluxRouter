# Contributing to FluxRouter

Thanks for considering a contribution.

## Quick start (Windows / PowerShell)

```powershell
git clone <fork-url> && cd FluxRouter
go build -o fluxrouter.exe ./cmd/fluxrouter   # builds the single static binary
go test ./... -count=1                          # all tests must pass before any PR
go vet ./...                                   # must be clean
./fluxrouter.exe parity fixtures/golden.jsonl  # 1272/1272 — routing parity with the TS engine
./fluxrouter.exe config validate               # config sanity
```

## Ground rules

1. **Routing behaviour is config, not code.** Tier models, complexity bands, category
   overrides, thresholds, and cost caps live in `fluxrouter.config.json`. Prefer a config
   change over a code change when the router's *policy* changes; code changes are for
   new capabilities or bug fixes.
2. **Document behaviour changes.** User-visible changes (policy defaults, reason codes,
   config fields, route-card shape) must update `CHANGELOG.md` and, when a check
   changes, `TESTING.md`.
3. **No new heavy dependencies.** Runtime deps: Go stdlib + `modernc.org/sqlite`
   (CGo-free). Anything beyond small pure-Go utilities needs justification in the PR.
4. **Cost-guard everything.** Any feature that can spend upstream money (evals, replays,
   new endpoints) must implement a pre-flight budget guard like the eval runners do.
   Silent spend is a design bug.
5. **Never mutate response bodies.** The proxy passes streams through; clients expect
   byte-identical OpenAI shapes. If you must annotate, use `X-Flux-*` headers.
6. **Routing decisions are logged.** New route reasons must be added to the
   `RouteReason` union in `internal/types/types.go` AND the list in README's
   "Route cards" section.
7. **Tests required.** New logic in `internal/routing/`, `internal/config/`,
   `internal/jev/` requires table-driven tests. Every reason code must be covered
   by at least one test (see existing patterns in `internal/routing/parity_test.go`).
8. **Routing parity is a gate.** The Go engine must make identical decisions to the
   recorded TS-engine fixtures (`fluxrouter parity`). An intentional band/policy change
   regenerates `fixtures/golden.jsonl` (`node old/scripts/gen-fixtures.mjs`) in the
   **same commit** as the Go change — never a bare fixture flip.

## Commit style

- Conventional Commits: `feat:`, `fix:`, `test:`, `docs:`, `chore:`, `refactor:`
- One logical change per commit; subject ≤ 72 chars.

## PR checklist

- [ ] `go test ./... -count=1` green (all tests, new tests included)
- [ ] `go vet ./...` clean
- [ ] `./fluxrouter.exe parity fixtures/golden.jsonl` → 1272/1272 (or fixtures regenerated
      deliberately, in the same commit as the policy change)
- [ ] `./fluxrouter.exe config validate` still passes with the shipped default config
- [ ] CHANGELOG updated for behavior changes
- [ ] If spending money is possible: budget guard implemented + tested (including the
      "refuse to start" path)
- [ ] Route-card fields documented if extended

## Reporting issues

Include: Go version (`go version`), OS, `fluxrouter.config.json` (redact keys —
they should never be in there anyway), the relevant route-card JSONL lines (metadata
only; fine to scrub), and what `go test ./...` says.

## Where things are

| Area | Path |
|---|---|
| Manual test walkthrough | `TESTING.md` |
| How a routing decision is made | `docs/HOW_IT_DECIDES.md` |
| Eval results explained | `docs/EVALS.md` |
| Roadmap (v0.2 / v0.3 / v0.4) | `ROADMAP.md` |
| Config schema | `schema/fluxrouter.schema.json` |
| The Go engine | `cmd/fluxrouter/` + `internal/` |
| The frozen TS engine (fixtures, eval suite) | `old/` |
| Parity oracle (recorded TS decisions) | `fixtures/golden.jsonl` |
| Binary-level e2e test | `test/e2e/e2e_test.go` |