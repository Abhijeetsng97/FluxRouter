// Models tests: alias-only default, tier listing, x_flux tags.
package server

import (
	"encoding/json"
	"testing"

	"github.com/abhijeet/fluxrouter/internal/config"
)

func TestBuildResponseAliasOnlyByDefault(t *testing.T) {
	resp := BuildModelsResponse(config.DefaultConfig())
	if len(resp.Data) != 1 || resp.Data[0].ID != "flux" || resp.Object != "list" {
		t.Fatalf("default models = %+v", resp)
	}
	b, _ := json.Marshal(resp)
	if string(b) != `{"object":"list","data":[{"id":"flux","object":"model","created":0,"owned_by":"fluxrouter"}]}` {
		t.Fatalf("JSON shape wrong: %s", b)
	}
}

func TestBuildResponseListTierModels(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.ListTierModels = true
	resp := BuildModelsResponse(cfg)
	if len(resp.Data) != 5 {
		t.Fatalf("data len = %d, want 5 (alias + 4 tiers)", len(resp.Data))
	}
	if resp.Data[1].ID != "nemotron-3-nano:30b" || resp.Data[1].XFluxTier == nil || *resp.Data[1].XFluxTier != 0 {
		t.Fatalf("tier model wrong: %+v", resp.Data[1])
	}
	if resp.Data[1].XFluxUpstream == nil || *resp.Data[1].XFluxUpstream != "ollama" {
		t.Fatalf("upstream tag wrong: %+v", resp.Data[1])
	}
	if resp.Data[4].ID != "kimi-k3" {
		t.Fatalf("frontier model wrong: %+v", resp.Data[4])
	}
	// x_flux_* must be OMITTED for the alias.
	b, _ := json.Marshal(resp.Data[0])
	if strings_contain(string(b), "x_flux") {
		t.Fatalf("alias must not carry x_flux tags: %s", b)
	}
}

func strings_contain(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}