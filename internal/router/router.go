// Package router mirrors old/src/router.ts: glue connecting
// classify → policy → sticky → upstream failover → card log.
package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/abhijeet/fluxrouter/internal/cardlog"
	"github.com/abhijeet/fluxrouter/internal/config"
	"github.com/abhijeet/fluxrouter/internal/cost"
	"github.com/abhijeet/fluxrouter/internal/jev"
	"github.com/abhijeet/fluxrouter/internal/metrics"
	"github.com/abhijeet/fluxrouter/internal/policy"
	"github.com/abhijeet/fluxrouter/internal/session"
	"github.com/abhijeet/fluxrouter/internal/types"
	"github.com/abhijeet/fluxrouter/internal/upstream"
)

// ChatRequest mirrors router.ts ChatRequest.
type ChatRequest struct {
	Messages []ChatMessage
	Stream   bool
	Model    string
	ModelSet bool

	// messagesNotArray marks "messages present but not an array" so
	// ParseChatRequest can return nil exactly like TS Array.isArray check.
	messagesNotArray bool
}

// ChatMessage is the normalized message shape.
type ChatMessage struct {
	Role    string
	Content string
}

// ParseChatRequest mirrors router.ts parseChatRequest (catalog item 9):
//   role: String(m.role ?? "user") — any non-null role stringified, null/undefined → "user"
//   content: typeof === "string" ? content : JSON.stringify(content ?? "")
//     → null/undefined content becomes the 2-char string `""`; numbers,
//       arrays, objects become their JSON text WITH SOURCE KEY ORDER
//       preserved (TS JSON.parse keeps insertion order; Go maps sort keys —
//       the ordered re-stringify in json_order.go prevents excerpt/hash
//       divergence).
func ParseChatRequest(rawBody []byte) *ChatRequest {
	reader := newOrderedReader(rawBody)
	top, err := reader.readValue()
	if err != nil {
		return nil
	}
	bm, ok := top.([]orderedPair)
	if !ok {
		return nil
	}
	var rawMessages []any
	req := &ChatRequest{}
	for _, p := range bm {
		switch p.Key {
		case "stream":
			req.Stream = p.Value == true
		case "model":
			if s, ok := p.Value.(string); ok {
				req.Model = s
				req.ModelSet = true
			}
		case "messages":
			if arr, ok := p.Value.([]any); ok {
				rawMessages = arr
			} else {
				rawMessages = []any{} // marker: present but not array
				req.messagesNotArray = true
			}
		}
	}
	if req.messagesNotArray || rawMessages == nil {
		return nil // TS: !body || !Array.isArray(body.messages) => null
	}
	for _, rm := range rawMessages {
		pairs, _ := rm.([]orderedPair)
		msg := ChatMessage{Role: "user"}
		contentSeen := false
		for _, p := range pairs {
			switch p.Key {
			case "role":
				switch r := p.Value.(type) {
				case string:
					msg.Role = r
				case nil:
					msg.Role = "user" // String(undefined ?? "user")
				default:
					msg.Role = jsToStringValue(r)
				}
			case "content":
				contentSeen = true
				if s, ok := p.Value.(string); ok {
					msg.Content = s
				} else if p.Value == nil {
					msg.Content = `""` // JSON.stringify(null ?? "") === '""'
				} else {
					var buf bytes.Buffer
					if err := encodeOrdered(&buf, p.Value); err == nil {
						msg.Content = buf.String()
					} else {
						msg.Content = `""`
					}
				}
			}
		}
		if !contentSeen {
			msg.Content = `""` // JSON.stringify(undefined ?? "") === '""'
		}
		req.Messages = append(req.Messages, msg)
	}
	return req
}

// jsToStringValue mirrors TS String(v) for JSON-decoded values.
func jsToStringValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case json.Number:
		return t.String()
	case nil:
		return "null"
	default:
		var buf bytes.Buffer
		if err := encodeOrdered(&buf, t); err == nil {
			return buf.String()
		}
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// jevMessage converts to the jev package shape.
func jevMessages(ms []ChatMessage) []jev.Message {
	out := make([]jev.Message, len(ms))
	for i, m := range ms {
		out[i] = jev.Message{Role: m.Role, Content: m.Content}
	}
	return out
}

// costMessages converts to the cost package shape.
func costMessages(ms []ChatMessage) []cost.Message {
	out := make([]cost.Message, len(ms))
	for i, m := range ms {
		out[i] = cost.Message{Content: m.Content}
	}
	return out
}

