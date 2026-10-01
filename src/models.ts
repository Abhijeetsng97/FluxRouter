// /v1/models response construction.
//
// The router advertises a single alias (`flux`) by default: the whole point is that
// clients stop choosing models. Setting `server.listTierModels: true` additionally
// exposes the concrete tier models (read-only discoverability), tagged with their tier —
// note that incoming model ids are still ignored unless `respectIncomingModel` is true.

import type { FluxConfig } from "./config.ts";
import { FLUX_MODEL_ALIAS } from "./types.ts";

export interface ModelEntry {
  id: string;
  object: "model";
  created: number;
  owned_by: string;
  /** FluxRouter extension: which tier serves this model (absent for the alias). */
  x_flux_tier?: number;
  x_flux_upstream?: string;
}

export interface ModelsResponse {
  object: "list";
  data: ModelEntry[];
}

export function buildModelsResponse(config: FluxConfig): ModelsResponse {
  const data: ModelEntry[] = [
    { id: FLUX_MODEL_ALIAS, object: "model", created: 0, owned_by: "fluxrouter" },
  ];
  if (config.server.listTierModels) {
    for (const tier of config.tiers) {
      for (const m of tier.models) {
        data.push({
          id: m.id,
          object: "model",
          created: 0,
          owned_by: `fluxrouter/tier-${tier.id}`,
          x_flux_tier: tier.id,
          x_flux_upstream: m.upstream,
        });
      }
    }
  }
  return { object: "list", data };
}