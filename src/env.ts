// Optional .env loading — no dependency, uses Node's built-in loader.
//
// Precedence (highest first):
//   1. real process environment
//   2. .env.local
//   3. .env
//   4. explicit --env-file <path> (if given, replaces the default candidates)
//
// Node's process.loadEnvFile() never overrides an already-set variable, so the
// highest-precedence file must be loaded first.

import { existsSync } from "node:fs";

export interface DotEnvResult {
  loaded: string[];
}

export function loadDotEnv(explicitPath?: string): DotEnvResult {
  const candidates = explicitPath ? [explicitPath] : [".env.local", ".env"];
  const loaded: string[] = [];
  // Highest precedence first: earlier loads win because Node never overrides an
  // already-set variable.
  for (const p of candidates) {
    if (!p || !existsSync(p)) continue;
    try {
      process.loadEnvFile(p);
      loaded.push(p);
    } catch (e) {
      console.warn(`warning: failed to load env file ${p}: ${(e as Error).message}`);
    }
  }
  return { loaded };
}