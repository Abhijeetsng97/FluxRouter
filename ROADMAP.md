# FluxRouter Roadmap

Direction, not promises. Each version is gated on the previous one being *measured*,
not just shipped.

**The arc:** v0.1 proved the routing decision and the cost story. v0.2 makes the
decision *measured* (data-derived bands, real agent traffic). v0.3 makes it
*installable everywhere* — a parity-proven rewrite in Go, shipped as a single binary.
v0.4 makes the *request body* the budget. v1.0 opens the ecosystem.

**What makes FluxRouter different** (and every version below strengthens one of these):
calibrated-confidence routing (never gambles down), an auditable decision per request
(route cards), budget-guarded evals that measure routing quality for <$1, and — from
v0.3 — the only router whose rewrite is *proven by its own audit trail*: the Go port
must reproduce the TypeScript engine's decisions on golden fixtures and live shadow
traffic before it ships.

## v0.1 — foundation (current)

**Theme: prove the routing decision and the cost story.**

- OpenAI-compatible proxy: one stable `flux` alias, Jev decides the model.
- One Jev call per request (category + complexity + triviality), pinned `jev-1.13.0`.
- 4-tier ladder on Ollama Cloud, OpenRouter failover/burst.
- Trivial bypass (greetings/chitchat → nano), context gate, per-request cost guard.
- Sticky sessions with upward escape.
- Route cards (JSONL + SQLite) + `flux report` with all-frontier counterfactual.
- `flux trace` and a budget-guarded Stage-1 routing eval.
- **Measured:** 95.9% within ±1 tier on 362 prompts ($0.36/run); trivial contract 100%;
  96.8% saving vs all-frontier on early dogfood.

**Known gaps carried forward:** tier 3 (frontier) almost never fires; ~7% under-routing
on terse code-debug prompts; Stage-2 answer quality not yet measured; not yet run inside
a real agent harness for a sustained period; install required Node 24+ (fixed by the v0.3 Go port — one static binary).

## v0.2 — calibration, routing logic, and real-harness validation (next)

**Theme: stop trusting the default bands, start trusting measured behaviour in a real agent.**

This version ships in TypeScript, deliberately. The bands that v0.2 derives are the
bands v0.3 ports — a rewrite of *provisional* logic would just have to be re-tuned
after the port. Calibrate first, then carry proven logic across.

### Routing logic
- [ ] **Re-derive `complexityToTier` bands from eval data**, not intuition. Current bands
      make tier 3 unreachable (Jev rarely returns 2.0) and route proof-style tasks
      (score ~0.78) to nano. Ship a second opinion the config can adopt.
- [ ] **Category floors** for the coding case: `code_debug` / `code_implement` minimum
      tier, because a one-line issue title understates the work. This closes the ~7%
      under-routing gap without inflating cost elsewhere.
- [ ] **Two-signal escalation**: use `is_trivial` disagreement (high category complexity
      but trivial noul, or vice versa) as a re-check trigger even when both answers come
      from one call.
- [ ] **Reasoning-effort selection**: actually exercise the per-tier
      `reasoningEffort`/model-variant knob (currently declared, not varied).
- [ ] **Cheap pre-filters before Jev**: obvious greetings/short prompts resolved by a
      local heuristic, reserving the Jev call (and its ~$0.001 + ~400ms) for prompts that
      need classification. Measure how much of real traffic this catches.

### Stickiness
- [ ] **Skip the Jev call on sticky hits.** v0.1 re-classifies every turn for safety;
      once turn-level behaviour is measured, cache the classification per session and
      only re-check on a boundary signal. Removes ~$0.001 and ~400ms per follow-up turn.
- [ ] **Task-boundary downgrade**: ask Jev "did the user start new work?" on a cheap
      cadence, and allow a session to drop back down a tier — the missing half of
      sticky-with-escape.
- [ ] **Escalation policy tuning** from real sessions: how often does escape-up fire, and
      did it actually help? Route cards already record it.

### Harness validation
- [ ] **Run inside OpenCode and OpenChamber for a sustained period** (a week of real
      coding sessions), not just smoke tests.
- [ ] **Instrument what the harness does to the request**: system prompts, tool schemas,
      multi-turn context — these dominate token counts and change what "complexity" means
      versus the clean benchmark prompts the eval uses.
- [ ] **Publish a real-traffic report**: tier mix, escape frequency, actual vs frontier
      spend, and where routing was wrong, from `flux report` on genuine sessions.
- [ ] **Adversarial pass**: prompt-injection attempts against the classifier,
      long-context bloat, and degenerate inputs, with results written down.

### Rewrite groundwork (feeds v0.3)
- [ ] **Golden-decision fixtures**: freeze the 362-prompt eval set *with its recorded
      Jev answers* as offline fixtures, plus `flux trace --json` as the machine-readable
      parity format. The policy engine is pure — same inputs must always yield the same
      tier/model/reason. This costs nothing now and becomes v0.3's acceptance oracle.
