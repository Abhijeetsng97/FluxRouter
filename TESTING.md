# TESTING.md — FluxRouter hands-on testing guide

_A personal walkthrough for testing FluxRouter from scratch. Follow top-to-bottom on a
fresh machine; every step tells you what you should see. Estimated time: ~30 min._
_Last verified against: v0.1.0, Node 24, Windows._

---

## 0. Prerequisites check

| Need | Check | Where to get it if missing |
|---|---|---|
| Node 24+ | `node --version` → v24.x | nodejs.org |
| Ollama account + key | `$env:OLLAMA_API_KEY` set | ollama.com → Settings → API keys |
| TypeSafe (Jev) key | `$env:TYPESAFE_API_KEY` set | docs.typesafe.ai |
| OpenRouter key (optional) | `$env:OPENROUTER_API_KEY` set | openrouter.ai → Keys |
| OpenChamber (for the final AC, optional at first) | running locally | openchamber docs |

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
Both the server and `flux eval` read these files automatically.

> Cost note: unit tests spend $0. Stage-1 eval ≈ $0.36. Stage-2 eval ≤ $10 (guarded).
> A full acceptance run ~$2–3 drawn from your Ollama credits + pennies of Jev.

---

## 1. Install & static checks (spends $0)

```powershell
cd C:\code\FluxRouter
npm install
```

Check 1 — dependencies install clean (8 packages, 0 vulnerabilities).

```powershell
npm test
```

Check 2 — **41/41 unit tests pass.** These cover the whole policy engine:
trivial bypass, complexity bands, low-confidence escalation, context gate,
cost-guard downgrade, sticky pin/reuse/upward-escape, config validation, Jev response
parsing, env loading.

```powershell
npm run typecheck
```

Check 3 — TypeScript is clean (`tsc --noEmit`, empty output = good).

```powershell
node cli/flux.ts config validate
```

Check 4 — prints ✓ valid + the 4 tiers with rates:

```
✓ fluxrouter.config.json valid
  jev: jev-1.13.0 @ https://api.typesafe.ai/v1/systemone (timeout 700ms)
  tier 0 nano     → ollama:nemotron-3-nano:30b ($0.06/$0.24 per M in/out, ctx 1000000)
  tier 1 flash    → ollama:glm-5.3-flash   ($0.15/$0.5 …)
  tier 2 mid      → ollama:deepseek-v4-pro:0813 ($0.66/$1.98 …)
  tier 3 frontier → ollama:kimi-k3        ($3.00/$15.00 …)
  cost cap/request: $0.25
  eval caps: routing $1, e2e $10
```

Try to break it: edit `fluxrouter.config.json`, set `"perRequestCapUsd": -1`, re-run
validate → expect exit code 2 and a readable error. Then undo it.

---

## 2. Start the server (spends $0)

```powershell
npm start
```

Expect:

```
FluxRouter v0.1.0 listening on http://127.0.0.1:8787
  OpenAI base URL: http://127.0.0.1:8787/v1
  model alias: flux (routing by Jev classification)
  route cards: .fluxrouter/route-cards.{jsonl,db}
```

(If you see `EADDRINUSE`, another instance is already on 8787 — stop it or use
`npm start -- --port 8788`.)

Leave it running. New terminal for the rest. Verify the surface:

```powershell
curl.exe http://127.0.0.1:8787/health
curl.exe http://127.0.0.1:8787/v1/models
curl.exe http://127.0.0.1:8788/metrics   # after a few requests
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

Or without spending anything:

```powershell
node cli/flux.ts trace "hello"
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
node cli/flux.ts report --by tier
node cli/flux.ts report --by category --today
```

**Check 9** — tier 0 shows your "hello"; math shows under `math`; a `cost $` column
with actual (tiny) numbers; totals reconcile with:

```powershell
(Get-Content .fluxrouter\route-cards.jsonl | Measure-Object -Line).Lines
```

(line count == sum of requests in report). Open one JSONL line and eyeball:
`reason` matches the header you saw, `costUsd` is ~1e-5-ish, `latenciesMs.jev` < 700.

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

Cost-guard check without spending: temporarily set tier-3 rates to 75/75 in config and
send a 1MB-content request with a tool_planning category — route card must show
`reason: cost_guard` and a lower tier. (Covered by unit test "cost guard downgrades…" —
run `npm test` if you don't want to hand-configure.)

---

## 7. Eval harness (Check 13 — the AC6 budget contracts)

Dry-run first (costs nothing):

```powershell
node cli/flux.ts eval routing --dry-run
```

Expect:

```
datasets loaded: trivial, explain_hand, gsm8k, math500, swebench_lite
routing eval: 362 prompts × $0.001 = $0.36 (cap $1)
dry-run: stopping before any calls.
```

Datasets download once and cache under `cli/eval/datasets-files/` (gitignored), so later
runs are offline and free. If a dataset source moves, the run warns and continues with
the rest instead of crashing.

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
expect `✗ budget guard… Nothing was spent.` and exit 2. Restore to 1.

---

## 8. End-to-end quality + cost (spends ≤ $10; check spend first!)

```powershell
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
   - a greeting → check `flux report --today` shows tier 0
   - "Write a debounce function in TypeScript with leading-edge option." → tier ≥ 1, streamed
   - a hard math/probability question → tier 2, correct answer
4. **Check 15** — all three turns completed in OpenChamber UI; route cards exist for
   each with sensible reasons; no body mangling (markdown renders; code blocks intact —
   this proves stream passthrough).

---

## 10. Final acceptance sweep

| # | Criterion | Where verified |
|---|---|---|
| AC1 | OpenChamber chat works end-to-end (greeting/math/streamed code) | §9 |
| AC2 | "hello" → tier 0, ~$0.000x | §3 |
| AC3 | Hard math → tier ≥ 1–2, complexity recorded | §3 |
| AC4 | forced 300k-token request → cost_guard + card | §6 (unit-test path) |
| AC5 | Jev timeout → completes at fallback tier | §6 |
| AC6 | routing eval ≤ $1; e2e ≤ $10; guard enforced | §7 |
| AC7 | `flux report` reconciles exactly with JSONL | §4 |

All green → v0.1 acceptance done.

---

## Troubleshooting

| Symptom | Fix |
|---|---|
| `Missing API keys in environment` | set them in shell or `.env` (see §0) |
| `Refusing to bind non-loopback host` | keep `host: 127.0.0.1` or set `FLUX_AUTH_TOKEN` |
| Jev 401 | wrong/expired `TYPESAFE_API_KEY` |
| Ollama 402/403 | credits exhausted on your plan; check ollama.com → usage |
| OpenRouter 429 on eval | `:free` daily cap; use paid variants for e2e |
| `config errors` at boot | run `node cli/flux.ts config validate`, fix listed fields |
| Every request tier 1 | Jev failing or scoring mid — look for `jev_unavailable_fallback` in cards; check key |
| Hard math lands on tier 1 | Expected until calibrated — Jev scores most word problems ~0.9–1.0. Tune `complexityToTier` bands using `flux eval routing` results |
| Trivial contract failing | check `jev.trivialNoul` (0.85) and `escalateOnlyAboveComplexity` (0.5) are set |
| PowerShell curl errors | use `curl.exe`, not the `Invoke-WebRequest` alias |
| `EADDRINUSE :8787` | another instance is running; stop it or `--port 8788` |