// sessionMessages converts to the session package shape.
func sessionMessages(ms []ChatMessage) []session.Message {
	out := make([]session.Message, len(ms))
	for i, m := range ms {
		out[i] = session.Message{Role: m.Role, Content: m.Content}
	}
	return out
}

// Deps mirrors router.ts RouterDeps.
type Deps struct {
	Config    *config.Config
	Upstreams *upstream.Client
	Creds     upstream.Credentials
	CardLog   *cardlog.CardLog
	Metrics   *metrics.Metrics
	Sessions  *session.Store
}

// Service mirrors router.ts RouterService.
type Service struct {
	deps     Deps
	sessions *session.Store
	ownsStore bool
}

// NewService mirrors RouterService constructor.
func NewService(deps Deps) *Service {
	s := &Service{deps: deps}
	if deps.Sessions != nil {
		s.sessions = deps.Sessions
	} else {
		s.sessions = session.NewStore()
		s.ownsStore = true
	}
	return s
}

// Dispose releases the session store if owned.
func (s *Service) Dispose() {
	if s.ownsStore {
		s.sessions.Dispose()
	}
}

func (s *Service) jevOptions() jev.Options {
	cfg := s.deps.Config
	return jev.Options{
		BaseURL:   cfg.Jev.BaseURL,
		Model:     cfg.Jev.Model,
		TimeoutMs: cfg.Jev.TimeoutMs,
		APIKey:    env0("TYPESAFE_API_KEY"),
	}
}

func (s *Service) policyConfig() policy.PolicyConfig {
	cfg := s.deps.Config
	return policy.PolicyConfig{
		MinConfidence:               cfg.Jev.MinConfidence,
		TrivialNoul:                 cfg.Jev.TrivialNoul,
		EscalateOnlyAboveComplexity: cfg.Jev.EscalateOnlyAboveComplexity,
		ComplexityToTier:            cfg.Policy.Default.ComplexityToTier,
		Overrides:                   overridesOf(cfg.Policy.Overrides),
		PerRequestCapUsd:            cfg.Cost.PerRequestCapUsd,
		StickyEnabled:               cfg.Sticky.Enabled,
		EscapeConfidence:            cfg.Sticky.EscapeConfidence,
	}
}

func overridesOf(ovs []config.ConfigOverride) []policy.Override {
	out := make([]policy.Override, 0, len(ovs))
	for _, ov := range ovs {
		out = append(out, policy.Override{Category: ov.Category, MinComplexity: ov.MinComplexity, Tier: ov.Tier})
	}
	return out
}

// Decide mirrors router.ts decide: classify + policy + sticky escape + pin.
func (s *Service) Decide(ctx context.Context, req *ChatRequest) (types.RouteDecision, error) {
	cfg := s.deps.Config
	requestTokens := cost.EstimateTokens(costMessages(req.Messages))
	sessionID := session.SessionIDFor(sessionMessages(req.Messages))

	var stickyTier *types.TierId
	if cfg.Sticky.Enabled {
		if st := s.sessions.Get(sessionID); st != nil {
			t := st.Tier
			stickyTier = &t
		}
	}

	// Classify every turn (see TS notes on why sticky hits re-classify).
	var cls *types.ClassificationResult
	var jevMs int64
	{
		started := time.Now()
		state := jev.BuildJevState(jevMessages(req.Messages), 2000)
		result, err := jev.Classify(ctx, state, s.jevOptions())
		if err != nil {
			cls = nil
			s.deps.Metrics.Inc("jev_failures_total", 1)
			var te *jev.TimeoutError
			if errors.As(err, &te) {
				s.deps.Metrics.Inc("jev_timeouts_total", 1)
			}
		} else {
			cls = &result
		}
		jevMs = time.Since(started).Milliseconds()
	}

	outcome := policy.RouteRequest(policy.Input{
		Classification: cls,
		RequestTokens:  requestTokens,
		StickyTier:     stickyTier,
		Tiers:          cfg.Tiers,
		FallbackTier:   &cfg.Jev.FallbackTier,
		Config:         s.policyConfig(),
	})

	// Sticky escape: pinned session upgrades when the new turn clearly needs more.
	if cfg.Sticky.Enabled && stickyTier != nil && cls != nil {
		escape := policy.StickyEscape(
			*stickyTier,
			*cls,
			cfg.Policy.Default.ComplexityToTier,
			overridesOf(cfg.Policy.Overrides),
			cfg.Sticky.EscapeConfidence,
		)
		if escape != nil && *escape > *stickyTier {
			outcome.Tier = *escape
			outcome.Reason = types.ReasonStickyEscapeUp
		}
	}

	// Sticky bookkeeping: pins on first turn, refreshes on every later turn.
	if cfg.Sticky.Enabled {
		tierObj := policy.FindTier(cfg.Tiers, outcome.Tier)
		if tierObj != nil && len(tierObj.Models) > 0 {
			m := tierObj.Models[0]
			if stickyTier != nil {
				s.sessions.RePin(sessionID, outcome.Tier, m.ID, string(m.Upstream))
			} else {
				s.sessions.Pin(sessionID, outcome.Tier, m.ID, string(m.Upstream))
			}
		}
	}

	tierObj := policy.FindTier(cfg.Tiers, outcome.Tier)
	if tierObj == nil && len(cfg.Tiers) > 0 {
		tierObj = &cfg.Tiers[0]
	}
	if tierObj == nil || len(tierObj.Models) == 0 {
		return types.RouteDecision{}, fmt.Errorf("no tier %d in ladder", outcome.Tier)
	}
	model := tierObj.Models[0]

	return types.RouteDecision{
		Tier:             outcome.Tier,
		Model:            model.ID,
		Upstream:         model.Upstream,
		Reason:           outcome.Reason,
		Category:         outcome.Category,
		Complexity:       outcome.Complexity,
		Confidence:       outcome.Confidence,
		ProjectedCostUsd: outcome.ProjectedCostUsd,
		JevLatencyMs:     &jevMs,
	}, nil
}

