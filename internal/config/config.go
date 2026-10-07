// Package config mirrors old/src/config.ts. TS "types" vanish at runtime â€”
// a FluxConfig there is just a JSON object merged over defaults. So this port
// validates the merged MAP (identical messages, identical order) and then
// compiles it into typed structs. Deep-merge semantics: user wins, objects
// recurse, arrays are replaced wholesale.
package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/abhijeet/fluxrouter/internal/routing"
	"github.com/abhijeet/fluxrouter/internal/types"
)

// ConfigOverride mirrors the TS policy.overrides entries.
type ConfigOverride struct {
	Category      string       `json:"category"`
	MinComplexity *float64     `json:"minComplexity,omitempty"`
	Tier          types.TierId `json:"tier"`
}

// Config mirrors config.ts FluxConfig.
type Config struct {
	Jev struct {
		BaseURL                     string  `json:"baseUrl"`
		Model                       string  `json:"model"`
		TimeoutMs                   int64   `json:"timeoutMs"`
		MinConfidence               float64 `json:"minConfidence"`
		TrivialNoul                 float64 `json:"trivialNoul"`
		EscalateOnlyAboveComplexity float64 `json:"escalateOnlyAboveComplexity"`
		FallbackTier                types.TierId `json:"fallbackTier"`
	} `json:"jev"`
	Upstreams struct {
		Ollama struct {
			BaseURL   string `json:"baseUrl"`
			ApiKeyEnv string `json:"apiKeyEnv"`
		} `json:"ollama"`
		OpenRouter struct {
			BaseURL   string `json:"baseUrl"`
			ApiKeyEnv string `json:"apiKeyEnv"`
		} `json:"openrouter"`
	} `json:"upstreams"`
	Tiers []types.Tier `json:"tiers"`
	Policy struct {
		Default struct {
			ComplexityToTier []routing.Band `json:"complexityToTier"`
		} `json:"default"`
		Overrides []ConfigOverride `json:"overrides"`
	} `json:"policy"`
	Sticky struct {
		Enabled          bool    `json:"enabled"`
		EscapeConfidence float64 `json:"escapeConfidence"`
	} `json:"sticky"`
	Cost struct {
		PerRequestCapUsd       float64  `json:"perRequestCapUsd"`
		OpenrouterMonthlyCapUsd *float64 `json:"openrouterMonthlyCapUsd,omitempty"`
	} `json:"cost"`
	Eval struct {
		RoutingCapUsd float64 `json:"routingCapUsd"`
		E2ECapUsd     float64 `json:"e2eCapUsd"`
		JudgeModel    string  `json:"judgeModel"`
	} `json:"eval"`
	Server struct {
		Port          int64  `json:"port"`
		Host          string `json:"host"`
		ListTierModels bool  `json:"listTierModels"`
	} `json:"server"`
	RespectIncomingModel bool   `json:"respectIncomingModel"`
	DataDir              string `json:"dataDir"`
}

// defaultComplexityToTier mirrors DEFAULT_COMPLEXITY_TO_TIER (strict "<" bands).
var defaultComplexityToTier = []routing.Band{
	{Threshold: 0.8, Tier: 0},
	{Threshold: 1.6, Tier: 1},
	{Threshold: 2.0, Tier: 2},
	{Threshold: 999, Tier: 3},
}

