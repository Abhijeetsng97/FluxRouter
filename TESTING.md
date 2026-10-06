# TESTING.md — FluxRouter (Go engine) hands-on testing guide

_A personal walkthrough for testing the Go FluxRouter from scratch. Follow top-to-bottom
on a fresh machine; every step tells you what you should see. Estimated time: ~25 min._
_Last verified against: v0.1.0-go (gorewrite branch), Go 1.27.1, Windows._

> The Go engine is the v0.3 port of the original TypeScript engine. Its routing decisions
> are proven identical to the TS engine by the golden-fixture parity gate (§1); the frozen
> TS reference lives in `old/` and is only used to regenerate fixtures. Everything in this
> guide runs the **Go binary**.

---

## 0. Prerequisites check

| Need | Check | Where to get it if missing |
|---|---|---|
| Go 1.27+ | `go version` → go1.27.x | go.dev/dl |
| Ollama account + key | `$env:OLLAMA_API_KEY` set | ollama.com → Settings → API keys |
| TypeSafe (Jev) key | `$env:TYPESAFE_API_KEY` set | docs.typesafe.ai |
| OpenRouter key (optional) | `$env:OPENROUTER_API_KEY` set | openrouter.ai → Keys |
| OpenChamber (for the final AC, optional at first) | running locally | openchamber docs |

No Node.js is required to run or test the router. (Node is only needed if you want to
*regenerate* the golden fixtures yourself — see §1.)

Set keys for this session — either shell exports:

```powershell
$env:OLLAMA_API_KEY="sk-…"
$env:TYPESAFE_API_KEY="ts-…"
$env:OPENROUTER_API_KEY="sk-or-…"   # optional; failover lane only
```

…or a `.env` file (simpler, survives new terminals):

```powershell
Copy-Item .env.example .env
notepad .env     # fill in the three values
```

Precedence: real environment variables win over `.env.local`, which wins over `.env`.
The server loads these automatically at boot.

> Cost note: everything in §1 spends $0 (offline). A live routing smoke (§3–§6) costs
> pennies of Jev (~$0.001/decision) + nano-tier generation. A full acceptance run is
> ~$1–2 drawn from your Ollama credits.

---

## 1. Static checks & the parity gate (spends $0)

Build the binary:

```powershell
cd C:\code\FluxRouter
go build -o fluxrouter.exe ./cmd/fluxrouter
```

**Check 1** — clean build, no output. Then:

```powershell
go test ./... -count=1
```

**Check 2 — all tests pass** (`internal/...` + `test/e2e`). The suite covers:
the full routing policy (trivial bypass, complexity bands, low-confidence escalation,
context gate, cost-guard downgrade, sticky pin/reuse/upward-escape), config validation,
session-id hashing, Jev state building and response parsing, JS UTF-16 string semantics,
SQLite route-card round-trip, HTTP contract (auth, error shapes, `/v1/models`,
`/metrics` formats, SSE pass-through), and a binary-level e2e that boots the real
server against stub upstreams. Use `go test ./... -short` to skip the e2e boot.

### The parity gate — the port's proof

```powershell
./fluxrouter.exe parity fixtures/golden.jsonl
```

**Check 3 — `parity: 1272/1272 fixtures match (100.0%)`, exit code 0.**

This is the acceptance oracle for the Go rewrite: 1,272 routing decisions recorded
from the live TS engine (a sweep of complexity × category × triviality × confidence,
plus sticky pins, Jev-fallback tiers, and context-gate shrinks). The Go engine must
reproduce every decision — same tier, same reason code, cost within 1e-9. Exit 1 with
a per-decision diff means a routing behavior changed; **one divergence is a bug, not a
tuning opinion** — unless you are *deliberately* retuning bands, in which case you
regenerate the fixtures in the same commit (see below) so reviewers see exactly which
decisions moved.

To regenerate (intentional policy changes only; needs Node):