- [ ] **Freeze the external contract**: `fluxrouter.config.json` shape, SQLite/JSONL
      route-card schemas, `X-Flux-Route-*` headers, CLI verbs, `/metrics` format —
      documented as the compatibility contract the port must not break.

**Exit criteria for v0.2:** measured savings and mis-route rate on at least one week of
real agent traffic; the routing bands replaced by ones derived from that data; no
known case where a code task silently lands on the smallest model; golden fixtures
recorded and the v0.3 contract written down.

## v0.3 — the Go rewrite: single binary, proven by its own audit trail

**Theme: the best router is the one you can install in one command. The rewrite ships
only after it proves it routes identically to the engine that earned the trust.**

The decision and its evidence live in **[docs/LANGUAGE_ANALYSIS.md](docs/LANGUAGE_ANALYSIS.md)**
(codebase audit + external research: LiteLLM's Python→Rust migration, Preto's Go-vs-Rust
decision, Bifrost, TensorZero, Node-vs-Go SSE data). Summary of the call:

- **Why rewrite at all:** the proxy's own overhead is noise (<2% of a request dominated
  by Jev + a 500–5,000ms LLM), but *distribution* is FluxRouter's #1 adoption blocker —
  the old install was Node 24 + `git clone` + `npm install`; the Go port is
  `go build ./cmd/fluxrouter` → one ~15MB static binary. A rewrite buys
  installability and footprint (~150MB RSS → tens of MB, ~1s start → milliseconds), not
  meaningful latency.
- **Why Go, not Rust:** the product is a *policy* that must iterate weekly with measured
  data; Go keeps change-cost low, streaming trivial (goroutine + `http.Flusher`), and
  its proven ceiling (Bifrost: 5K+ RPS) is ~3 orders of magnitude above FluxRouter's
  needs. Rust's advantages only pay off when users bear the runtime cost on every call —
  FluxRouter has one user, behind a 5-second LLM. Python is rejected outright (GIL;
  LiteLLM is migrating its own hot path out of it for that reason).
- **Why after v0.2:** you port *proven* bands and policy, not provisional ones. The
  golden fixtures from v0.2 are the acceptance oracle.

### What must NOT change (the port's contract)
`fluxrouter.config.json` shape and schema · SQLite route-card schema and JSONL lines
(history must survive; `flux report` keeps working on old cards) · `X-Flux-Route-*`
headers and reason codes · the `flux` alias and OpenAI-compatible endpoints · CLI verbs
(`report`, `trace`, `config validate`, `eval routing|e2e`) · `.env` loading and
precedence · security posture (loopback default, refuse wide bind without
`FLUX_AUTH_TOKEN`). From outside, v0.3 must be invisible except in how it installs.

### Stage G0 — fixtures first (already shipped by v0.2's groundwork) — **SHIPPED**
The 362-prompt golden set with recorded Jev answers, plus trace-JSON as the parity
format. Parity is checked *offline* — the port re-proves itself without spending a
cent or hitting the network.

### Stage G1 — port the pure core (no I/O) — **SHIPPED**
- [x] Port `cost`, `policy`, `session` hashing, and config load/merge/validate to Go packages (`internal/{types,cost,policy,session,config,jev,jsstr}`)
- [x] **Golden parity harness**: the Go engine reproduces 100% of the TS engine's decisions (tier, model, reason code) across the fixture set — one divergence is a bug, not a tuning opinion. **Measured: 1272/1272 fixtures match (100.0%)** (`old/scripts/gen-fixtures.mjs` → `fixtures/golden.jsonl` → `fluxrouter parity`, mutation-tested exit 1). Plus recorded cross-engine vectors: session-ids incl. a unicode system prompt (proves UTF-16 length parity), estimateTokens, buildJevState byte-identical truncation, and verbatim config validation messages. Jev-unavailable fallback ladder and sticky-escape vectors also match.

### Stage G2 — port the server and streaming — **SHIPPED**
- [x] `net/http` server (stdlib), SSE forwarding with `http.Flusher`, upstream pooling mirroring undici (32 conns / 30s idle), the timeout/failover ladder (4xx-except-429/408 break, 502-on-network-error, verbatim error propagation, exhausted-message-only-when-no-lane-attempted semantics).
- [x] Jev client with identical semantics (700ms timeout; 408/504 and abort → timeout error; anything else → unavailable; both → jev_*_fallback, matching TS error classes).
- [x] Route cards via pure-Go SQLite (`modernc.org/sqlite`, CGo-free), writing the *same* schema — **cross-read proven: a DB written by the TS engine's `node:sqlite` opens, aggregates, and totals correctly in the Go engine; TS JSONL cards parse and re-marshal equivalently** (`internal/cardlog/testdata/ts-written.*`, generated by `old/scripts/gen-testdb.mjs`).
- [x] **Live E2E harness green** (`internal/e2e`): stub Jev + stub Ollama drive the real binary — boot checks/exit codes match TS, `hello → tier 0/policy/nano`, `HARD → tier 2`, `X-Flux-Route-*` headers, metrics counters, route cards per request. Router/server unit tests additionally pin ParseChatRequest JSON.stringify semantics (item 9, incl. source-key-order preservation for multimodal content), config verbatim messages, CostFromUsage cached-rate math, and failover error propagation.
- [ ] Re-run the full TESTING.md acceptance walkthrough and Postman collection against the Go binary (needs real API keys; harness and binary are ready).

