# FluxRouter

**A Jev-classified, tier-based OpenAI-compatible LLM router with a full audit trail.**

> **Status: v0.1 — foundation, Go engine (v0.3 port in progress).** Build from
> source with Go 1.27+ (`go build ./cmd/fluxrouter` — one static binary, no
> runtime dependencies). Roadmap: [v0.2 = calibrated routing + real-harness validation,
> v0.3 = the Go rewrite (single binary, proven by its own audit trail),
> v0.4 = context economics](ROADMAP.md).

FluxRouter is a local OpenAI-compatible proxy that classifies every chat request with
[Jev](https://jevwiki.ai) (TypeSafe's "System One" classifier — typed decisions with
calibrated confidence) and routes it to one of **four cost/performance tiers** running on
your existing Ollama Cloud credits (OpenRouter as failover/burst). It is built to make
sure "hello" never costs frontier-model money, while genuinely hard reasoning still
reaches frontier models — and it records **every routing decision** as a route card you
can grep, query, and report on.

```text
OpenChamber / OpenCode / any OpenAI client
        │  POST /v1/chat/completions  (model: "flux")
        ▼
┌──────────────────────────── FluxRouter (localhost:8787) ─────────────────────────┐
│ 1 token estimate → 2 context gate → 3 Jev classify → 4 policy table             │
│ → 5 confidence escalation → 6 cost guard → 7 sticky pin → 8 forward+failover     │
│ → 9 route card (JSONL + SQLite) → response with X-Flux-Route-* headers           │
└──────────────────────────────────────────────────────────────────────────────────┘
        │                                  │
        ▼ tier 0..3                        ▼ failover
   Ollama Cloud (primary)           OpenRouter (burst/failover)
   nemotron-3-nano / glm-5.3-flash /
   deepseek-v4-pro / kimi-k3
```

## Why it's different

Existing routers are popularity-ranked (OpenRouter auto → community spend),
recommendation-only (NotDiamond), closed (Ramp), or dormant (RouteLLM). The neutral
RouterArena benchmark found leading routers **over-select expensive models** and none
expose an auditable decision. FluxRouter's differentiators:

- **Calibrated-confidence routing** — Jev returns a probability, not prose; confidence
  below threshold escalates (never gambles down).
- **Trivial bypass** — a `noul` gate guarantees greetings land on nano tier ($0.00003,
  not $0.02).
- **Context gate + per-request cost cap** — a 300k-token request can never silently hit
  a long-context $75/M-output model; the guard downgrades and logs why.
- **Route cards** — every request logs category, complexity, confidence, chosen tier,
  reason code, actual cost, and latencies (JSONL + SQLite). `fluxrouter report` reconciles
  actual spend vs an all-frontier counterfactual so savings are *measured*, not claimed.
- **Budget-guarded evals** — `eval routing` (TS suite in `old/`) measures routing accuracy for ~$0.50;
  a pre-flight guard refuses to spend above your configured cap ($1 routing / $10 e2e).

## Requirements

