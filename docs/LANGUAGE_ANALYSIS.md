# FluxRouter Language Analysis

**Date:** 2026-10-06
**Status:** Research-based analysis (external evidence + codebase audit)
**Question:** If FluxRouter were written today — or rewritten — which language is the
right fit? (TypeScript/Node, Go, Rust, Python, and others considered)

---

## TL;DR

**Keep TypeScript through v0.2. If/when a rewrite is justified, the right target is Go.
Rust is the right answer only if the gateway data plane itself becomes the product.
Python is the wrong answer for the data plane.**

The single most important number in this analysis: **FluxRouter's total request pipeline
overhead is a few milliseconds, while the LLM upstream takes 500–5,000+ ms.** The proxy
is <1% of request time at FluxRouter's scale. No language choice changes what the user
experiences on a single-request basis — so the decision is driven by **distribution,
footprint, and iteration speed**, not by request latency.

What a rewrite *would* buy (in Go): a single static binary (no Node 24+ requirement —
the #1 install friction today), ~10× lower memory, near-instant startup, and the
easiest concurrency model for streaming among the compiled candidates. What it would
*cost*: ~2–4 weeks of solo-dev time re-implementing ~3,200 lines of working, tested,
measured code — while v0.2's actual risk (band calibration) is orthogonal to language.

---

## 1. What FluxRouter actually is (workload profile)

From auditing `src/`, `cli/`, and `tests/`:

| Property | Value |
|---|---|
| Size | ~3,200 lines TypeScript (src ~1.8k, cli ~1k, tests ~480) |
| Runtime deps | 4 (`hono`, `@hono/node-server`, `undici`, `commander`) |
| Deployment | Local, single-user, binds `127.0.0.1:8787` |
| Concurrency | 1–10 in-flight requests (one agent session at a time) |
| Work type | **I/O-bound**: HTTP in → Jev classify (external, ~400 ms) → policy table → upstream LLM (500–5,000+ ms) → SSE stream back |
| CPU work | Token estimation, JSON transforms, SQLite + JSONL writes — all sub-ms |
| State | SQLite + JSONL route cards in `.fluxrouter/` |
| Interfaces | OpenAI-compatible REST, SSE streaming, a CLI (`flux`), Prometheus `/metrics` |
| Team | Single maintainer, MIT, not yet published to npm |

Two features of this profile dominate the language decision:

1. **The bottleneck is the network, never the router.** Jev (~400 ms) plus upstream
   LLM (0.5–30 s) mean the router's own overhead is noise. This is the same conclusion
   Preto.ai measured in production: ~5–8 ms total pipeline overhead vs 500–5,000 ms of
   LLM time — "our proxy is under 1% of total request time."
2. **The product is the policy and the audit trail, not the data plane.** The
   differentiators are calibrated routing, cost guards, route cards, and evals — all
   logic that must iterate weekly. The forwarding path is plumbing that should never
   need to change.

v0.3's context economics (compaction, trimming) add more CPU work, but still
streaming-scale, not compute-scale — summarization happens on an LLM, off the
critical path by design.

---

## 2. Evidence gathered (external research)

What teams building the same class of system chose, and what they measured:

### LiteLLM: Python → Rust (2026)
LiteLLM — the incumbent Python LLM gateway — is migrating its hot path to Rust
(axum/tokio), citing their own benchmark: **15× throughput** (453 → 6,782 req/s),
**11× less memory** (359 MB → 32 MB), **150× lower per-request overhead** (7.5 ms →
0.05 ms). Their migration is staged route-by-route with parity gates, exactly the
pattern to copy if a rewrite ever happens. Notably, they describe the payoff as
mattering "most for high-throughput, low-latency workloads like classification and
embeddings at scale" — not single-user proxies.

### Bifrost: Go
Bifrost (maximhq), an open-source Go LLM gateway, benchmarks **9.5× faster throughput,
54× lower P99, and 68% less memory than LiteLLM-Python** (vendor-run). Its existence
proves Go reaches production-grade LLM-gateway performance with a stdlib-first stack.

### Preto.ai: chose Go over Rust and Python (2026)
Built an LLM proxy, evaluated all three, shipped Go. Their measured trade-off:

| Dimension | Go | Rust |
|---|---|---|
| Proxy overhead | ~11 μs/req at 5K RPS | <1 ms P99 at 10K QPS |
| Single-instance ceiling | 5,000+ RPS | 10,000+ QPS |
| Memory under load | ~200 MB at 5K RPS | ~50 MB at 10K QPS |
| Time to MVP | ~2 weeks | ~5–6 weeks |
| Compile time | ~5 s | ~2–5 min |

Their conclusion, which maps 1:1 onto FluxRouter: choose Rust only if (a) you need
10K+ QPS on one instance, (b) memory is a hard edge constraint, or (c) **the proxy IS
the entire product**. For a product where the proxy is the *foundation* and the value
is the intelligence above it — Go.

### Ulaa gateway: prototyped both, shipped Go
A proxy/gateway team that built the data plane in both languages: throughput landed
within ~10% once Go pooled buffers; the real difference was tail latency (Go's GC
shows at p99.9, Rust's doesn't) and — decisively — iteration speed. Their summary:
**"Rust when the cost of the runtime is paid by your users; Go when the cost of the
language is paid by your roadmap."** FluxRouter is a roadmap-funded, single-maintainer
project: every hour spent fighting the borrow checker on streaming connection state
is an hour not spent calibrating bands.

### TensorZero: Rust — the counterexample
TensorZero chose Rust and it's the right call *for them*: the gateway, experimentation
framework, and optimization pipeline are the entire product; they target
industrial-grade throughput where every consumer pays their overhead. This is
criterion (c) from Preto's list. It does not describe FluxRouter today.

### Node.js → Go SSE migration (Studio074)
The most relevant head-to-head for streaming: at 10K concurrent SSE connections,
Node needed ~4.2 GB and degraded past ~5K connections; Go handled 50K+ at ~180 MB
(~8 KB/connection vs Node's ~450 KB). Directionally important — but this is a
50,000-connection broadcast server. FluxRouter peaks at ~10 connections. Node's
per-connection overhead is irrelevant at this scale.

### Python's moving baseline
Free-threaded Python (3.13/3.14, no-GIL) is real progress — ~3% single-thread
overhead vs the GIL build per CPython's own measurements — but ecosystem
compatibility is still partial, and even LiteLLM is moving its hot path *out* of
Python. Nothing in FluxRouter's profile needs Python's ML gravity (the eval datasets
are JSON, not tensors).

---

## 3. Candidate-by-candidate verdict

### TypeScript / Node 24 (current) — "adequate, keep for now"
- ✅ Already working, measured, and tested; zero build step; runs TS natively.
- ✅ The I/O-bound profile is Node's sweet spot; proxy overhead is invisible.
- ✅ Fastest possible iteration on what actually matters (policy, evals, config).
- ❌ **Distribution**: requires Node 24+; "git clone + npm install" is real friction
  for an OSS tool (README's own status line). No static binary.
- ❌ Memory ~100–200 MB RSS for what could be a 15 MB binary doing nothing.
- ❌ GC pauses and single-core limits exist but are irrelevant at 1–10 connections.

**Verdict: not the bottleneck. Stay here until v0.2's calibration work lands.**

### Go — the right rewrite target
- ✅ **Single static binary** (~15 MB) that cross-compiles to Windows/Linux/macOS in
  one command — directly fixes FluxRouter's biggest current pain (install friction).
- ✅ Goroutine-per-request + `http.Flusher` makes SSE streaming trivial (see Preto's
  ~20-line core loop; the same logic is lifecycle hooks in Node/undici).
- ✅ Stdlib `net/http` + `net/http/httputil.ReverseProxy` covers most of the data
  plane with zero dependencies; `modernc.org/sqlite` gives CGo-free SQLite.
- ✅ Fast compiles (~5 s), excellent built-in profiling (pprof), huge hiring pool —
  which for an OSS project translates to *contributor* pool.
- ✅ Proven at this exact job by Bifrost (Go, 5K+ RPS with 100% success).
- ❌ ~10× the memory of Rust under load, GC-visible p99.9 — irrelevant here.
- ❌ Rewrite cost: ~2–4 weeks solo for feature parity of the current 3,200 lines.
- ❌ CLI/type ergonomics slightly weaker than TS for the JSON-heavy config schema
  (mitigated: Go's `encoding/json` + a JSON-schema validator is fine).

**Verdict: best fit if FluxRouter is rewritten — fastest iteration among compiled
options, best distribution story, more than enough performance.**

### Rust — correct only if the gateway becomes the product
- ✅ Best-in-class: ~50 MB RSS under 10K QPS, no GC, flattest tail latency
  (LiteLLM: 0.05 ms overhead; TensorZero's industrial-grade targets).
- ✅ axum/tokio/hyper is a mature, documented stack for exactly this shape
  (an OpenAI-compatible proxy in ~1,600 lines of Rust is a documented pattern).
- ✅ `rusqlite` for route cards; `serde_json` is best-in-class for the JSON surface.
- ❌ **2–3× the development time** for a solo maintainer (Preto: 5–6 weeks vs 2 for an
  MVP; Ulaa: a month to the borrow checker vs a week to traffic). Streaming
  connection state across await points is precisely the hardest Rust pattern, and
  FluxRouter is *made of* streaming connections.
- ❌ Slower iteration on policy changes — the thing that must change weekly during
  calibration. Every data-shape tweak ripples through types and lifetimes.
- ❌ Compile times (~2–5 min) make the eval/trace tight loop worse.

**Verdict: overkill today. Becomes correct if FluxRouter targets multi-tenant/team
mode, embedding as a library (other tools linking the routing core), edge deployment,
or hyperscale — the TensorZero/Helicone/LiteLLM-end-state path.**

### Python — no
- ✅ Unmatched ML/eval ecosystem; free-threading is improving the ceiling.
- ❌ The GIL serializes JSON parsing/token-counting/cost math exactly when
  concurrency climbs; LiteLLM's own published numbers show a wall (~1K QPS) and
  4–8 GB memory under load — and LiteLLM is migrating its hot path to Rust for
  precisely this reason.
- ❌ Adds a runtime version problem worse than Node's.

**Verdict: reject for the data plane. Its eval-side strengths are irrelevant —
FluxRouter's evals are JSON datasets + HTTP, already handled fine in TS.**

### Others, briefly considered
- **Bun/Deno**: same V8 fundamentals, better DX — doesn't change the analysis;
  Bun's `Bun.serve` is genuinely good, but it's still "a JS runtime to install."
- **C# / ASP.NET Core**: TechEmpower-grade perf, excellent SSE, single-file publish
  — but a heavier runtime story than Go and a much smaller LLM-infra community.
- **Java/Kotlin + virtual threads**: production-grade, but JVM footprint and a small
  OSS footprint in this niche.
- **Zig**: compelling binaries, immature web/async ecosystem — too risky solo.
- **Elixir/Phoenix**: superb for streams, per-connection memory and niche fit worse
  than Go for a CLI + SQLite tool.

---

## 4. Head-to-head on what matters for *this* project

Scored against FluxRouter's actual profile (local, single-user, I/O-bound,
policy-iterating, OSS-distributed):

| Criterion (weight) | TS/Node | Go | Rust | Python |
|---|---|---|---|---|
| Proxy overhead vs LLM latency (any is fine) | ✅ ms | ✅ μs | ✅ μs | ⚠️ ms+GIL |
| SSE streaming ergonomics | ✅ good | ✅✅ trivial | ⚠️ hardest | ✅ good |
| Single-binary distribution | ❌ | ✅✅ | ✅✅ | ❌ |
| Memory footprint | ⚠️ ~150 MB | ✅ ~20–50 MB | ✅✅ ~10–30 MB | ❌ 100s MB |
| Solo-dev iteration speed (policy/config churn) | ✅✅ | ✅ | ⚠️ | ✅ |
| Contributor pool (OSS) | ✅✅ | ✅✅ | ⚠️ niche | ✅ |
| Eval/CLI tooling fit | ✅✅ | ✅ | ✅ | ✅✅ (unused edge) |
| SQLite (route cards) | ✅ built-in | ✅ pure-Go | ✅✅ | ✅ |
| Startup time / always-on cost | ⚠️ ~1 s | ✅ ms | ✅ ms | ⚠️ |
| Rewrite cost from today | 0 | ~2–4 wks | ~5–8 wks | ~4–6 wks |
| **Fit for v0.2 goal (calibration velocity)** | ✅✅ | ✅✅ | ✅ | ✅ |

---

## 5. The decisive numbers, in context

```
Latency budget for one request through FluxRouter (today, any language):

  Jev classification........  ~400 ms    (external API)
  Policy table lookup.......  <0.1 ms    (in-process)
  Upstream LLM..............  500–5,000+ ms  (the actual work)
  SSE streaming.............  spans the above
  ─────────────────────────────────────────
  Router's own overhead.....  ~1–8 ms in Node; ~0.1 ms in Go; ~0.05 ms in Rust
                            → all are noise; all are <2% of the request
```

Reaching for Rust to optimize this is optimizing the wrong term. The term that *does*
move with language choice — for an OSS, install-it-yourself tool — is:

```
Install friction:      Node 24 runtime + git clone + npm install   →   curl one 15 MB binary (Go/Rust)
Always-on footprint:   ~150 MB RSS                                  →   20–50 MB (Go) / 10–30 MB (Rust)
Startup:               ~1 s                                         →   ms
```

---

## 6. Recommendation

**Now (v0.2): stay on TypeScript.** The project's stated risk is uncalibrated bands
and unvalidated real-harness behavior — a rewrite spends the calibration budget on a
non-problem. Node's overhead is invisible behind the LLM.

**If/when any of these triggers fire, rewrite in Go (not Rust):**
- You want FluxRouter installable by non-Node users (a GitHub release with
  Windows/macOS/Linux binaries, no runtime) — the strongest near-term trigger.
- Team/multi-user mode (v1.0 candidate) brings sustained concurrent sessions.
- You want it always-running with negligible footprint (background service on a dev
  box, or a small VPS serving a household/team).

**Choose Rust instead of Go only if:** the routing core is to be embedded in other
tools as a library, or FluxRouter pivots to being a general high-throughput gateway
(the LiteLLM/TensorZero lane). If that day comes, adopt LiteLLM's migration pattern
— port one route at a time behind a parity harness, never a big-bang rewrite. Their
sequence (transforms first, streaming next, full route last) is the right risk order
for this codebase too: `classify+policy` (pure logic, unit-tested, no I/O) →
`forward/stream` → CLI/evals.

**Never:** Python for the data plane. If eval tooling ever wants Python (e.g., to
reuse HF dataset loaders), keep it out-of-process — evals are already file- and
HTTP-based, so nothing structural changes.

### Why Go over Rust for this repo, in one line
FluxRouter's value is a policy that must change weekly with measured data; Go keeps
the change-cost low while making distribution trivial, and its performance ceiling
(Bifrost: 5K+ RPS, 100% success) is ~3 orders of magnitude above FluxRouter's needs.
Rust's advantages — the last 10% of tail latency and memory — accrue to projects
whose users pay their runtime cost on every call. FluxRouter's only user is the
person running it, behind a 5,000 ms LLM.

---

## 7. References

- LiteLLM — *Migrating LiteLLM to Rust* (Jun 2026): 15×/11×/150× benchmark, staged
  route-by-route migration plan. https://docs.litellm.ai/blog/litellm-rust-launch
- Preto.ai — *Building an LLM Proxy in Go: Why We Chose Go Over Rust and Python*
  (Mar 2026): measured Go-vs-Rust table, the three-Rust-triggers rule, production
  lessons (goroutine-leak warning is directly applicable to any Go port).
  https://preto.ai/blog/llm-proxy-golang/
- Sudipta Deb (Ulaa) — *Go vs Rust for proxy workloads* (2025): both prototyped with
  real traffic; ~10% throughput delta after buffer pooling; the runtime-cost vs
  roadmap-cost rule. https://debkosh.com/blogs/go-vs-rust-for-proxy-workloads/
- Bifrost (maximhq) — Go LLM gateway, benchmark vs LiteLLM (vendor-run: 9.5×
  throughput, 54× lower P99, 68% less memory). https://github.com/maximhq/bifrost
- TensorZero — Rust LLM gateway + optimization platform (the "gateway is the
  product" case for Rust). https://github.com/tensorzero/tensorzero
- Studio074 — *Go vs Node.js: Building a High-Performance SSE Server*: per-connection
  memory and connection-ceiling numbers (Node 450 KB/conn @ ~5K ceiling vs Go 8 KB
  @ 50K+). Vendor case study; directionally useful, scale is far beyond FluxRouter's.
  https://studio074.dev/case-studies/go-vs-js-sse-server
- CPython docs — free-threading (no-GIL) status and ~3% single-thread overhead vs
  GIL build (3.13/3.14). https://docs.python.org/3/howto/free-threading-python.html
- Loft — *OpenAI-compatible proxy in Rust (axum/tokio)*, ~1,600 LOC reference
  implementation of this exact shape.
  https://loftllc.dev/en/docs/tech/architecture/openai-proxy-pipeline-architecture/