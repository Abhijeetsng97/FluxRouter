# Eval results and how to read them

_Measured routing-eval output, what each number means, and what the numbers taught us.
This is the **observed results** companion to README → "Eval methodology" and
"Measured results" (the consolidated reference run)._

All numbers below are from real runs on Ollama Cloud + Jev `jev-1.13.0`, pasted verbatim.
No run in this document was edited or re-simulated.

---

## What this eval measures — and what it does not

**Stage 1 (`flux eval routing`) measures one thing:** does Jev's difficulty judgement
match the dataset's labels? It calls the classifier and the policy engine and **never
generates an answer**. That is why a full run costs ~$0.36 instead of dollars.

It does **not** measure whether the routed model actually *solves* the prompt. Whether
`deepseek-v4-pro` correctly answers a level-5 MATH problem is Stage 2 (`flux eval e2e`),
which has not been run. Treat the tier→model mapping as **designed, not validated**.

---

## Running it

```sh
node cli/flux.ts eval routing --dry-run                  # plan + projected cost, no spend
node cli/flux.ts eval routing                            # full run  (~$0.36, cap $1)
node cli/flux.ts eval routing --only math500,swebench_lite   # targeted re-run (~$0.20)
node cli/flux.ts eval routing --only trivial             # contract check only (~$0.05)
```

- Cost is ~$0.001 per prompt (one Jev decision); the harness estimates ~$0.001/prompt.
- A **pre-flight budget guard refuses to start** if the projection exceeds the configured
  cap (`eval.routingCapUsd`, default $1) — the run aborts before spending anything.
- Datasets download once and cache under `cli/eval/datasets-files/`; later runs are
  offline. A dataset that fails to load warns and skips rather than aborting the run.
- Each run writes a per-prompt audit trail to `.fluxrouter/eval-routing-<timestamp>.jsonl`.

---

## Reading the output

A run prints five things. They answer different questions:

| Section | Question it answers |
|---|---|
| **Results table** | how often the router agreed with the dataset label |
| **Tier distribution** | where each kind of prompt actually went (t0–t3 share) |
| **Complexity histogram** | how Jev scored each set — the data for choosing band cut-offs |
| **Worst misroutes** | which prompts disagreed most, with Jev's own score/confidence |
| **Contract line** | whether greetings stayed on tier 0 |

### The results table columns

| Column | Meaning | Watch for |
|---|---|---|
| `n` | prompts in that set | — |
| `±1 tier` | share routed within one tier of the label | **the primary metric** — ≥85% is the bar |
| `exact` | share routed to an allowed tier exactly | partly an artifact of the labels (see below) |
| `timeouts` | Jev calls that hit the 700 ms budget | should be ~0; each one falls back safely |

**`exact` is the least trustworthy column** because it inherits the eval's own labels.
MATH-500 labels come from the dataset's published difficulty levels; SWE-bench has no
such field, so its `[2,3]` expectation is a blanket guess. When `exact` is low, check the
misroute list before blaming the router.

### The contract line

`trivial bypass (tier-0 exact): X%` — **must be 100%** (or ≥99%). If it is not, a
greeting reached a paid tier, which violates the core guarantee.

---

## Results log

Three runs, in order, across a period where the eval's labels and one policy bug were
being fixed. The change between them is itself the finding, so the code state is recorded.

| Run | Code / label state | n | ±1 tier | exact | timeouts | Contract |
|---|---|---|---|---|---|---|
| **A** | original policy; original labels | 362 | **90.3%** | 58.8% | 0 | 98.1% ✗ |
| **B** | after low-confidence fix; labels unchanged | 362 | 88.7% | 53.9% | 0 | 100% ✓ |
| **C** | after MATH-500 labels switched to dataset levels | 200 | **94.0%** | 49.5% | 1 | (not in set) |

### Run A — first full run (original code)

```
source           n     ±1 tier   exact    timeouts
explain_hand     10    100.0%    0.0%     0
gsm8k            100   100.0%    77.0%    0
math500          100   76.0%     39.0%    0
swebench_lite    100   90.0%     46.0%    0
trivial          52    98.1%     98.1%    0
TOTAL            362   90.3%     58.8%    0

Estimated spend: $0.36
trivial bypass (tier-0 exact): 98.1% ✗ CONTRACT VIOLATION — tune trivialNoul/policy
```

Two things stood out. The **trivial contract failed** (one greeting escaped), and
`explain_hand` was 0% exact while 100% within ±1 — a sign the *labels* were wrong, not the
router.

### Run B — after the contract fix, labels unchanged

```
source           n     ±1 tier   exact    timeouts
explain_hand     10    100.0%    0.0%     0
gsm8k            100   100.0%    64.0%    0
math500          100   70.0%     33.0%    0
swebench_lite    100   89.0%     46.0%    0
trivial          52    100.0%    100.0%   0
TOTAL            362   88.7%     53.9%    0
```