- **Go 1.27+** (builds one static binary; no Node.js/runtime dependencies)
- **API keys in env:**
  - `OLLAMA_API_KEY` — **required** — [ollama.com](https://ollama.com) (Pro/Max plan credits)
  - `TYPESAFE_API_KEY` — **required** — [TypeSafe](https://docs.typesafe.ai) (Jev; ~$0.001/decision,
    output tokens free — see [Jev API](https://www.jevtypesafeai.com/how-to-use))
  - `OPENROUTER_API_KEY` — **optional** — [openrouter.ai](https://openrouter.ai); adds the
    failover/burst lane. Without it the server boots with a warning and failover uses
    Ollama tiers only (same-tier alternate → next tier up).

Without the Jev key the server still runs but every request degrades to the cheapest
fitting tier (`jev_unavailable_fallback`) — routing is blind, so treat that as a
test-only mode.

### Keys via `.env`

Instead of shell exports, you can keep keys in a `.env` file next to the project:

```powershell
Copy-Item .env.example .env
notepad .env      # fill in OLLAMA_API_KEY, TYPESAFE_API_KEY, optional OPENROUTER_API_KEY
./fluxrouter.exe serve
```

- `.env` and `.env.local` are both loaded automatically (gitignored).
- Precedence: **real environment variables** > `.env.local` > `.env`.
- Point at a different file: `./fluxrouter.exe serve --env-file C:\path\to\keys.env`
  (the `old/` TS eval CLI also loads `.env`/`.env.local`).
- Nothing is written back to disk; keys stay wherever you put them.

```powershell
git clone <your-fork> FluxRouter
cd FluxRouter
go build -o fluxrouter.exe ./cmd/fluxrouter
Copy-Item .env.example .env && notepad .env       # or export vars in your shell
./fluxrouter.exe serve
```

## Connecting OpenChamber (or any OpenAI client)

**OpenChamber** → Settings → Providers → **Add custom provider**:
- Base URL: `http://127.0.0.1:8787/v1`
- API key: anything (set `FLUX_AUTH_TOKEN` and use it here if you want proxy auth)
- Model: `flux` (the alias; FluxRouter picks the real model per request)

**OpenCode**: add a provider with the same base URL + model `flux`.

The full acceptance walkthrough (7 checks incl. "hello → tier 0", hard math → tier ≥ 2,
streamed code, Jev timeout resilience, eval within budget) is in **[TESTING.md](TESTING.md)**.

## CLI

One binary — `fluxrouter` — with subcommands (the frozen TS tree in `old/` still
provides the eval suite until that subcommand is ported):

```sh
fluxrouter serve               # start the OpenAI-compatible proxy (default)
fluxrouter config validate     # validate fluxrouter.config.json, print tiers
fluxrouter report --by tier     # spend/perf by day | tier | category | model | session
fluxrouter trace "your prompt"  # show the FULL decision for one prompt (see below)
fluxrouter parity fixtures/golden.jsonl   # prove routing matches the recorded TS-engine truth
```

Eval suite (TS-only until the port — run from `old/` with Node):

```sh
cd old && node cli/flux.ts eval routing --dry-run   # plan + projected cost, no spend
cd old && node cli/flux.ts eval routing              # stage 1: classification only  (~$0.36, cap $1)
cd old && node cli/flux.ts eval e2e                  # stage 2: real generation + grading (cap $10)
```

### Understanding a routing decision (`fluxrouter trace`)

`fluxrouter trace` runs the **real** classifier and policy on one prompt and prints every
intermediate step — the state sent to Jev, its typed answers, the context gate, each
policy rule and whether it fired, and the final tier/model/cost:

```sh
fluxrouter trace "Prove that there are infinitely many primes. Justify every step."
```

```
STEP 1 — Jev classification
  category = math   complexity = 0.78   is_trivial = 0.01
STEP 2 — Context gate (all tiers fit a small request)
STEP 3 — Policy rules, in order
  3a trivial bypass?  no
  3b category override? math → no override
  3c complexity bands? 0.78 → tier 0
  3d low-confidence?  no
  3e cost guard?  no
RESULT: tier 0 (nano) · nemotron-3-nano:30b · 98.4% cheaper than frontier
```

That output is itself a finding: a prime-proof task scoring 0.78 sits just under the
`0.8 → tier 0` boundary, so it lands on nano. Lower the first band to fix it.

To explore the policy engine with **no network and no spend**:

```sh
fluxrouter trace "anything" --offline --category math --complexity 1.7 --confidence 0.9
```

`--offline` injects the classification values you supply (`--category`, `--complexity`,
`--confidence`, `--noul`), so you can test band changes instantly.

## Configuration

Everything routing-related lives in `fluxrouter.config.json` — tier ladder (models,
rates, context windows), the category×complexity policy table, thresholds
(`minConfidence`, `trivialNoul`), cost caps, eval caps. Edit the table to re-tune
routing without touching code; `fluxrouter config validate` tells you if you broke it.

```jsonc
{
  "policy": {
    "overrides": [
      { "category": "math", "minComplexity": 1.2, "tier": 2 },
      { "category": "greeting_chitchat", "tier": 0 }
    ]
  }
}
```

Defaults: tier 0 `nemotron-3-nano:30b` ($0.06/$0.24), tier 1 `glm-5.3-flash`
($0.15/$0.50), tier 2 `deepseek-v4-pro:0813` ($0.66/$1.98), tier 3 `kimi-k3` ($3/$15) —
all on Ollama Cloud, all editable.

## Default tier ladder

| Tier | Model | $/M in | $/M out | Routed when |
|---|---|---|---|---|
| 0 nano | nemotron-3-nano:30b | 0.06 | 0.24 | trivial noul > 0.85, greetings, Jev complexity < 0.8 |
| 1 flash | glm-5.3-flash | 0.15 | 0.50 | complexity < 1.6 (routine chat/explain, textbook math) |
| 2 mid | deepseek-v4-pro:0813 | 0.66 | 1.98 | complexity < 2.0, math ≥ 1.2 override, low confidence |
| 3 frontier | kimi-k3 | 3.00 | 15.00 | Jev complexity = 2.0 (its "Hard" top), or a `tier: 3` override |

> These bands are deliberately provisional: Jev's 0–2 score is what the Stage-1
> routing eval (`eval routing` (TS suite in `old/`), ~$0.50) exists to calibrate. Run it against real
> prompts before trusting the ladder.

OpenRouter is the failover/burst lane: same-model equivalents per tier (configurable
`failoverId`s), then next-tier-up, then verbatim upstream error.

## Route cards

Every proxied request appends one JSONL line + one SQLite row in `.fluxrouter/`:

```json
{
  "ts": "2026-09-30T12:00:00.000Z", "sessionId": "a1b2c3…",
  "tier": 0, "model": "nemotron-3-nano", "upstream": "ollama",
  "reason": "trivial_bypass", "category": "greeting_chitchat",
  "complexity": 0.2, "confidence": 0.91,
  "requestTokensEst": 34,
  "usage": { "input": 30, "output": 28, "cachedInput": 0 },
  "costUsd": 0.0000149, "projectedCostUsd": 0.0000192,
  "latenciesMs": { "jev": 240, "upstream": 780, "total": 1030 },
  "status": 200
}
```

Reason codes: `trivial_bypass`, `policy`, `low_confidence_escalation`, `sticky`,
`sticky_escape_up`, `cost_guard`, `context_gate`, `jev_timeout_fallback`,
`jev_unavailable_fallback`, `upstream_exhausted`.

Responses also carry `X-Flux-Route-Tier/Model/Reason/Session` headers.

## Metrics & health

- `GET /health` — liveness
- `GET /v1/models` — advertises the single alias `flux` (the router chooses the real
  model). Set `server.listTierModels: true` to also list each tier's concrete model
  read-only (tagged `x_flux_tier` / `x_flux_upstream`) for discoverability.
- `GET /metrics` — Prometheus text (requests, per-tier counters, cost micro-USD,
  Jev failures, latency p50/p90/p99)

## Security notes

- Binds `127.0.0.1` by default. Binding anything wider requires `FLUX_AUTH_TOKEN`
  (the server refuses to start otherwise).
- API keys are read from the environment only; never logged, never written to config.
- **Prompt injection**: FluxRouter sends conversation excerpts (head+tail, ~2k chars) to
  Jev for classification. A malicious message could try to influence the *routing
  decision*. Blast radius is limited to model/tier choice — Jev never executes anything.
  Known limitation of v1; excerpt sanitization is on the v2 list.
- Route cards contain prompts metadata (category/tokens), **not** message content.

## Eval methodology

Three stages, each with a hard budget guard that refuses to run over cap:

1. **Stage 1 — routing only** (`eval routing` (TS suite in `old/`)): public-benchmark-labeled prompts
   (GSM8K / MATH-500 / SWE-bench Lite / hand-written trivial set) are classified, never
   generated — measuring whether Jev's difficulty judgement matches the dataset labels.
   ~$0.36/run, cap $1.
2. **Stage 2 — end-to-end** (`eval e2e` (TS suite in `old/`)): stratified across tiers, exact-match
   grading where ground truth exists, Jev-as-judge otherwise, with a 10% LLM-judge
   cross-check. Cap $10.
3. **Stage 3 — shadow replay** (free): re-run recorded route cards through policy changes
   offline.

**Quality bar:** 100% greeting→tier-0, ≥85% routing accuracy within ±1 tier, ≥90% quality
retention vs all-frontier at a fraction of the cost.

### Measured results

**Stage 1 — routing only** (`eval routing` (TS suite in `old/`), 362 prompts, **$0.36/run**). One full run
with the default ladder (2026-09-30, Jev `jev-1.13.0`). This is a snapshot, not a
guarantee — re-run it on your own traffic.

| Source | n | Within ±1 tier | Exact | Tier 0 | Mean Jev complexity | Max | Timeouts |
|---|---|---|---|---|---|---|---|
| trivial (hand-written) | 52 | **100%** | **100%** | 100% | 0.00 | 0.12 | 0 |
| explain_hand | 10 | **100%** | **100%** | 100% | 0.04 | 0.25 | 0 |
| gsm8k (grade-school math) | 100 | **100%** | **100%** | 37% | 0.72 | 0.99 | 1 |
| math500 (competition math) | 100 | **96%** | 58% | 29% | 0.88 | 1.94 | 0 |
| swebench_lite (real bug fixes) | 100 | 89% | 46% | 11% | 1.10 | 1.87 | 1 |
| **TOTAL** | **362** | **95.9%** | **73.5%** | — | — | — | 2 |

**Trivial contract holds:** 52/52 greetings and chitchat routed to tier 0
("hello never hits a frontier model"), at ~$0.00001 each.

**Tier distribution and Jev scores** (the tuning surface):

| Source | t0 | t1 | t2 | t3 | Jev <0.5 | <1.0 | <1.5 | <2.0 |
|---|---|---|---|---|---|---|---|---|
| trivial | 100% | 0% | 0% | 0% | 52 | 0 | 0 | 0 |
| explain_hand | 100% | 0% | 0% | 0% | 10 | 0 | 0 | 0 |
| gsm8k | 37% | 54% | 9% | 0% | 24 | 76 | 0 | 0 |
| math500 | 29% | 37% | 34% | 0% | 18 | 49 | 18 | 15 |
| swebench_lite | 11% | 43% | 46% | 0% | 6 | 38 | 38 | 18 |

**What the numbers revealed**

- **Tier 0 absorbs easy traffic** (37% of grade-school math, all greetings) — routing
  buys real savings rather than relabeling the same spend.
- **±1-tier agreement is the metric that matters**; the `exact` column is partly an
  artifact of the eval's own `expectedTiers` labels. MATH-500's labels come from the
  dataset's difficulty levels; SWE-bench has no difficulty field, so its `exact` row is
  the least trustworthy (`[2,3]` is a blanket guess there).
- **Under-routing is the risk, not over-spending.** ~7% of genuinely hard prompts still
  land on nano — concentrated in **terse SWE-bench issue titles**, where a one-line
  description understates the work (a Django validator fix scored complexity 0.25).
  Prepending the repo name (`Repository: django/django`) improved this; it did not
  eliminate it.
- **Tier 3 (frontier) rarely fires.** Jev's 0–2 scale is conservative — even a cubic
  proof scored 1.01 — so the mid tier carries hard work. The bands are provisional by
  design and meant to be tuned from these histograms.
- **Confidence escalation cannot catch the terse-issue case** (those misroutes had
  adequate confidence, 0.41–0.68). The evidence-backed mitigation for coding traffic is a
  category floor, e.g. `{"category":"code_debug","tier":2}`.

**Reproduce**

```sh
cd old && node cli/flux.ts eval routing --dry-run        # plan + projected cost, no spend
cd old && node cli/flux.ts eval routing                    # full run, ~$0.36 (cap $1)
cd old && node cli/flux.ts eval routing --only trivial,math500  # targeted re-run, pennies
```

Each run writes a per-prompt audit trail to `.fluxrouter/eval-routing-*.jsonl` and prints
tier distribution + Jev complexity histograms, so band tuning is data-driven.

> **Not yet measured:** Stage 2 end-to-end answer quality (does `deepseek-v4-pro` actually
> *solve* the hard prompts?) — `eval e2e` (TS suite in `old/`), ≤ $10/run. Routing accuracy only proves
> Jev's difficulty judgment matches dataset labels; it does not prove the answers are good.
> Until Stage 2 runs, treat the tier→model mapping as designed, not validated.

## Project layout

```
cmd/fluxrouter/  the single Go binary (serve, report, trace, config, parity)
internal/        the Go engine: routing (cost+policy+session), jev, config,
                 upstream, router, telemetry (cardlog+metrics), server, compat, types
old/             frozen TypeScript engine (v0.1) — fixture generator + eval suite
fixtures/        golden.jsonl — 1,272 recorded TS-engine decisions (parity oracle)
schema/          JSON schema for fluxrouter.config.json
docs/            HOW_IT_DECIDES.md, EVALS.md, LANGUAGE_ANALYSIS.md
test/e2e/        binary-level end-to-end test (go test ./test/e2e)
```

Testing your own install: **[TESTING.md](TESTING.md)**.
How a routing decision is made: **[docs/HOW_IT_DECIDES.md](docs/HOW_IT_DECIDES.md)**.
Eval output explained, with the raw runs: **[docs/EVALS.md](docs/EVALS.md)**.

## Roadmap

- **v0.1 (current)** — foundation: proxy, Jev routing, 4-tier ladder, route cards, eval,
  `fluxrouter trace`. Measured 95.9% ±1-tier routing accuracy, trivial contract 100%.
- **v0.2 (next)** — calibrated routing logic (bands derived from eval data, coding
  category floors), stickiness improvements (skip classification on sticky hits,
  task-boundary downgrade), sustained validation inside OpenCode / OpenChamber with a
  published real-traffic report, and the v0.3 groundwork: golden-decision fixtures and
  a frozen compatibility contract.
- **v0.3** — the Go rewrite: single ~15MB static binary, parity-proven against the TS
  engine via golden fixtures + a week of live shadow dual-run, then published as release
  assets for all platforms. See [docs/LANGUAGE_ANALYSIS.md](docs/LANGUAGE_ANALYSIS.md).
- **v0.4** — context economics: compaction, trimming stale tool output, and reducing the
  skills / tool schemas injected per prompt based on the classified task; prompt-cache-aware
  routing.

Full detail and exit criteria: [ROADMAP.md](ROADMAP.md).

## License

MIT — see [LICENSE](LICENSE).
