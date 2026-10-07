// package routing mirrors old/src/policy.ts: context gate, 2D table, trivial
// bypass, confidence escalation, cost guard. Every branch comment references
// the TS line it ports; the golden fixture parity harness enforces identical
// decisions, so do not "improve" any ordering here without regenerating
// fixtures from the old/ engine in the same commit.
package routing

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/abhijeet/fluxrouter/internal/types"
)

// Band mirrors TS `Array<[number, TierId]>` entries: complexity < Threshold → Tier.
// In JSON the TS shape is an array PAIR: [0.8, 0]. Unmarshal accepts both the
// pair form and the object form {"threshold":0.8,"tier":0}.
type Band struct {
	Threshold float64
	Tier      types.TierId
}

// UnmarshalJSON accepts [threshold, tier] pairs (the TS wire shape) and
// objects.
func (b *Band) UnmarshalJSON(data []byte) error {
	var pair []any
	if err := json.Unmarshal(data, &pair); err == nil && len(pair) == 2 {
		if th, ok := pair[0].(float64); ok {
			if ti, ok := pair[1].(float64); ok {
				b.Threshold = th
				b.Tier = types.TierId(ti)
				return nil
			}
		}
		return fmt.Errorf("complexityToTier pair must be [number, tier]")
	}
	var obj struct {
		Threshold float64      `json:"threshold"`
		Tier      types.TierId `json:"tier"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	*b = Band{Threshold: obj.Threshold, Tier: obj.Tier}
	return nil
}

// MarshalJSON writes the TS pair form.
func (b Band) MarshalJSON() ([]byte, error) {
	return json.Marshal([]float64{b.Threshold, float64(b.Tier)})
}

// Override mirrors TS `{ category, minComplexity?, tier }`.
type Override struct {
	Category     string
	MinComplexity *float64 // nil = undefined
	Tier         types.TierId
}

// PolicyConfig mirrors the inline `config` object of TS PolicyInput.
type PolicyConfig struct {
	MinConfidence               float64
	TrivialNoul                 float64
	EscalateOnlyAboveComplexity float64
	ComplexityToTier            []Band
	Overrides                   []Override
	PerRequestCapUsd            float64
	StickyEnabled               bool
	EscapeConfidence            float64
}

// Input mirrors TS PolicyInput. Classification nil => Jev failed/timed out.
type Input struct {
	Classification  *types.ClassificationResult
	Categories      []string // for tests / direct use (unused in decision)
	RequestTokens   int64
	StickyTier      *types.TierId // nil => no sticky state
	Tiers           []types.Tier
	FallbackTier    *types.TierId
	BudgetTokensOut int64 // default 2000

	Config PolicyConfig
}

// Outcome mirrors TS PolicyOutcome.
type Outcome struct {
	Tier             types.TierId
	Reason           types.RouteReason
	Category         string
	Complexity       float64
	Confidence       float64
	ProjectedCostUsd float64
	Notes            []string
}

// ContextGate mirrors policy.ts contextGate: keep tiers whose any model fits.
func ContextGate(tiers []types.Tier, requestTokens int64) []types.Tier {
	out := make([]types.Tier, 0, len(tiers))
	for _, t := range tiers {
		for _, m := range t.Models {
			if m.Ctx >= requestTokens {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// TierByContext mirrors policy.ts tierByContext: highest tier that fits.
func TierByContext(tiers []types.Tier, requestTokens int64) *types.Tier {
	viable := ContextGate(tiers, requestTokens)
	if len(viable) == 0 {
		return nil
	}
	return &viable[len(viable)-1]
}

// ComplexityToTier mirrors policy.ts complexityToTier: strict "<", last band
// is the catch-all; empty table guards to tier 2.
func ComplexityToTier(complexity float64, table []Band) types.TierId {
	for _, b := range table {
		if complexity < b.Threshold { // NOTE: strict <, matches TS
			return b.Tier
		}
	}
	if len(table) == 0 {
		return types.TierMid
	}
	return table[len(table)-1].Tier
}

// ApplyOverrides mirrors policy.ts applyOverrides: first matching override
// wins; nil => no override fired.
func ApplyOverrides(category string, complexity float64, overrides []Override) *types.TierId {
	for _, ov := range overrides {
		if ov.Category == category {
			if ov.MinComplexity == nil || complexity >= *ov.MinComplexity {
				t := ov.Tier
				return &t
			}
		}
	}
	return nil
}

// RouteRequest mirrors policy.ts routeRequest. Step numbers match TS comments.
func RouteRequest(input Input) Outcome {
	var notes []string
	cfg := input.Config

	// 1. Sticky: reuse pinned tier for the session.
	if cfg.StickyEnabled && input.StickyTier != nil {
		category := "other"
		complexity := 0.0
		confidence := 1.0
		if input.Classification != nil {
			category = input.Classification.Category
			complexity = input.Classification.Complexity
			confidence = input.Classification.ComplexityConfidence
		}
		return Outcome{
			Tier:             *input.StickyTier,
			Reason:           types.ReasonSticky,
			Category:         category,
			Complexity:       complexity,
			Confidence:       confidence,
			ProjectedCostUsd: 0,
			Notes:            notes,
		}
	}

	// 2. Jev unavailable → fail-safe tier (default flash, not the cheapest).
	if input.Classification == nil {
		wanted := types.DefaultFallbackTier
		if input.FallbackTier != nil {
			wanted = *input.FallbackTier
		}
		fallback := FitFallbackTier(input.Tiers, wanted, input.RequestTokens)
		notes = append(notes, fmt.Sprintf("jev unavailable, using fallback tier %d", fallback))
		return Outcome{
			Tier:             fallback,
			Reason:           types.ReasonJevUnavailableFallback,
			Category:         "unknown",
			Complexity:       0,
			Confidence:       0,
			ProjectedCostUsd: 0,
			Notes:            notes,
		}
	}

	cls := input.Classification
	confidence := math.Min(cls.CategoryConfidence, cls.ComplexityConfidence)

	// 3. Trivial bypass (only if tier 0 exists — else continue to policy).
	if cls.TrivialNoul > cfg.TrivialNoul {
		notes = append(notes, fmt.Sprintf("trivial noul=%.2f > %g", cls.TrivialNoul, cfg.TrivialNoul))
		if tier0 := FindTier(input.Tiers, 0); tier0 != nil {
			return Outcome{
				Tier:             types.TierNano,
				Reason:           types.ReasonTrivialBypass,
				Category:         cls.Category,
				Complexity:       cls.Complexity,
				Confidence:       confidence,
				ProjectedCostUsd: 0,
				Notes:            notes,
			}
		}
		notes = append(notes, "no tier 0 available, continuing policy")
	}

	// 4. Policy: overrides then default table.
	tier := ApplyOverrides(cls.Category, cls.Complexity, cfg.Overrides)
	reason := types.ReasonPolicy
	if tier == nil {
		t := ComplexityToTier(cls.Complexity, cfg.ComplexityToTier)
		tier = &t
	}

	// 5. Context gate: chosen tier can't fit → cheapest tier that fits.
	viable := ContextGate(input.Tiers, input.RequestTokens)
	if len(viable) == 0 {
		notes = append(notes, "no tier can fit request tokens, using highest-tier model anyway")
	} else if !tierIn(viable, *tier) {
		notes = append(notes, fmt.Sprintf("tier %d cannot fit %d tokens, escalating for context", *tier, input.RequestTokens))
		if fit := CheapestFittingTier(input.Tiers, input.RequestTokens); fit != nil {
			tier = &fit.ID
			reason = types.ReasonContextGate
		}
	}

	// 6. Low-confidence escalation — only where under-routing is a risk:
	//    complexity >= escalateOnlyAboveComplexity AND tier < 2.
	//    (The "go" regression: a trivially-simple low-confidence reply must
	//    stay on nano.)
	if confidence < cfg.MinConfidence &&
		cls.Complexity >= cfg.EscalateOnlyAboveComplexity &&
		*tier < 2 {
		notes = append(notes, fmt.Sprintf("confidence %.2f < %g at complexity %.2f, escalating to mid",
			confidence, cfg.MinConfidence, cls.Complexity))
		t := types.TierMid
		tier = &t
		reason = types.ReasonLowConfidenceEsc
	}

	// 7. Cost guard: downgrade to closest fitting tier below, within cap.
	if tierObj := FindTier(input.Tiers, *tier); tierObj != nil {
		out := input.BudgetTokensOut
		if out == 0 {
			out = 2000
		}
		model := tierObj.Models[0]
		projected := ProjectedCost(input.RequestTokens, out,
			Rates{In: model.In, Out: model.Out})
		if projected > cfg.PerRequestCapUsd {
			notes = append(notes, fmt.Sprintf("projected $%.4f > cap $%g, downgrading", projected, cfg.PerRequestCapUsd))
			if down := NearestCheaperFittingTier(input.Tiers, *tier, input.RequestTokens, cfg.PerRequestCapUsd, out); down != nil {
				tier = down
				reason = types.ReasonCostGuard
			}
		}
	}

	return Outcome{
		Tier:             *tier,
		Reason:           reason,
		Category:         cls.Category,
		Complexity:       cls.Complexity,
		Confidence:       confidence,
		ProjectedCostUsd: projectedCostForTier(input.Tiers, *tier, input.RequestTokens, input.BudgetTokensOut),
		Notes:            notes,
	}
}

// HighestTier mirrors policy.ts highestTier.
func HighestTier(tiers []types.Tier) types.TierId {
	max := types.TierId(0)
	for _, t := range tiers {
		if t.ID > max {
			max = t.ID
		}
	}
	return max
}

// FitFallbackTier mirrors policy.ts fitFallbackTier: prefer wanted tier that
// fits; else nearest larger-context tier at/above wanted; else highest.
func FitFallbackTier(tiers []types.Tier, wanted types.TierId, requestTokens int64) types.TierId {
	sorted := make([]types.Tier, len(tiers))
	copy(sorted, tiers)
	// sort by id ascending (stable — TS .sort((a,b)=>a.id-b.id) is stable)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].ID < sorted[j-1].ID; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	var last *types.Tier
	for i := range sorted {
		if sorted[i].ID >= wanted {
			last = &sorted[i]
			if anyModelFits(sorted[i], requestTokens) {
				return sorted[i].ID
			}
		}
	}
	if last != nil {
		return last.ID
	}
	if len(sorted) > 0 {
		return sorted[len(sorted)-1].ID
	}
	return types.TierFlash
}

// FindTier mirrors policy.ts findTier.
func FindTier(tiers []types.Tier, id types.TierId) *types.Tier {
	for i := range tiers {
		if tiers[i].ID == id {
			return &tiers[i]
		}
	}
	return nil
}

// CheapestFittingTier mirrors policy.ts cheapestFittingTier.
func CheapestFittingTier(tiers []types.Tier, requestTokens int64) *types.Tier {
	viable := ContextGate(tiers, requestTokens)
	if len(viable) == 0 {
		return nil
	}
	return &viable[0]
}

// NearestCheaperFittingTier mirrors policy.ts nearestCheaperFittingTier:
// closest tier BELOW original that fits context AND cap; else cheapest
// fitting tier (even if over cap — better logged than silent); nil if none.
func NearestCheaperFittingTier(tiers []types.Tier, originalTier types.TierId, requestTokens int64, capUsd float64, budgetTokensOut int64) *types.TierId {
	if budgetTokensOut == 0 {
		budgetTokensOut = 2000
	}
	for t := originalTier - 1; t >= 0; t-- {
		cand := FindTier(tiers, t)
		if cand == nil {
			continue
		}
		if !anyModelFits(*cand, requestTokens) {
			continue
		}
		m := cand.Models[0]
		c := ProjectedCost(requestTokens, budgetTokensOut, Rates{In: m.In, Out: m.Out})
		if c <= capUsd {
			return &t
		}
	}
	cheapest := CheapestFittingTier(tiers, requestTokens)
	if cheapest == nil {
		return nil
	}
	id := cheapest.ID
	return &id
}

// StickyEscape mirrors policy.ts stickyEscape.
func StickyEscape(pinned types.TierId, cls types.ClassificationResult, bands []Band, overrides []Override, escapeConfidence float64) *types.TierId {
	confidence := math.Min(cls.CategoryConfidence, cls.ComplexityConfidence)
	if confidence < escapeConfidence {
		return nil
	}
	tier := ApplyOverrides(cls.Category, cls.Complexity, overrides)
	if tier == nil {
		t := ComplexityToTier(cls.Complexity, bands)
		tier = &t
	}
	if *tier > pinned {
		return tier
	}
	return nil
}

// projectedCostForTier mirrors the private TS helper.
func projectedCostForTier(tiers []types.Tier, tier types.TierId, requestTokens int64, outTokens int64) float64 {
	t := FindTier(tiers, tier)
	if t == nil {
		return 0
	}
	m := t.Models[0]
	out := outTokens
	if out == 0 {
		out = 2000
	}
	return ProjectedCost(requestTokens, out, Rates{In: m.In, Out: m.Out})
}

func anyModelFits(t types.Tier, requestTokens int64) bool {
	for _, m := range t.Models {
		if m.Ctx >= requestTokens {
			return true
		}
	}
	return false
}

func tierIn(tiers []types.Tier, id types.TierId) bool {
	for _, t := range tiers {
		if t.ID == id {
			return true
		}
	}
	return false
}