**Contract restored: 100%.** The `"go"` violation was traced to the route card —
`noul 0.69, complexity 0.02, confidence 0.46`. Jev correctly saw complexity ≈ 0, but the
old rule "confidence < 0.5 → escalate to tier 2" fired on a two-character prompt. Fixed by
only escalating when complexity is at least `escalateOnlyAboveComplexity` (0.5).

Note `gsm8k` exact *fell* 77% → 64% while routing **improved**: the fix moved easy items
from tier 2 to tier 0, and under the then-current `[1,2]` label a correct tier-0 route
counted as "not exact". A lower `exact` was the sign of a more correct router.

Tier distribution and Jev's scoring from this run:

```
source           t0      t1      t2      t3      mean complexity
explain_hand     100%    0%      0%      0%      0.05
gsm8k            36%     52%     12%     0%      0.73
math500          30%     37%     33%     0%      0.87
swebench_lite    11%     43%     46%     0%      1.08
trivial          100%    0%      0%      0%      0.00

source           <0.5   <1     <1.5   <2      max
math500          19     48     18     15      1.95
swebench_lite    4      40     36     20      1.83
```

### Run C — after MATH-500 labels became difficulty-derived

```
source           n     ±1 tier   exact    timeouts
math500          100   97.0%     58.0%    0
swebench_lite    100   91.0%     41.0%    1
TOTAL            200   94.0%     49.5%    1
```

`math500` jumped **70% → 97%** within ±1 tier. Nothing about the router changed — only the
expectations. MATH-500's own `level` field (1–5) now drives the label
(`L1–2 → [0,1]`, `L3 → [1,2]`, `L4–5 → [2,3]`) instead of a flat `[2,3]`.

---

## Findings

**1. Labels were the weak point, not the router.** `math500` 70% → 97% and `explain_hand`
0% → 100% came from correcting expectations. Any eval number should be read with its
labels in view; SWE-bench's `exact` is still untrustworthy because it has no difficulty
field to derive from.

**2. A falling score once signalled an improving router.** `gsm8k` exact 77% → 64% after a
correctness fix, because the label was stale. Metrics without good labels can move the
wrong way when the system gets better.

**3. The trivial contract caught a real bug.** 98.1% → 100% after fixing low-confidence
escalation on near-zero-complexity prompts. This is why the contract is a hard gate and
not a soft target.

**4. Under-routing is the risk, not overspending.** Cross-referencing the audit against
dataset difficulty (Run C): roughly **7–9% of genuinely hard prompts still land on nano**,
concentrated in **terse SWE-bench issue titles** — e.g. a Django validator fix scored
complexity 0.28. Confidence escalation cannot catch this: those misroutes had *adequate*
confidence (0.37–0.68).

**5. Tier 3 (frontier) essentially never fires.** Across every run, `t3 = 0%` for all
sources except a single `math500` count in the histogram's top band. Jev's 0–2 scale is
conservative — even a cubic proof scored ~1.0 — so the mid tier carries hard work.
Frontier is currently decorative; that is a cost decision to make deliberately.

**6. Timeouts are rare and safe.** 0 in two full runs, 1 in one; each falls back to the
configured fail-safe tier and is recorded per row rather than failing the request.

---

## What to tune from these numbers

| Observation | Lever |
|---|---|
| Hard math/proofs hitting nano (7–9%) | lower the first band, e.g. `[[0.6,0],[1.3,1],[1.7,2],[999,3]]` |
| Terse bug reports scoring trivially | category floor: `{"category":"code_debug","tier":2}` |
| Tier 3 unreachable on the 0–2 scale | accept mid as the hard-work tier, or add explicit overrides |
| Re-tuning cost | `--only <sources>` re-checks a subset for pennies |

Each change is a config edit; re-measure with a targeted run, not a full one.

---

## Limitations to keep in mind

- **Routing accuracy ≠ answer quality.** Nothing here proves nano *fails* a hard prompt or
  that mid *solves* it. That is Stage 2, not yet run.
- **SWE-bench labels are a guess.** No difficulty field exists; `[2,3]` is an assumption.
  Its `exact` column should not be read as ground truth.
- **Single runs.** These are one run each. Jev is a model; expect run-to-run variation. No
  confidence intervals are reported, and none should be implied.
- **Benchmark contamination.** Cheap models may have seen GSM8K/MATH-500. Stage-2
  cross-checks against published per-model leaderboards help detect it.
- **Timeout rows are excluded from accuracy** (they fall back), so a run with many
  timeouts overstates agreement.
- **`is_trivial` vs `complexity` can disagree.** The trivial gate can override a non-zero
  complexity; that is intended, but it means the two columns are not independent.

---

## Related

- [README.md](../README.md) → "Eval methodology" and "Measured results" — methodology and the reference snapshot
- [HOW_IT_DECIDES.md](HOW_IT_DECIDES.md) — how a single decision is made (with `flux trace`)
- [ROADMAP.md](../ROADMAP.md) — v0.2 makes these bands data-derived; v0.3 adds context economics