// DefaultConfig mirrors config.ts defaultConfig.
func DefaultConfig() Config {
	var c Config
	c.Jev.BaseURL = "https://api.typesafe.ai/v1/systemone"
	c.Jev.Model = "jev-1.13.0"
	c.Jev.TimeoutMs = 700
	c.Jev.MinConfidence = 0.5
	c.Jev.TrivialNoul = 0.85
	c.Jev.EscalateOnlyAboveComplexity = 0.5
	c.Jev.FallbackTier = types.TierFlash
	c.Upstreams.Ollama.BaseURL = "https://ollama.com/v1"
	c.Upstreams.Ollama.ApiKeyEnv = "OLLAMA_API_KEY"
	c.Upstreams.OpenRouter.BaseURL = "https://openrouter.ai/api/v1"
	c.Upstreams.OpenRouter.ApiKeyEnv = "OPENROUTER_API_KEY"
	c.Tiers = []types.Tier{
		{ID: 0, Name: "nano", Models: []types.TierModel{{
			Upstream: types.UpstreamOllama, ID: "nemotron-3-nano:30b", Ctx: 1_000_000,
			In: 0.06, Out: 0.24, FailoverID: "nvidia/nemotron-3-nano",
		}}},
		{ID: 1, Name: "flash", Models: []types.TierModel{{
			Upstream: types.UpstreamOllama, ID: "glm-5.3-flash", Ctx: 1_000_000,
			In: 0.15, Out: 0.5, FailoverID: "zhipuai/glm-5.3-flash",
		}}},
		{ID: 2, Name: "mid", Models: []types.TierModel{{
			Upstream: types.UpstreamOllama, ID: "deepseek-v4-pro:0813", Ctx: 1_000_000,
			In: 0.66, Out: 1.98, ReasoningEffort: "high", FailoverID: "deepseek/deepseek-v4-pro",
		}}},
		{ID: 3, Name: "frontier", Models: []types.TierModel{{
			Upstream: types.UpstreamOllama, ID: "kimi-k3", Ctx: 1_000_000,
			In: 3.0, Out: 15.0, FailoverID: "moonshotai/kimi-k3",
		}}},
	}
	c.Policy.Default.ComplexityToTier = defaultComplexityToTier
	c.Policy.Overrides = []ConfigOverride{
		{Category: "math", MinComplexity: ptr(1.2), Tier: 2},
		{Category: "greeting_chitchat", Tier: 0},
	}
	c.Sticky.Enabled = true
	c.Sticky.EscapeConfidence = 0.6
	c.Cost.PerRequestCapUsd = 0.25
	c.Eval.RoutingCapUsd = 1
	c.Eval.E2ECapUsd = 10
	c.Eval.JudgeModel = "jev-1.13.0"
	c.Server.Port = 8787
	c.Server.Host = "127.0.0.1"
	c.Server.ListTierModels = false
	c.RespectIncomingModel = false
	c.DataDir = ".fluxrouter"
	return c
}

func ptr(f float64) *float64 { return &f }

// deepMergeMaps mirrors config.ts deepMerge: objects (non-array, non-nil)
// recurse when the target value is also an object; everything else replaces.
func deepMergeMaps(target, src map[string]any) {
	for k, v := range src {
		vm, vIsObj := v.(map[string]any)
		tm, tIsObj := target[k].(map[string]any)
		if vIsObj && tIsObj {
			deepMergeMaps(tm, vm)
		} else {
			target[k] = v
		}
	}
}

// LoadResult mirrors loadConfig's return shape.
type LoadResult struct {
	Config Config
	Errors []string
}

// LoadConfig mirrors config.ts loadConfig. A read failure returns the DEFAULT
// config plus the message (base is NOT merged with the user file) â€” matches TS.
func LoadConfig(path string) LoadResult {
	base := DefaultConfig()
	var user any = map[string]any{}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return LoadResult{Config: base, Errors: []string{fmt.Sprintf("cannot read config file %s: %s", path, readErrMsg(err))}}
		}
		if err := json.Unmarshal(data, &user); err != nil {
			// TS JSON.parse throws with a different message but the same
			// "cannot read config file" shape is NOT used there â€” TS catches
			// the parse error inside the same try, so the message differs.
			// Keep the same wrapper for load failures either way.
			return LoadResult{Config: base, Errors: []string{fmt.Sprintf("cannot read config file %s: %s", path, err.Error())}}
		}
	}
	userMap, _ := user.(map[string]any)
	baseMap := toMap(base)
	deepMergeMaps(baseMap, userMap)
	normalizeNumbers(baseMap)

	if errs := ValidateMap(baseMap); len(errs) > 0 {
		// TS merges and validates the object; on validation failure the CLI
		// still prints errors. loadConfig itself returns merged config+errors.
		cfg, cerr := compileMap(baseMap)
		if cerr != nil {
			return LoadResult{Config: base, Errors: errs}
		}
		return LoadResult{Config: cfg, Errors: errs}
	}
	cfg, cerr := compileMap(baseMap)
	if cerr != nil {
		return LoadResult{Config: base, Errors: []string{cerr.Error()}}
	}
	return LoadResult{Config: cfg}
}

func readErrMsg(err error) string {
	var pe *os.PathError
	if e, ok := err.(*os.PathError); ok {
		pe = e
		return pe.Err.Error()
	}
	return err.Error()
}

// ValidateConfig mirrors validateConfig on a compiled Config (used by tests
// and `flux config validate` against typed configs).
func ValidateConfig(c Config) []string { return ValidateMap(toMap(c)) }

// getNum reads a JSON number that may decode as float64 or (after
// normalizeNumbers) int64 â€” mirrors JS typeof === "number" for both shapes.
func getNum(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	switch v := m[key].(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	}
	return 0, false
}

