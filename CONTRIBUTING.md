# Contributing to FluxRouter

Thanks for considering a contribution.

## Quick start (Windows / PowerShell)

```powershell
git clone <fork-url> && cd FluxRouter
npm install
npm test                 # 41/41 must pass before any PR
npm run typecheck        # tsc --noEmit must be clean
node cli/flux.ts config validate   # config sanity
```

## Ground rules

1. **Routing behaviour is config, not code.** Tier models, complexity bands, category
   overrides, thresholds, and cost caps live in `fluxrouter.config.json`. Prefer a config
   change over a code change when the router's *policy* changes; code changes are for
   new capabilities or bug fixes.
2. **Document behaviour changes.** User-visible changes (policy defaults, reason codes,
   config fields, route-card shape) must update `CHANGELOG.md` and, when a check
   changes, `TESTING.md`.
3. **No new heavy dependencies.** Current runtime deps: hono, @hono/node-server,
   undici, commander. Anything beyond small pure-JS utils needs justification in the PR.
4. **Cost-guard everything.** Any feature that can spend upstream money (evals, replays,
   new endpoints) must implement a pre-flight budget guard like the eval runners do.
   Silent spend is a design bug.
5. **Never mutate response bodies.** The proxy passes streams through; clients expect
   byte-identical OpenAI shapes. If you must annotate, use `X-Flux-*` headers.
6. **Routing decisions are logged.** New route reasons must be added to the
   `RouteReason` union in `src/types.ts` AND the list in README's "Route cards" section.
7. **Tests required.** New logic in `src/policy.ts`, `src/cost.ts`, `src/config.ts`
   requires table-driven tests. Every reason code must be covered by at least one test
   (see existing patterns in `tests/unit.test.ts`).

## Commit style

- Conventional Commits: `feat:`, `fix:`, `test:`, `docs:`, `chore:`, `refactor:`
- One logical change per commit; subject ≤ 72 chars.

## PR checklist

- [ ] `npm test` green (all tests, new tests included)
- [ ] `npm run typecheck` clean
- [ ] `flux config validate` still passes with the shipped default config
- [ ] CHANGELOG updated for behavior changes
- [ ] If spending money is possible: budget guard implemented + tested (including the
      "refuse to start" path)
- [ ] Route-card fields documented if extended

## Reporting issues

Include: Node version (`node --version`), OS, `fluxrouter.config.json` (redact keys —
they should never be in there anyway), the relevant route-card JSONL lines (metadata
only; fine to scrub), and what `npm test` says.

## Where things are

| Area | Path |
|---|---|
| Manual test walkthrough | `TESTING.md` |
| How a routing decision is made | `docs/HOW_IT_DECIDES.md` |
| Eval results explained | `docs/EVALS.md` |
| Roadmap (v0.2 / v0.3 / v0.4) | `ROADMAP.md` |
| Language analysis (why the Go rewrite) | `docs/LANGUAGE_ANALYSIS.md` |
| Config schema | `schema/fluxrouter.schema.json` |