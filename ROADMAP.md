# FluxRouter Roadmap

Direction, not promises. Each version is gated on the previous one being *measured*,
not just shipped.

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
a real agent harness for a sustained period.

## v0.2 — calibration, routing logic, and real-harness validation (next)

**Theme: stop trusting the default bands, start trusting measured behaviour in a real agent.**

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
      work?" on a cheap cadence, and allow a session to drop back down a tier — the
      missing half of sticky-with-escape.
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

**Exit criteria for v0.2:** measured savings and mis-route rate on at least one week of
real agent traffic; the routing bands replaced by ones derived from that data; no
known case where a code task silently lands on the smallest model.

## v0.3 — context economics (planned)

**Theme: the request body is the budget. Routing decides the price per token; the next
lever is how many tokens you send at all.**

- [ ] **Context compaction.** Summarise turns older than a session threshold with a
      cheap model before forwarding, targeting a large reduction on long sessions.
      Compaction runs off the critical path, never blocks a request, and its own cost is
      attributed per session in the route card. (Compaction and trimming are the v0.3
      themes — this item lands there, with the quality gate below.)
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

**Exit criteria for v0.3:** measured token reduction on long sessions with no measurable
answer-quality regression, and a documented break-even point (when compaction costs more
than it saves).

## v1.0 — beyond routing

Candidate, in rough order of expected value:

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
