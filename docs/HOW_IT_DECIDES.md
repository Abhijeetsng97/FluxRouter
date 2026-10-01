# How FluxRouter decides

_What happens when a request arrives, how Jev classifies it, and how the model is chosen.
Use `flux trace "<your prompt>"` to see the same reasoning for your own input._

---

## The 30-second version

```
your prompt
  → Jev answers 3 questions in ONE call (~$0.001, ~400ms)
      category   (choice of 10)   e.g. "math"
      complexity (score 0..2)     e.g. 1.3
      is_trivial (noul 0..1)      e.g. 0.02
  → context gate   (drop models too small for the request)
  → policy table   (category × complexity → tier, plus overrides)
  → confidence check / cost guard / sticky
  → forward to that tier's model on Ollama Cloud (OpenRouter as failover)
  → write a route card (what happened, why, exact cost)
```

---

## What happens when a call arrives

| # | Step | What it does |
|---|---|---|
| 1 | **Auth** | Skipped unless `FLUX_AUTH_TOKEN` is set |
| 2 | **Parse** | Normalise `messages[]`, `stream`, requested model |
| 3 | **Estimate tokens** | `chars / 4` per message. Free, instant |
| 4 | **Classify with Jev** | **One** HTTP call — details below. ~$0.001, ~400ms |
| 5 | **Decide the tier** | Context gate → policy → confidence → cost guard |
| 6 | **Forward** | To the tier's model; failover ladder if it errors |
| 7 | **Log a route card** | JSONL + SQLite: what, why, usage, cost, latency |
| 8 | **Respond** | Stream passed through untouched; `X-Flux-Route-*` headers added |

**Sticky sessions:** the first request in a conversation pins a tier. Later turns reuse
it, and upgrade only if the new turn is clearly harder — so models don't flap mid-task.

**If Jev fails or times out** (700ms budget), the request still completes: it goes to the
fail-safe tier (`jev.fallbackTier`, default flash) and the route card records why. A slow
or dead classifier degrades the *decision*, never your response.

---

## What Jev is and what it is asked

**Jev** is TypeSafe's "System One" model — a classifier, not a chat model. It never writes
prose; it returns **typed answers with calibrated probabilities**.

FluxRouter asks three questions in a **single** call (extra questions in one call cost no
extra latency, and output tokens are free):

| Question | Type | Returns | Drives |
|---|---|---|---|
| `category` | choice (10 options) | winning option + probabilities + confidence | which overrides apply |
| `complexity` | score (3 levels, 0–2) | probability-weighted score + confidence | the tier band |
| `is_trivial` | noul (calibrated yes/no) | a probability 0–1 | the tier-0 bypass |

**What Jev sees:** a short excerpt of your conversation — the last message up to 2 000
chars (head and tail), earlier turns trimmed to ~500 chars each. It never receives whole
files or images.

---

## How the model is chosen

Jev's answers map to a **tier** (0–3), not directly to a model. Each tier names one model
in `fluxrouter.config.json`, so models can be swapped without touching routing logic.

```
is_trivial ─→ (if > 0.85) ──────→ tier 0 directly
category ──┐
           ├─→ override table ──┐
complexity─┘                    ├─→ tier (0..3) ─→ model
           └─→ complexity band ─┘
```

| Tier | Default model | Roughly when |
|---|---|---|
| 0 nano | `nemotron-3-nano:30b` | trivial, or complexity < 0.8 |
| 1 flash | `glm-5.3-flash` | complexity < 1.6 |
| 2 mid | `deepseek-v4-pro:0813` | complexity < 2.0, a `math`/`code` override, or low confidence |
| 3 frontier | `kimi-k3` | top of Jev's scale, or an explicit override |

**Precedence, highest first:** sticky pin → trivial bypass → category override →
complexity band → context gate → low-confidence escalation → cost guard.
Whichever fires is recorded as the request's `reason`.

**The cost guard** deserves a note: before forwarding, FluxRouter projects the cost
(tokens × rates). If it exceeds `cost.perRequestCapUsd` ($0.25 default), the request is
downgraded to the nearest cheaper tier that fits — so a huge context can never silently
hit an expensive long-context model.

---

## Examples

Real prompts, real decisions (`flux trace` prints this for anything you type):

| Prompt | Jev says | Routes to |
|---|---|---|
| `hello` | greeting, complexity 0.00, trivial 0.98 | **tier 0** — the trivial gate wins; a greeting can never reach a paid model (~$0.000008) |
| "Janet's ducks lay 16 eggs a day…" | math, complexity 0.4 | **tier 0** — grade-school arithmetic is genuinely easy |
| "Probability of ≥2 heads in five coin tosses?" | math, complexity 0.91 | **tier 1** — a standard textbook calculation |
| MATH-500 level-5 problem | math, complexity ~1.8 | **tier 2** — competition math reaches mid |
| Short bug report ("trailing newline in usernames") | code_debug, complexity ~0.25 | **tier 0** ⚠️ — see "Current limits" below |

---

## Tuning your own ladder

All routing behaviour lives in `fluxrouter.config.json` — changing it never requires a
code change:

- **Bands**: `"complexityToTier": [[0.8,0],[1.6,1],[2.0,2],[999,3]]` — the score cut-offs
- **Category floors**: add `{"category": "code_debug", "tier": 2}` so bug fixes never hit
  the smallest model
- **Trivial gate**: `jev.trivialNoul` (default 0.85) — how confident Jev must be that a
  prompt is filler
- **Cost cap**: `cost.perRequestCapUsd` (default $0.25)

After any change, re-measure cheaply:

```sh
flux config validate
flux trace "your prompt"                          # one decision, ~$0.001
flux eval routing --only trivial,math500          # targeted re-run, pennies
```

`flux trace` and `flux eval` call the **same** policy function as the live server — what
you see is what the proxy does.

---

## Current limits (honest)

- **Jev scores conservatively.** A serious proof prompt scored 0.78 — right at the 0.8
  band, so it landed on nano. Hard tasks cluster 1.0–1.8 on its 0–2 scale, so tier 3
  rarely fires and the mid tier carries hard work. The bands are provisional by design;
  tune them from `flux eval` output.
- **Terse bug reports can under-route.** A one-line issue title can hide real engineering
  work (the "trailing newline" example above). If your traffic is mostly coding, add the
  `code_debug` category floor.
- **Routing accuracy ≠ answer quality.** These examples show what Jev *judged*, not
  whether the chosen model answered well. Measuring answers is Stage 2
  (`flux eval e2e`, ≤ $10/run).

For the raw eval numbers and how to read them, see [EVALS.md](EVALS.md).