// RouteDecisionResult is what Execute returns to the HTTP layer.
type RouteDecisionResult struct {
	Decision types.RouteDecision
	SessionID string
	Response *upstream.Result
}

// Execute mirrors router.ts execute: route, forward with failover, stream
// back, write the route card. rawBodyJSON is the ORIGINAL request bytes —
// the forwarded body must be a re-stringification with the model patched
// (TS `{...rawBody, model: id}` keeps the original key position).
func (s *Service) Execute(ctx context.Context, rawBodyJSON []byte, req *ChatRequest, requestedModel string, startTs time.Time) (*ExecuteResult, error) {
	cfg := s.deps.Config
	sessionID := session.SessionIDFor(sessionMessages(req.Messages))
	decision, err := s.Decide(ctx, req)
	if err != nil {
		return nil, err
	}
	jevMs := int64(0)
	if decision.JevLatencyMs != nil {
		jevMs = *decision.JevLatencyMs
	}
	tier := policy.FindTier(cfg.Tiers, decision.Tier)
	if tier == nil {
		return nil, fmt.Errorf("tier %d vanished", decision.Tier)
	}
	startedUpstream := time.Now()
	attempt := s.forwardWithFailover(ctx, tier, req, rawBodyJSON)
	upstreamMs := time.Since(startedUpstream).Milliseconds()
	totalMs := upstreamMs
	if !startTs.IsZero() {
		totalMs = time.Since(startTs).Milliseconds()
	}

	usage := types.Usage{}
	if attempt.JSON != nil {
		usage = ExtractUsage(attempt.JSON)
	}

	model := tier.Models[0]
	card := &types.RouteCard{
		TS:               session.FormatISO(startTs),
		SessionID:        sessionID,
		Tier:             decision.Tier,
		Model:            decision.Model,
		Upstream:         decision.Upstream,
		Reason:           decision.Reason,
		Category:         decision.Category,
		Complexity:       decision.Complexity,
		Confidence:       decision.Confidence,
		RequestTokensEst: cost.EstimateTokens(costMessages(req.Messages)),
		Usage:            usage,
		CostUsd:          CostFromUsage(usage, model),
		LatenciesMs:      types.Latencies{Jev: jevMs, Upstream: upstreamMs, Total: totalMs},
		Status:           attempt.Status,
		RequestedModel:   requestedModel,
		ProjectedCostUsd: decision.ProjectedCostUsd,
	}
	_ = s.deps.CardLog.Log(card)
	s.deps.Metrics.Inc("requests_total", 1)
	s.deps.Metrics.Inc(fmt.Sprintf("tier_%d_total", decision.Tier), 1)
	s.deps.Metrics.Inc("cost_usd_total_micro", float64(int64(card.CostUsd*1e6)))
	s.deps.Metrics.Observe("upstream_latency_ms", float64(upstreamMs))

	return &ExecuteResult{
		Card:      card,
		SessionID: sessionID,
		Attempt:   attempt,
		Decision:  decision,
		Model:     model,
	}, nil
}

// ExecuteResult feeds the HTTP layer.
type ExecuteResult struct {
	Card      *types.RouteCard
	SessionID string
	Attempt   *upstream.Result
	Decision  types.RouteDecision
	Model     types.TierModel
}