// ValidateMap mirrors config.ts validateConfig â€” messages VERBATIM, same order.
func ValidateMap(m map[string]any) []string {
	var errors []string
	push := func(s string) { errors = append(errors, s) }

	jev, _ := m["jev"].(map[string]any)
	if jev == nil {
		jev = map[string]any{}
	}
	baseURL, _ := jev["baseUrl"].(string)
	if !strings.HasPrefix(baseURL, "http") {
		push("jev.baseUrl must be http(s)")
	}
	model, _ := jev["model"].(string)
	if model == "" {
		push("jev.model is required")
	}
	timeoutMs, _ := getNum(jev, "timeoutMs")
	if timeoutMs < 50 {
		push("jev.timeoutMs must be >= 50")
	}
	minConfidence, _ := getNum(jev, "minConfidence")
	if !(minConfidence > 0) || !(minConfidence < 1) {
		push("jev.minConfidence must be between 0 and 1 exclusive")
	}
	trivialNoul, _ := getNum(jev, "trivialNoul")
	if !(trivialNoul > 0) || !(trivialNoul < 1) {
		push("jev.trivialNoul must be between 0 and 1 exclusive")
	}
	if fb, ok := getNum(jev, "fallbackTier"); !ok || !isTierIdFloat(fb) {
		push("jev.fallbackTier must be an integer 0..3")
	}

	tiersAny, ok := m["tiers"].([]any)
	if !ok || len(tiersAny) == 0 {
		push("tiers must be a non-empty array")
		tiersAny = nil
	}
	for _, ta := range tiersAny {
		t, _ := ta.(map[string]any)
		if t == nil {
			t = map[string]any{}
		}
		id, idOK := getNum(t, "id")
		if !idOK || !isTierIdFloat(id) {
			push(fmt.Sprintf("tier id %s must be an integer 0..3", jsonString(t["id"])))
			id = -1
		}
		name, _ := t["name"].(string)
		if name == "" {
			push(fmt.Sprintf("tier %s missing name", numForMsg(id, idOK)))
		}
		modelsAny, mOK := t["models"].([]any)
		if !mOK || len(modelsAny) == 0 {
			push(fmt.Sprintf("tier %s must have at least one model", numForMsg(id, idOK)))
		} else {
			for _, ma := range modelsAny {
				mm, _ := ma.(map[string]any)
				if mm == nil {
					mm = map[string]any{}
				}
				upstream, _ := mm["upstream"].(string)
				if upstream != "ollama" && upstream != "openrouter" {
					push(fmt.Sprintf("tier %s model %s: upstream must be \"ollama\" or \"openrouter\"", numForMsg(id, idOK), plainID(mm["id"])))
				}
				mid, _ := mm["id"].(string)
				if mid == "" {
					push(fmt.Sprintf("tier %s model missing id", numForMsg(id, idOK)))
				}
				ctx, _ := getNum(mm, "ctx")
				if !(ctx > 0) {
					push(fmt.Sprintf("tier %s model %s: ctx must be > 0", numForMsg(id, idOK), plainID(mm["id"])))
				}
				in, inOK := getNum(mm, "in")
				out, outOK := getNum(mm, "out")
				if !inOK || in < 0 || !outOK || out < 0 {
					push(fmt.Sprintf("tier %s model %s: rates must be >= 0", numForMsg(id, idOK), plainID(mm["id"])))
				}
			}
		}
	}
	// Contiguity: ids deduped, sorted ascending; each id != 0 needs id-1.
	idsSet := map[float64]bool{}
	for _, ta := range tiersAny {
		t, _ := ta.(map[string]any)
		if t == nil {
			continue
		}
		if id, ok := getNum(t, "id"); ok {
			idsSet[id] = true
		}
	}
	ids := make([]float64, 0, len(idsSet))
	for id := range idsSet {
		ids = append(ids, id)
	}
	sort.Float64s(ids)
	for _, id := range ids {
		if id != 0 && !idsSet[id-1] {
			push(fmt.Sprintf("tier %s exists but tier %s is missing", numOrJson(id), numOrJson(id-1)))
		}
	}

	polAny, _ := m["policy"].(map[string]any)
	var overridesAny []any
	if polAny != nil {
		overridesAny, _ = polAny["overrides"].([]any)
	}
	for _, oa := range overridesAny {
		ov, _ := oa.(map[string]any)
		if ov == nil {
			ov = map[string]any{}
		}
		cat, _ := ov["category"].(string)
		if !types.IsKnownCategory(cat) {
			push(fmt.Sprintf("policy override category %q is not a known category", cat))
		}
		if tier, ok := getNum(ov, "tier"); !ok || !isTierIdFloat(tier) {
			push(fmt.Sprintf("policy override for %s: tier must be 0..3", cat))
		}
	}

	costAny, _ := m["cost"].(map[string]any)
	if costAny == nil {
		costAny = map[string]any{}
	}
	perCap, _ := getNum(costAny, "perRequestCapUsd")
	if !(perCap > 0) {
		push("cost.perRequestCapUsd must be > 0")
	}
	evalAny, _ := m["eval"].(map[string]any)
	if evalAny == nil {
		evalAny = map[string]any{}
	}
	routingCap, _ := getNum(evalAny, "routingCapUsd")
	if !(routingCap > 0) {
		push("eval.routingCapUsd must be > 0")
	}
	e2eCap, _ := getNum(evalAny, "e2eCapUsd")
	if !(e2eCap > 0) {
		push("eval.e2eCapUsd > 0")
	}

	seen := map[string]bool{}
	for _, ta := range tiersAny {
		t, _ := ta.(map[string]any)
		if t == nil {
			continue
		}
		modelsAny, _ := t["models"].([]any)
		for _, ma := range modelsAny {
			mm, _ := ma.(map[string]any)
			if mm == nil {
				continue
			}
			upstream, _ := mm["upstream"].(string)
			mid, _ := mm["id"].(string)
			key := upstream + ":" + mid
			if seen[key] {
				push(fmt.Sprintf("duplicate model across tiers: %s", key))
			}
			seen[key] = true
		}
	}
	return errors
}