```powershell
node old/scripts/gen-fixtures.mjs   # re-records from the TS reference engine
./fluxrouter.exe parity fixtures/golden.jsonl   # must be 100% again before commit
```

Then config sanity:

```powershell
./fluxrouter.exe config validate
```

**Check 4 — prints `Config OK. Tiers:` + the 4 tiers with rates:**

```
Config OK. Tiers:
  tier 0 nano: nemotron-3-nano:30b ($0.06/$0.24 per M, ctx 1000000)
  tier 1 flash: glm-5.3-flash ($0.15/$0.5 per M, ctx 1000000)
  tier 2 mid: deepseek-v4-pro:0813 ($0.66/$1.98 per M, ctx 1000000)
  tier 3 frontier: kimi-k3 ($3/$15 per M, ctx 1000000)
```

Try to break it: edit `fluxrouter.config.json`, set `"perRequestCapUsd": -1`, re-run
validate → expect exit code 2 and a readable error. Then undo it.

---

## 2. Start the server (spends $0)

```powershell
./fluxrouter.exe serve
```

Expect:

```
FluxRouter v0.1.0 listening on http://127.0.0.1:8787
  OpenAI base URL: http://127.0.0.1:8787/v1
  model alias: flux (routing by Jev classification)
  route cards: .fluxrouter/route-cards.{jsonl,db}
```

(If the port is taken, another instance is already on 8787 — stop it or use
`./fluxrouter.exe serve --port 8788`.)

Leave it running. New terminal for the rest. Verify the surface:

```powershell
curl.exe http://127.0.0.1:8787/health
curl.exe http://127.0.0.1:8787/v1/models
curl.exe http://127.0.0.1:8787/metrics   # after a few requests
```

**Check 5** — health `{"ok":true}`, models lists `flux`, metrics exposes counters.

---

## 3. First live routing (spends ~$0.001 Jev + pennies of nano)

Use `curl.exe` in PowerShell (plain `curl` is an `Invoke-WebRequest` alias and its flags
differ), or the Postman collection in `postman/`.

The moment of truth — **"hello" must route to tier 0**:

```powershell
curl.exe http://127.0.0.1:8787/v1/chat/completions -H "Content-Type: application/json" -d '{\"model\":\"flux\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}'
```

**Check 6 — the founding guarantee.** In the response headers:

```
X-Flux-Route-Tier: 0
X-Flux-Route-Model: nemotron-3-nano:30b
X-Flux-Route-Reason: trivial_bypass
X-Flux-Session: <hash>
```

Or drive the exact same policy engine offline, spending nothing:

```powershell
./fluxrouter.exe trace "hello" --offline --category greeting_chitchat --complexity 0.2 --noul 0.9
```

Hard math must go UP a tier (change content):

```json
{ "role": "user", "content": "What is the probability of getting at least two heads in five fair coin tosses? Show the calculation." }
```

**Check 7** — `X-Flux-Route-Tier` is `1` with reason `policy` (Jev scores this around
0.9: standard, not hard). A **textbook** problem is *meant* to land on flash — Jev's
calibration is the thing being tested here, and §7's eval is what refines it.

Now a genuinely hard one:

```json
{ "role": "user", "content": "Prove or disprove: for every n ≥ 3, there are no positive integers a, b, c with a^n + b^n = c^n. Discuss why elementary attempts via factoring fail." }
```

**Check 8** — record the tier *and* the `complexity` value from the route card. Note
Jev's real behaviour before judging it: even a cubic-proof request scored ~1.0 in
testing, so tier `1`–`2` is a legitimate outcome. What matters is that the number Jev
returns is **consistent and defensible** — that is exactly what the Stage-1 eval
measures and what your `complexityToTier` bands should be tuned against.

---

## 4. Route cards & report (spends $0)

```powershell
./fluxrouter.exe report --by tier
./fluxrouter.exe report --by category
```

**Check 9** — tier 0 shows your "hello"; math shows under `math`; a `cost_usd` column
with actual (tiny) numbers; totals reconcile with:

```powershell
(Get-Content .fluxrouter\route-cards.jsonl | Measure-Object -Line).Lines
```

(line count == sum of requests in report). Open one JSONL line and eyeball:
`reason` matches the header you saw, `costUsd` is ~1e-5-ish, `latenciesMs.jev` < 700.
The report also prints the **all-frontier counterfactual** — what the same traffic
would have cost at tier-3 rates — e.g. `21 requests, $0.0069 actual vs $0.2193
all-frontier (96.8% cheaper)`.

The SQLite store (`.fluxrouter/route-cards.db`) is schema-identical to the one the TS
engine wrote, so cards written by the old engine remain queryable, and vice versa
(covered by `internal/telemetry` cross-read tests; verified live in §1's test run).

---

## 5. Sticky sessions & escape (spends ~$0.002)

Repeat-turn check — run the "hello" request again. The session id (hash of system +
first user message) is stable, so:

**Check 10** — the same conversation shows reason `sticky` on later turns.

Escape check — same first message, harder follow-up:

```json
{
  "model": "flux",
  "messages": [
    { "role": "user", "content": "hello" },
    { "role": "assistant", "content": "Hi! What can I do for you?" },
    { "role": "user", "content": "Now solve: What is the probability of getting at least two heads in five fair coin tosses? Show work." }
  ]
}
```

**Check 11** — reason `sticky_escape_up`, tier jumps up. (`X-Flux-Session` unchanged.)

---

## 6. Resilience: Jev timeout & failover (spends $0)

Kill network to Jev only (or set a bogus `jev.baseUrl` in config) and send "hello":

**Check 12** — request still succeeds; reason `jev_timeout_fallback` or
`jev_unavailable_fallback`; response completes — no user-visible error. Restore config.

Cost-guard check without spending:

```powershell
./fluxrouter.exe trace "big request" --offline --category tool_planning --complexity 0.3 --noul 0.1
```

then temporarily raise tier-3 rates in config and re-check — but the no-config path is
the unit-test version: "cost guard downgrades when projected cost exceeds cap" is
covered by a recorded golden vector in `go test ./internal/routing/`. (Route cards must
show `reason: cost_guard` and a lower tier when it fires live.)

---

## 7. Eval harness (Check 13 — the AC6 budget contracts)

> **Status: TS-only for now.** The eval runners (`eval routing` / `eval e2e`) live in the
> frozen TS tree and are ported in a later v0.3 stage (see ROADMAP). Until then run them
> from `old/` with Node — they exercise the same Jev API, the same config file, and the
> same route-card format; the numbers they produce remain valid for the Go engine's
> band tuning because routing policy is parity-locked (§1).

Dry-run first (costs nothing):

```powershell
cd old
npm install
node cli/flux.ts eval routing --dry-run
```

Expect:

```
datasets loaded: trivial, explain_hand, gsm8k, math500, swebench_lite
routing eval: 362 prompts × $0.001 = $0.36 (cap $1)
dry-run: stopping before any calls.
```

Datasets download once and cache under `old/cli/eval/datasets-files/` (gitignored), so
later runs are offline and free. If a dataset source moves, the run warns and continues
with the rest instead of crashing.

Real run (≈ $0.36, cap $1):

```powershell
node cli/flux.ts eval routing
```

**Check 13** — the money table (numbers from the reference run):

```
=== routing eval results ===
source           n     ±1 tier   exact    timeouts
explain_hand     10    100.0%    100.0%   0
gsm8k            100   100.0%    100.0%   1
math500          100   96.0%     58.0%    0
swebench_lite    100   89.0%     46.0%    1
trivial          52    100.0%    100.0%   0
TOTAL            362   95.9%     73.5%    2

Estimated spend: $0.36
```

The run also prints tier distribution, a Jev complexity histogram, and the worst
misroutes with prompt text (see [docs/EVALS.md](docs/EVALS.md)).

Contract gates: trivial row **exact = 100%**, total ±1 tier ≥ 85%. Targeted re-checks:
`--only trivial` ($0.05), `--only math500,swebench_lite` ($0.20).

Budget guard proof: temporarily set `"routingCapUsd": 0.01` in config, re-run →
expect the guard refusal message and exit 2. Restore to 1.

---

## 8. End-to-end quality + cost (spends ≤ $10; check spend first!)

> **TS-only for now** (same note as §7).

```powershell
cd old
node cli/flux.ts eval e2e --dry-run
```

Review the per-tier plan and worst-case projection **before** running for real.
Real run (≈ $5.40 typical):

```powershell
node cli/flux.ts eval e2e
```

**Check 14** — output includes per-tier pass %, judge-disagreement rate (< 20% = ok),
and `Actual spend: $…`. The **quality-retention number** is the one that validates the
tier→model mapping: target ≥ 90% of the all-frontier baseline.

---

## 9. OpenChamber acceptance (final AC) (spends ~$0.10)

1. OpenChamber → Settings → Providers → add custom OpenAI-compatible provider:
   - Base URL `http://127.0.0.1:8787/v1`, key: anything, model: `flux`
2. Make it the active model in a chat.
3. Run three turns:
   - a greeting → check `./fluxrouter.exe report --by tier` shows tier 0
   - "Write a debounce function in TypeScript with leading-edge option." → tier ≥ 1, streamed
   - a hard math/probability question → tier 2, correct answer
4. **Check 15** — all three turns completed in OpenChamber UI; route cards exist for
   each with sensible reasons; no body mangling (markdown renders; code blocks intact —
   this proves stream passthrough).

---

## 10. Final acceptance sweep

| # | Criterion | Where verified |
|---|---|---|
| AC0 | Go engine routes identically to the TS engine | §1 parity gate |
| AC1 | OpenChamber chat works end-to-end (greeting/math/streamed code) | §9 |
| AC2 | "hello" → tier 0, ~$0.000x | §3 |
| AC3 | Hard math → tier ≥ 1–2, complexity recorded | §3 |
| AC4 | forced 300k-token request → cost_guard + card | §6 (unit-test path) |
| AC5 | Jev timeout → completes at fallback tier | §6 |
| AC6 | routing eval ≤ $1; e2e ≤ $10; guard enforced | §7 |
| AC7 | `flux report` reconciles exactly with JSONL | §4 |

All green → Go-engine acceptance done.

---

## Troubleshooting

| Symptom | Fix |
|---|---|
| `Missing API keys in environment` | set them in shell or `.env` (see §0) |
| `Refusing to bind non-loopback host` | keep `host: 127.0.0.1` or set `FLUX_AUTH_TOKEN` |
| Jev 401 | wrong/expired `TYPESAFE_API_KEY` |
| Ollama 402/403 | credits exhausted on your plan; check ollama.com → usage |
| OpenRouter 429 on eval | `:free` daily cap; use paid variants for e2e |
| `FluxRouter config errors` at boot | run `./fluxrouter.exe config validate`, fix listed fields |
| Parity divergences after a policy edit | intentional? regenerate fixtures (`node old/scripts/gen-fixtures.mjs`) **and commit them together** with the band change; accidental? revert and investigate |
| Every request tier 1 | Jev failing or scoring mid — look for `jev_unavailable_fallback` in cards; check key |
| Hard math lands on tier 1 | Expected until calibrated — Jev scores most word problems ~0.9–1.0. Tune `complexityToTier` bands using eval results (§7) |
| Trivial contract failing | check `jev.trivialNoul` (0.85) and `escalateOnlyAboveComplexity` (0.5) are set |
| PowerShell curl errors | use `curl.exe`, not the `Invoke-WebRequest` alias |
| Port 8787 already in use | another instance is running; stop it or `--port 8788` |
| `go test` fails on `test/e2e` alone | e2e needs to build the binary; run from repo root (`go test ./...`), or skip with `-short` |