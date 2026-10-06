// package server mirrors old/src/models.ts: the /v1/models response.
// The alias-only default and the x_flux_* tag keys are contract.
package server

import (
	"fmt"

	"github.com/abhijeet/fluxrouter/internal/config"
	"github.com/abhijeet/fluxrouter/internal/types"
)

// Entry mirrors models.ts ModelEntry.
type ModelEntry struct {
	ID         string `json:"id"`
	Object     string `json:"object"`
	Created    int64  `json:"created"`
	OwnedBy    string `json:"owned_by"`
	XFluxTier     *int  `json:"x_flux_tier,omitempty"`
	XFluxUpstream *string `json:"x_flux_upstream,omitempty"`
}

// ModelsResponse mirrors models.ts ModelsResponse.
type ModelsResponse struct {
	Object string       `json:"object"`
	Data   []ModelEntry `json:"data"`
}

// BuildModelsResponse mirrors models.ts buildModelsResponse.
func BuildModelsResponse(cfg config.Config) ModelsResponse {
	data := []ModelEntry{{
		ID: types.FLUXModelAlias, Object: "model", Created: 0, OwnedBy: "fluxrouter",
	}}
	if cfg.Server.ListTierModels {
		for _, tier := range cfg.Tiers {
			for _, m := range tier.Models {
				tierID := int(tier.ID)
				up := string(m.Upstream)
				data = append(data, ModelEntry{
					ID: m.ID, Object: "model", Created: 0,
					OwnedBy:    fmt.Sprintf("fluxrouter/tier-%d", tierID),
				XFluxTier:     &tierID,
				XFluxUpstream: &up,
				})
			}
		}
	}
	return ModelsResponse{Object: "list", Data: data}
}