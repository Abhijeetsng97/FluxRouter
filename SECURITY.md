# FluxRouter — Security Policy

## Supported versions

| Version | Supported |
|---|---|
| 0.1.x | ✅ |

> FluxRouter v0.1 is distributed as a **source checkout / git clone**, not yet published
> to npm. Run it with Node 24+ from the repository; see README setup.

## Threat model (what FluxRouter is / isn't)

FluxRouter is a **local, single-user proxy**. It sits on `127.0.0.1` between your
OpenAI-compatible clients (OpenChamber, OpenCode, curl, SDKs) and upstream model
providers (Ollama Cloud, OpenRouter) plus the Jev classification service.

**In scope:** anything that changes routing or leaks keys/logs when the proxy is used
as documented on one developer's machine.
**Not in scope at v1:** multi-user production deployments, cluster auth, tenant
isolation — see "Hardening for shared deployment" below.

## Data handling

| Data | Where | Notes |
|---|---|---|
| Upstream API keys | process environment only | never logged, never persisted |
| `FLUX_AUTH_TOKEN` | env, compared per request | required for non-loopback binds |
| Conversation excerpts | sent to Jev for classification only | head+tail, ~2k chars, 3-question call |
| Route cards | `.fluxrouter/route-cards.{jsonl,db}` local disk | metadata only (category, tokens, cost, latency) — **message content is never logged** |
| TypeSafe key | in-memory / env | never sent to browser or written to config |

## Known limitations (documented, accepted for v1)

1. **Prompt injection can influence routing.** A crafted message might push the
   classifier toward a specific category/tier. Blast radius: wrong model choice — Jev
   receives excerpts but never executes anything and returns typed decisions only.
   Mitigation for untrusted-content users: cap exposure by lowering
   `cost.perRequestCapUsd`; excerpt sanitization is planned for a later version.
2. **Jev response is trusted.** A compromised/malicious classification endpoint could
   steer tiers. Use the official `https://api.typesafe.ai/v1/systemone` unless you
   operate your own gateway.
3. **Local file access to `.fluxrouter/`** implies local-machine security. SQLite/JSONL
   contain your metadata (not message content). Protect the directory as you would
   any local usage data.

## Hardening for shared deployment (if you must)

- Run behind a reverse proxy that terminates auth; keep FluxRouter on loopback.
- Set `FLUX_AUTH_TOKEN` and distribute only over your existing secret channel.
- Put `fluxrouter.config.json` under read-only control; treat its policy as code review.
- Ship route cards to your own log pipeline with the same retention rules you apply to
  other operational logs.

## Reporting a vulnerability

Open a **private** security advisory via GitHub ("Report a vulnerability" on the
repository's Security tab). Please include: affected paths, a minimal reproduction,
Node version, and whether the issue can be triggered without valid API keys.

We will acknowledge within 7 days. Please do not test against third-party upstream
services in a way that violates their terms.

## What we will never do

- Log or persist message content.
- Write API keys to disk (config, cards, or logs).
- Send message content anywhere except the selected upstream model and the Jev
  classifier excerpt.