### Stage G3 — shadow dual-run (the unique part)
- [ ] Run the Go binary on a second port in **mirror mode**: every live request is
      decided by *both* engines; the Go side writes route cards flagged as shadow, and
      a `flux parity` diff reports every divergence.
- [ ] One week of real traffic. This is something only FluxRouter can do — its route
      cards mean the port is verified against the exact decisions the incumbent made on
      the same traffic, not against synthetic tests.
- [ ] **Gate:** ≥99.9% decision parity on live traffic, zero status-code regressions,
      before any cutover.

### Stage G4 — cutover and distribution
- [ ] The Go binary becomes the release artifact; cross-compile Windows/macOS/Linux
      (amd64+arm64) from one codebase, published as GitHub release assets.
- [ ] The npm package becomes a thin installer shim that downloads the platform binary —
      existing npm users see no change.
- [ ] Measure and publish the rewrite's own scorecard: RSS <50MB under load, cold start
      <100ms, binary <20MB — against the TS baseline.
- [ ] Freeze the TypeScript tree (security fixes only); archive after one stable cycle.
      The port was proven — the old engine is no longer needed as a safety net.

**Exit criteria for v0.3:** 100% golden-fixture parity; ≥99.9% live shadow parity over
a week of real traffic; release binaries for all platforms with zero runtime
dependencies; route-card history readable across the port; the full TESTING.md
walkthrough passing on the binary alone; the measured scorecard published.

## v0.4 — context economics (planned)

**Theme: the request body is the budget. Routing decides the price per token; the next
lever is how many tokens you send at all.** (Formerly v0.3; unchanged in substance, and
easier in Go — the always-on binary makes off-path compaction workers cheap.)

- [ ] **Context compaction.** Summarise turns older than a session threshold with a
      cheap model before forwarding, targeting a large reduction on long sessions.
      Compaction runs off the critical path, never blocks a request, and its own cost is
      attributed per session in the route card.
- [ ] **Context trimming.** Drop stale tool outputs and oversized single messages above
      configurable limits — often the largest single win in agent traffic, where tool
      results accumulate and are re-sent every turn.
- [ ] **Skill / tool-schema reduction based on the prompt.** Inject only the skill and
      tool definitions the current task plausibly needs, instead of the full catalogue.
      A coding turn does not need the writing skills; a question does not need 20 tool
      schemas. Jev's `category` answer is already the signal to drive this.
- [ ] **Prompt-cache-aware routing.** Prefer upstreams/models with cache-hit pricing for
      repeated prefixes (Ollama publishes cached-input rates), and avoid re-ordering
      messages in ways that break prefix caching.
- [ ] **Compaction quality gate.** A cheap check that the compacted context did not lose
      a decision, constraint, or unresolved question, so compaction cannot silently
      degrade answers.
- [ ] **Tier-0 summariser economics.** Ensure compaction itself is routed cheaply and
      never becomes the cost it is meant to remove.

**Exit criteria for v0.4:** measured token reduction on long sessions with no measurable
answer-quality regression, and a documented break-even point (when compaction costs more
than it saves).

## v1.0 — beyond routing

Candidate, in rough order of expected value (v0.3's single binary makes the last two
far easier):

- **Semantic caching** — repeated or near-identical prompts served from cache.
- **Struggle-signal escalation** — detect self-correction/retry in a session and escalate
  proactively rather than waiting for the next turn's classification.
- **Per-key / per-project budgets** with hard stops, not just per-request caps.
- **Dashboard** — the SQLite route cards already hold everything a UI needs.
- **Multi-provider lanes** — add direct provider keys alongside Ollama Cloud/OpenRouter.
- **Calibration drift detection** — periodic shadow eval against a held-out set, alerting
  when Jev's behaviour shifts under a new pinned version.
- **Team mode** — shared config, shared budgets, but not before single-user is solid.

## Non-goals

- Becoming a general gateway/observability platform (LiteLLM, Portkey territory).
- Model aggregation for its own sake — FluxRouter routes; it does not try to be every
  model's front door.
- Training our own classifier. Jev is the classifier; the product is the routing policy
  and its audit trail.
- Rewriting again for performance's sake. The router's overhead is already noise behind
  the LLM; v0.3 is the last planned language move. (Rust re-enters the conversation only
  if the routing core becomes an embeddable library — see
  [docs/LANGUAGE_ANALYSIS.md](docs/LANGUAGE_ANALYSIS.md).)