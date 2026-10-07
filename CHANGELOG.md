# Changelog

All notable changes to FluxRouter are documented here.
Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versioning: [SemVer](https://semver.org/).

## [Unreleased]

### Added — Go engine (v0.3 port, in progress)
The TypeScript engine's port to Go, proven decision-identical by a golden-fixture
parity gate (`fluxrouter parity` → 1,272/1,272 recorded TS decisions match, cost
within 1e-9). Single static binary built with Go 1.27 (stdlib + CGo-free SQLite):
`fluxrouter serve | report | trace | config | parity`. Route cards, config file,
headers, and CLI semantics are unchanged; TS-written databases remain readable
(cross-read proven). The frozen TS reference lives in `old/` (fixture generator +
eval suite until the eval port lands). See [ROADMAP.md](ROADMAP.md) stage G1/G2.

### Planned — v0.2
Calibrated routing logic (data-derived bands, category floors), stickiness improvements
(skip classification on sticky hits, task-boundary downgrade), and sustained validation
inside OpenCode / OpenChamber with a published real-traffic report. See
[ROADMAP.md](ROADMAP.md).

### Planned — v0.3
The Go rewrite: the engine ported route-by-route behind a parity harness (golden
fixtures from v0.2, then a week of live shadow dual-run), shipped as a single static
binary for all platforms. Config, route-card schema, headers, and CLI stay identical —
the only visible change is how it installs. See

### Planned — v0.4
Context economics: compaction, trimming stale tool output, and Jev-driven reduction of
injected skills / tool schemas, plus prompt-cache-aware routing.

## [0.1.0] — foundation

First version. Proves the routing decision and the cost story end to end.

### Added

- **`flux trace <prompt>`**: runs the real classify → policy pipeline on a single prompt
  and prints every intermediate step (state sent to Jev, its typed answers, context gate,
  each policy rule and whether it fired, final tier/model/cost, saving vs frontier).
  `--offline --category/--complexity/--confidence/--noul` drives the policy engine with
  no network and no spend.
- **`docs/EVALS.md`**: eval results explained — what Stage 1 measures (and does not),
  how to read each column, the three raw runs with their code state, six findings
  (labels were the weak point; a falling score once meant an improving router; 7–9%
  under-routing on terse bug reports; tier 3 never fires; timeouts rare and safe), tuning
  levers, and the limitations of the measurements.
- **`docs/HOW_IT_DECIDES.md`**: worked walkthrough with real dataset prompts (greeting,
  GSM8K, textbook probability, MATH-500 level 5, a Django bug report, the "go" contract
  case, the prime-proof boundary case) mapped to the exact code path and each policy rule.
- **Measured eval results in README**: Stage-1 routing accuracy (362 prompts, $0.36/run,
  95.9% within ±1 tier, trivial contract 100%) with tier distribution, Jev complexity
  histograms, and the honest limitations found while measuring. Cost-plan and
  acceptance-run updated with real spend ($0.0061 across 16 requests → 96.8% vs
  all-frontier) instead of projections only.
- **Eval observability**: per-prompt audit JSONL (`.fluxrouter/eval-routing-*.jsonl`),
  tier-distribution table, Jev complexity histogram, worst-misroute listing with the
  prompt text, and `--only <sources>` for cheap targeted re-runs. Dataset failures are
  non-fatal. MATH-500 expectations derive from the dataset's difficulty levels.
- **`.env` support** (`src/env.ts`): automatic `.env` / `.env.local` loading via Node's
  built-in `process.loadEnvFile` (no dependency); precedence real env > `.env.local` >
  `.env`; `--env-file <path>` override on the server; `.env.example` template.
- **OpenAI-compatible proxy server** (Hono + `@hono/node-server`, Node 24):
  `POST /v1/chat/completions` (stream + non-stream), `GET /v1/models` (alias `flux`),
  `GET /health`, `GET /metrics` (Prometheus text).
- **Jev classification** (`src/classify.ts`): one System One call per routing decision —
  `category` Choice (10 options), `complexity` Score (3 levels), `is_trivial` Noul;
  pinned `jev-1.13.0`; 700ms timeout; head+tail conversation excerpts.
- **Policy engine** (`src/policy.ts`): 2D config table (category × complexity → tier),
  trivial bypass (noul > 0.85 → tier 0), low-confidence escalation (< 0.5 → tier 2),
  context gate (drop models whose window can't fit), per-request cost guard
  ($0.25 default → nearest-fitting cheaper tier), sticky tier reuse.
- **Four-tier ladder** on Ollama Cloud: nemotron-3-nano / glm-5.3-flash /
  deepseek-v4-pro / kimi-k3 (all rates, context windows, and ids configurable) with
  OpenRouter failover/burst lane and next-tier-up fallback ladder.
- **Sticky sessions with upward escape** (`src/session.ts`): pin on first route; upgrade
  when a later turn classifies ≥1 tier higher at confidence ≥ 0.6; 6h inactivity TTL.
- **Route cards** (`src/cardlog.ts`): JSONL + SQLite (`node:sqlite`) per request —
  category, complexity, confidence, tier, reason code, usage, actual vs projected cost,
  latencies; `X-Flux-Route-*` response headers; 3 indexes.
- **`flux` CLI** (`cli/`): `report` (day/tier/category/model/session aggregations with
  all-frontier counterfactual + saving %), `config validate`, and the eval suite.
- **Stage-1 routing eval** (`cli/eval/routing.ts`): ~500 labeled public-benchmark prompts
  (GSM8K, MATH-500, SWE-bench Lite, hand-written trivial + explain sets), classification
  only, ±1-tier and exact accuracy matrices, ~$0.50/run, hard $1 budget guard that
  refuses to start above cap.
- **Stage-2 e2e eval** (`cli/eval/e2e.ts`): stratified 30/30/30/30 across tiers, exact
  grading where ground truth exists, Jev-as-judge Score rubric otherwise, ≤ $10 guarded.
- **Config system** (`src/config.ts`): defaults + JSON deep-merge + structural
  validation (tier contiguity, rate sanity, duplicate-model detection, known categories).
- **33 unit tests** (`tests/unit.test.ts`, `node --test`) covering policy, cost math,  config validation/merge, sticky store, Jev parsing, usage/cost extraction.- **Security posture**: loopback-only bind by default (non-loopback refuses to start
  without `FLUX_AUTH_TOKEN`), env-only keys, cards carry metadata not message content.
- **Route card latency bug fixed**: Jev classification latency is now carried through
  the decision into `card.latenciesMs.jev` (previously always 0).
- **Jev-failure fallback is now fail-safe**: unreachable Jev routes to `jev.fallbackTier`
  (default 1/flash, context-gated) instead of the cheapest tier — under-routing hard
  work is the worse failure. Configurable via `jev.fallbackTier`.
- **Tier 3 reachability fix**: the shipped `complexityToTier` table used a 2.5 threshold,
  but Jev's score maxes at 2.0, making frontier mathematically unreachable. Bands are now
  `[0.8→0, 1.6→1, 2.0→2, ∞→3]`, with a regression test asserting all four tiers are
  reachable from the score alone. (Provisional — the Stage-1 eval tunes them.)
- **Upstream model IDs corrected** against the live Ollama Cloud model list:
  `nemotron-3-nano:30b`, `deepseek-v4-pro:0813` (previously bare names → upstream 404).
- **Proxy header fix**: upstream `content-length` / `content-encoding` /
  `transfer-encoding` are no longer passed through (undici already decodes the body),
  which previously terminated client sockets mid-response.
- **Postman collection** (`postman/FluxRouter.postman_collection.json`) with tier
  assertions for hello / textbook / competition / streaming requests.