func isTierIdFloat(v float64) bool {
	return v >= 0 && v <= 3 && v == math.Trunc(v)
}

// jsonString mirrors JSON.stringify for error messages:
//   - undefined prints as "undefined" (only relevant via numForMsg/nil cases)
//   - strings print JSON-quoted: JSON.stringify("x") === "\"x\""
func jsonString(v any) string {
	if v == nil {
		return "undefined"
	}
	if s, ok := v.(string); ok {
		b, _ := json.Marshal(s)
		return string(b)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// plainID mirrors TS template-literal interpolation of a model id (${m.id}) â€”
// raw string, NO JSON quoting. Used for the `model <id>:` part of messages
// (validateConfig uses m.id directly, JSON.stringify only on tier ids).
func plainID(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return jsonString(v)
}

// rawTemplate mirrors TS template-literal `${v}` interpolation:
// undefined â†’ "undefined", strings print RAW (no quotes), numbers decimal.
func rawTemplate(v any) string {
	switch t := v.(type) {
	case nil:
		return "undefined"
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64, int64, int:
		return numOrJson(toFloat(v))
	default:
		return jsonString(v)
	}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}

func numForMsg(id float64, ok bool) string {
	if !ok {
		// TS prints whatever t.id was via ${t.id} â€” raw semantics.
		return rawTemplate(id)
	}
	return numOrJson(id)
}

func numOrJson(id float64) string {
	if id == math.Trunc(id) && math.Abs(id) < 1e15 {
		return fmt.Sprintf("%d", int64(id))
	}
	return jsonString(id)
}

// toMap marshals the typed defaults into the dynamic merge base. Numbers as
// float64 everywhere (the JS model); normalizeNumbers fixes integral leaves.
func toMap(c Config) map[string]any {
	b, _ := json.Marshal(c)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	normalizeNumbers(m)
	return m
}

// normalizeNumbers converts integral float64 leaves to int64 so typed
// unmarshal of JSON-number blur (1000000 vs 1000000.0) behaves like JS.
func normalizeNumbers(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, vv := range t {
			if f, ok := vv.(float64); ok && f == math.Trunc(f) && math.Abs(f) < 1e15 {
				t[k] = int64(f)
				continue
			}
			normalizeNumbers(vv)
		}
	case []any:
		for i, vv := range t {
			if f, ok := vv.(float64); ok && f == math.Trunc(f) && math.Abs(f) < 1e15 {
				t[i] = int64(f)
				continue
			}
			normalizeNumbers(vv)
		}
	}
}

// compileMap turns the validated merged map into the typed Config.
func compileMap(m map[string]any) (Config, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// HasTier mirrors config.ts hasTier.
func HasTier(tiers []types.Tier, id types.TierId) bool {
	for _, t := range tiers {
		if t.ID == id {
			return true
		}
	}
	return false
}

// PolicyBands returns the default complexityâ†’tier bands.
func PolicyBands() []routing.Band { return defaultComplexityToTier }