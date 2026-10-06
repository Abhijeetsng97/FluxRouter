// Package types mirrors old/src/types.ts exactly. Port rule: every field,
// JSON key, and constant must be byte-compatible with the TS engine so route
// cards, headers, and config files stay interchangeable across the rewrite.
package types

// TierId is a routing tier. The type-constraint trick mirrors TS's literal
// union `0 | 1 | 2 | 3` while keeping arithmetic comfortable.
type TierId int

const (
	TierNano     TierId = 0
	TierFlash    TierId = 1
	TierMid      TierId = 2
	TierFrontier TierId = 3
)

// IsValidTierId reports whether v is an integer tier 0..3 (config.ts isTierId).
func IsValidTierId(v int) bool { return v >= 0 && v <= 3 }

// UpstreamName mirrors `type UpstreamName = "ollama" | "openrouter"`.
type UpstreamName string

const (
	UpstreamOllama     UpstreamName = "ollama"
	UpstreamOpenRouter UpstreamName = "openrouter"
)

// TierModel mirrors types.ts TierModel. JSON keys are camelCase, identical to TS.
type TierModel struct {
	Upstream        UpstreamName `json:"upstream"`
	ID              string       `json:"id"`
	Ctx             int64        `json:"ctx"`
	In              float64      `json:"in"`   // $/M input
	CachedIn        float64      `json:"cachedIn,omitempty"` // $/M cached input
	Out             float64      `json:"out"`  // $/M output
	ReasoningEffort string       `json:"reasoningEffort,omitempty"`
	FailoverID      string       `json:"failoverId,omitempty"`
}

// HasCachedIn reports whether a cached-input rate is configured.
func (m TierModel) HasCachedIn() bool { return m.CachedIn > 0 }

// Tier mirrors types.ts Tier.
type Tier struct {
	ID     TierId      `json:"id"`
	Name   string      `json:"name"`
	Models []TierModel `json:"models"`
}

// RouteReason mirrors the TS union of reason codes. Values are the wire format.
type RouteReason string

const (
	ReasonTrivialBypass         RouteReason = "trivial_bypass"
	ReasonPolicy                RouteReason = "policy"
	ReasonLowConfidenceEsc      RouteReason = "low_confidence_escalation"
	ReasonSticky                RouteReason = "sticky"
	ReasonStickyEscapeUp        RouteReason = "sticky_escape_up"
	ReasonCostGuard             RouteReason = "cost_guard"
	ReasonContextGate           RouteReason = "context_gate"
	ReasonJevTimeoutFallback    RouteReason = "jev_timeout_fallback"
	ReasonJevUnavailableFallback RouteReason = "jev_unavailable_fallback"
	ReasonUpstreamExhausted     RouteReason = "upstream_exhausted"
)

// Usage mirrors RouteCard.usage.
type Usage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CachedInput int64 `json:"cachedInput"`
}

// Latencies mirrors RouteCard.latenciesMs.
type Latencies struct {
	Jev      int64 `json:"jev"`
	Upstream int64 `json:"upstream"`
	Total    int64 `json:"total"`
}

// RouteCard mirrors types.ts RouteCard — the audit-trail row.
//
// FIELD ORDER MATTERS: the JSONL line is compared byte-for-byte between the
// engines, so struct order matches the TS object literal
// (ts, sessionId, tier, model, upstream, reason, category, complexity,
// confidence, requestTokensEst, usage{input,output,cachedInput}, costUsd,
// latenciesMs{jev,upstream,total}, status, requestedModel, projectedCostUsd).
type RouteCard struct {
	TS              string    `json:"ts"`
	SessionID       string    `json:"sessionId"`
	Tier            TierId    `json:"tier"`
	Model           string    `json:"model"`
	Upstream        UpstreamName `json:"upstream"`
	Reason          RouteReason  `json:"reason"`
	Category        string    `json:"category"`
	Complexity      float64   `json:"complexity"`
	Confidence      float64   `json:"confidence"`
	RequestTokensEst int64   `json:"requestTokensEst"`
	Usage           Usage     `json:"usage"`
	CostUsd         float64   `json:"costUsd"`
	LatenciesMs     Latencies `json:"latenciesMs"`
	Status          int       `json:"status"`
	RequestedModel  string    `json:"requestedModel"`
	ProjectedCostUsd float64  `json:"projectedCostUsd"`
}

// JevAnswer mirrors types.ts JevAnswer.
type JevAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

// JevResponse mirrors types.ts JevResponse.
type JevResponse struct {
	Model   string               `json:"model"`
	Answers map[string]JevAnswer `json:"answers"`
	Usage   JevUsage             `json:"usage"`
}

// JevUsage mirrors types.ts jevUsage.
type JevUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// ClassificationResult mirrors types.ts ClassificationResult.
type ClassificationResult struct {
	Category             string  `json:"category"`
	Complexity           float64 `json:"complexity"`
	ComplexityConfidence float64 `json:"complexityConfidence"`
	CategoryConfidence   float64 `json:"categoryConfidence"`
	TrivialNoul          float64 `json:"trivialNoul"`
	JevModel             string  `json:"jevModel"`
	JevUsage             JevUsage `json:"jevUsage"`
}

// FLUXModelAlias mirrors types.ts FLUX_MODEL_ALIAS.
const FLUXModelAlias = "flux"

// Categories mirrors types.ts CATEGORIES (order preserved; validateConfig
// relies on membership, and config-cli prints this list).
var Categories = []string{
	"math",
	"code_debug",
	"code_implement",
	"code_explain",
	"refactor",
	"greeting_chitchat",
	"summarize",
	"tool_planning",
	"creative_writing",
	"other",
}

// IsKnownCategory mirrors CATEGORIES.includes().
func IsKnownCategory(c string) bool {
	for _, k := range Categories {
		if k == c {
			return true
		}
	}
	return false
}

// DefaultFallbackTier mirrors types.ts DEFAULT_FALLBACK_TIER.
const DefaultFallbackTier = TierFlash