// ForwardWithFailover mirrors router.ts forwardWithFailover (exported for the
// parity harness): primary tier models in order → next-tier-up models.
func (s *Service) ForwardWithFailover(ctx context.Context, tier *types.Tier, req *ChatRequest, rawBodyJSON []byte) *upstream.Result {
	return s.forwardWithFailover(ctx, tier, req, rawBodyJSON)
}

func (s *Service) forwardWithFailover(ctx context.Context, tier *types.Tier, req *ChatRequest, rawBodyJSON []byte) *upstream.Result {
	cfg := s.deps.Config
	type attempt struct {
		model types.TierModel
	}
	var attempts []attempt
	for _, m := range tier.Models {
		attempts = append(attempts, attempt{model: m})
	}
	tIdx := -1
	for i := range cfg.Tiers {
		if cfg.Tiers[i].ID == tier.ID {
			tIdx = i
			break
		}
	}
	if tIdx >= 0 && tIdx+1 < len(cfg.Tiers) {
		for _, m := range cfg.Tiers[tIdx+1].Models {
			attempts = append(attempts, attempt{model: m})
		}
	}

	lastStatus := 500
	var lastJSON any
	for _, a := range attempts {
		var key string
		var ep upstream.Endpoint
		if a.model.Upstream == types.UpstreamOllama {
			ep = s.deps.Creds.Ollama
			key = s.deps.Creds.Ollama.APIKey
		} else {
			ep = s.deps.Creds.OpenRouter
			key = s.deps.Creds.OpenRouter.APIKey
		}
		// Skip lanes whose key is absent (optional OpenRouter not configured).
		if key == "" {
			continue
		}
		// TS: { ...rawBody, model: id } — the model key KEEPS ITS ORIGINAL
		// POSITION if present, else lands at the end. Re-stringify ordered
		// and patch in place.
		forwarded := patchModelInBody(rawBodyJSON, a.model.ID)
		res, err := s.deps.Upstreams.Forward(ctx, ep, string(a.model.Upstream), "/chat/completions", upstream.ForwardOptions{
			Body:   forwarded,
			Stream: req.Stream,
		})
		if err != nil {
			lastStatus = 502
			lastJSON = map[string]any{"error": map[string]any{"message": err.Error()}}
			continue
		}
		if res.Status >= 200 && res.Status < 300 {
			return res
		}
		lastStatus = res.Status
		lastJSON = res.JSON
		// 429: no blind retry, surface via ladder. Other 4xx (except
		// 429/408): likely request problem — surface immediately.
		if res.Status >= 400 && res.Status < 500 && res.Status != 429 && res.Status != 408 {
			break
		}
	}
	if lastJSON == nil {
		lastJSON = map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("FluxRouter: all upstreams exhausted for tier %d", tier.ID),
				"type":    "fluxrouter_upstream_exhausted",
			},
		}
	}
	return &upstream.Result{Status: lastStatus, Headers: map[string]string{}, Body: nil, JSON: lastJSON}
}

// ExtractUsage mirrors router.ts extractUsage: OpenAI usage shape.
func ExtractUsage(jsonBody any) types.Usage {
	m, ok := jsonBody.(map[string]any)
	if !ok {
		return types.Usage{}
	}
	um, ok := m["usage"].(map[string]any)
	if !ok {
		return types.Usage{}
	}
	in := numOr0(um["prompt_tokens"])
	out := numOr0(um["completion_tokens"])
	cached := int64(0)
	if dm, ok := um["prompt_tokens_details"].(map[string]any); ok {
		cached = numOr0(dm["cached_tokens"])
	}
	return types.Usage{Input: in, Output: out, CachedInput: cached}
}

func numOr0(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

// CostFromUsage mirrors router.ts costFromUsage:
// (input - cached)*in + cached*cachedIn + output*out, per million.
func CostFromUsage(usage types.Usage, model types.TierModel) float64 {
	cachedIn := 0.0
	if model.HasCachedIn() {
		cachedIn = model.CachedIn
	}
	in := model.In
	if usage.CachedInput > 0 && model.HasCachedIn() {
		uncached := float64(usage.Input - usage.CachedInput)
		if uncached < 0 {
			uncached = 0
		}
		return (uncached/1e6)*in +
			(float64(usage.CachedInput)/1e6)*cachedIn +
			(float64(usage.Output)/1e6)*model.Out
	}
	return (float64(usage.Input)/1e6)*in + (float64(usage.Output)/1e6)*model.Out
}

// env0 reads an env var ("" when missing) — isolated for test injection.
func env0(key string) string { return os.Getenv(key) }