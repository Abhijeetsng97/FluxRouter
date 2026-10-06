// Sticky session store: pin a routed tier per session, support upward escape.

import { createHash } from "node:crypto";
import type { TierId } from "./types.ts";

export interface SessionState {
  tier: TierId;
  model: string;
  upstream: string;
  pinnedAt: string;
  turns: number;
}

const TTL_MS = 6 * 60 * 60 * 1000; // 6 hours of inactivity

export class SessionStore {
  private sessions = new Map<string, SessionState>();
  private sweepTimer: NodeJS.Timeout | undefined;

  constructor() {
    this.sweepTimer = setInterval(() => this.sweep(), 10 * 60 * 1000);
    this.sweepTimer.unref?.();
  }

  /** Stable session id: hash of system prompt + first user message. */
  static sessionIdFor(messages: Array<{ role: string; content: string }>): string {
    const sys = messages.find((m) => m.role === "system")?.content ?? "";
    const firstUser = messages.find((m) => m.role === "user")?.content ?? "";
    return createHash("sha256")
      .update(`${sys.length}:${sys}\u0000${firstUser}`)
      .digest("hex")
      .slice(0, 24);
  }

  get(sessionId: string): SessionState | undefined {
    const s = this.sessions.get(sessionId);
    if (!s) return undefined;
    if (Date.now() - new Date(s.pinnedAt).getTime() > TTL_MS) {
      this.sessions.delete(sessionId);
      return undefined;
    }
    return s;
  }

  pin(sessionId: string, tier: TierId, model: string, upstream: string): void {
    this.sessions.set(sessionId, {
      tier,
      model,
      upstream,
      pinnedAt: new Date().toISOString(),
      turns: 1,
    });
  }

  /** Update the pinned tier (upward escape). */
  rePin(sessionId: string, tier: TierId, model: string, upstream: string): void {
    const s = this.sessions.get(sessionId);
    if (s) {
      s.tier = tier;
      s.model = model;
      s.upstream = upstream;
      s.pinnedAt = new Date().toISOString();
      s.turns += 1;
    } else {
      this.pin(sessionId, tier, model, upstream);
    }
  }

  bumpTurn(sessionId: string): void {
    const s = this.sessions.get(sessionId);
    if (s) {
      s.turns += 1;
      s.pinnedAt = new Date().toISOString();
    }
  }

  sweep(): void {
    const now = Date.now();
    for (const [k, v] of this.sessions) {
      if (now - new Date(v.pinnedAt).getTime() > TTL_MS) this.sessions.delete(k);
    }
  }

  get size(): number {
    return this.sessions.size;
  }

  dispose(): void {
    if (this.sweepTimer) clearInterval(this.sweepTimer);
    this.sessions.clear();
  }
}