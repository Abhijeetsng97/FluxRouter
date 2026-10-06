// Package models mirrors old/src/models.ts: the /v1/models response.
// The alias-only default and the x_flux_* tag keys are contract.
package models

import (
	"fmt"

	"github.com/abhijeet/fluxrouter/internal/config"
	"github.com/abhijeet/fluxrouter/internal/types"
)

// Entry mirrors models.ts ModelEntry.
type Entry struct {
	ID         string `json:"id"`
	Object     string `json:"object"`
	Created    int64  `json:"created"`
	OwnedBy    string `json:"owned_by"`
	XFluxTier     *int  `json:"x_flux_tier,omitempty"`
	XFluxUpstream *string `json:"x_flux_upstream,omitempty"`
}

// Response mirrors models.ts ModelsResponse.
type Response struct {
	Object string  `json:"object"`
	Data   []Entry `json:"data"`
}

// BuildResponse mirrors models.ts buildModelsResponse.
func BuildResponse(cfg config.Config) Response {
	data := []Entry{{
		ID: types.FLUXModelAlias, Object: "model", Created: 0, OwnedBy: "fluxrouter",
	}}
	if cfg.Server.ListTierModels {
		for _, tier := range cfg.Tiers {
			for _, m := range tier.Models {
				tierID := int(tier.ID)
				up := string(m.Upstream)
				data = append(data, Entry{
					ID: m.ID, Object: "model", Created: 0,
					OwnedBy:    fmt.Sprintf("fluxrouter/tier-%d", tierID),
				XFluxTier:     &tierID,
				XFluxUpstream: &up,
				})
			}
		}
	}
	return Response{Object: "list", Data: data}
}