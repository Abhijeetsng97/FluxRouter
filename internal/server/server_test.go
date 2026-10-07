// Server contract tests (R3 ring): boot the real handler, assert auth, error
// shapes, /v1/models, /metrics, and the full chat path including route-card
// writing — with a stub Jev and stub upstream.
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abhijeet/fluxrouter/internal/config"
	"github.com/abhijeet/fluxrouter/internal/jev"
	"github.com/abhijeet/fluxrouter/internal/router"
	"github.com/abhijeet/fluxrouter/internal/telemetry"
	"github.com/abhijeet/fluxrouter/internal/upstream"
)

// stubJevAnswer is the canned Jev JSON the stub server returns.
func stubJevJSON(complexity float64, noul float64) string {
	return `{"model":"jev-1.13.0","answers":{` +
		`"category":{"type":"choice","choice":"other","confidence":0.9},` +
		`"complexity":{"type":"score","score":` + jsonF(complexity) + `,"confidence":0.9},` +
		`"is_trivial":{"type":"noul","noul":` + jsonF(noul) + `}},` +
		`"usage":{"input_tokens":10,"output_tokens":5}}`
}

func jsonF(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// setup builds a Handler with stub Jev + stub upstream servers and a temp
// data dir. Returns the handler and the stub upstream's model capture.
func setup(t *testing.T, jevBody string, upstreamStatus int, upstreamBody string) (*Handler, *[]string, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()

	var capturedModels []string
	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if m, ok := body["model"].(string); ok {
			capturedModels = append(capturedModels, m)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(upstreamStatus)
		_, _ = w.Write([]byte(upstreamBody))
	}))
	t.Cleanup(upSrv.Close)

	jevSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jevBody))
	}))
	t.Cleanup(jevSrv.Close)

	cfg := config.DefaultConfig()
	cfg.DataDir = dir
	cfg.Upstreams.Ollama.BaseURL = upSrv.URL
	cfg.Jev.BaseURL = jevSrv.URL

	cl, err := telemetry.NewCardLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })

	m := telemetry.NewMetrics()
	creds := upstream.Credentials{
		Ollama:     upstream.Endpoint{BaseURL: upSrv.URL, APIKey: "test-key"},
		OpenRouter: upstream.Endpoint{BaseURL: upSrv.URL, APIKey: ""},
	}
	svc := router.NewService(router.Deps{
		Config:    &cfg,
		Upstreams: upstream.NewClient(),
		Creds:     creds,
		CardLog:   cl,
		Metrics:   m,
	})
	t.Cleanup(svc.Dispose)

	return &Handler{Service: svc, Cfg: &cfg, Metrics: m, Token: ""}, &capturedModels, jevSrv
}

func TestHealthAndModels(t *testing.T) {
	h, _, _ := setup(t, stubJevJSON(0.1, 0.01), 200, `{"ok":true}`)

	r := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("health = %d %s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest("GET", "/v1/models", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != "flux" {
		t.Fatalf("models = %+v, want single flux alias", resp.Data)
	}
}

func TestAuthRequiredWhenTokenSet(t *testing.T) {
	h, _, _ := setup(t, stubJevJSON(0.1, 0.01), 200, `{}`)
	h.Token = "secret"

	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("no-auth status = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "fluxrouter_auth") {
		t.Fatalf("auth error shape wrong: %s", w.Body.String())
	}

	r = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
	r.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == 401 {
		t.Fatal("correct token rejected")
	}
}

func TestChatBadRequestShapes(t *testing.T) {
	h, _, _ := setup(t, stubJevJSON(0.1, 0.01), 200, `{}`)

	// Invalid JSON.
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{broken`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "fluxrouter_bad_request") {
		t.Fatalf("invalid json: %d %s", w.Code, w.Body.String())
	}

	// Missing messages[].
	r = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"nope":1}`))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "messages[] is required") {
		t.Fatalf("missing messages: %d %s", w.Code, w.Body.String())
	}
}

func TestChatHappyPathWritesCardAndHeaders(t *testing.T) {
	hiJev := stubJevJSON(0.1, 0.01) // trivial-ish: noul 0.01 low, other+0.1 → tier 0 via band
	h, captured, _ := setup(t, hiJev, 200, `{"id":"1","choices":[{"message":{"content":"yo"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)

	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"flux","messages":[{"role":"user","content":"hello there"}]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	// Decision headers present.
	if got := w.Header().Get("X-Flux-Route-Tier"); got == "" {
		t.Fatal("missing X-Flux-Route-Tier")
	}
	if got := w.Header().Get("X-Flux-Route-Model"); got == "" {
		t.Fatal("missing X-Flux-Route-Model")
	}
	if got := w.Header().Get("X-Flux-Session"); len(got) != 24 {
		t.Fatalf("X-Flux-Session = %q, want 24 chars", got)
	}
	// Upstream got the tier model id, not "flux".
	if len(*captured) != 1 || (*captured)[0] != "nemotron-3-nano:30b" {
		t.Fatalf("upstream models = %v, want [nemotron-3-nano:30b]", *captured)
	}
	// Route card written to BOTH jsonl and sqlite.
	jsonlPath := filepath.Join(h.Cfg.DataDir, "route-cards.jsonl")
	data, err := os.ReadFile(jsonlPath)
	if err != nil || len(data) == 0 {
		t.Fatalf("route-cards.jsonl missing/empty: %v", err)
	}
	var card map[string]any
	if err := json.Unmarshal(data, &card); err != nil {
		t.Fatalf("card json malformed: %v", err)
	}
	for _, key := range []string{"ts", "sessionId", "tier", "model", "upstream", "reason", "category", "complexity", "confidence", "requestTokensEst", "usage", "costUsd", "latenciesMs", "status", "requestedModel", "projectedCostUsd"} {
		if _, ok := card[key]; !ok {
			t.Fatalf("card missing key %q: %s", key, data)
		}
	}
	if card["model"] != "nemotron-3-nano:30b" || card["status"] != float64(200) {
		t.Fatalf("card fields wrong: %s", data)
	}
}

func TestChatFailoverPropagatesUpstreamError(t *testing.T) {
	// TS parity: a 5xx from the primary lane loops through remaining attempts;
	// when none succeed, the LAST upstream error (here: openrouter skipped —
	// no key) propagates verbatim. The "exhausted" message only appears when
	// NO lane was ever attempted (lastJson stays nil).
	h, _, _ := setup(t, stubJevJSON(0.5, 0.01), 500, `{"error":{"message":"upstream down"}}`)

	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"upstream down"`) {
		t.Fatalf("upstream error must propagate verbatim: %s", w.Body.String())
	}
	// Card still written (status 500) — catalog item 13.
	jsonlPath := filepath.Join(h.Cfg.DataDir, "route-cards.jsonl")
	data, err := os.ReadFile(jsonlPath)
	if err != nil || !strings.Contains(string(data), `"status":500`) {
		t.Fatalf("error card must still be logged: %v %s", err, data)
	}
}

func TestChatFailoverExhaustedMessageWhenNoLaneAttempted(t *testing.T) {
	// When ollama AND openrouter keys are both absent, no attempt happens;
	// lastJson stays nil and the exhausted message fires.
	dir := t.TempDir()
	jevSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(stubJevJSON(0.5, 0.01)))
	}))
	defer jevSrv.Close()
	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no upstream request should be made when no keys exist")
	}))
	defer upSrv.Close()

	cfg := config.DefaultConfig()
	cfg.DataDir = dir
	cfg.Upstreams.Ollama.BaseURL = upSrv.URL
	cfg.Jev.BaseURL = jevSrv.URL
	cl, err := telemetry.NewCardLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	m := telemetry.NewMetrics()
	creds := upstream.Credentials{
		Ollama:     upstream.Endpoint{BaseURL: upSrv.URL, APIKey: ""}, // no key
		OpenRouter: upstream.Endpoint{BaseURL: upSrv.URL, APIKey: ""}, // no key
	}
	svc := router.NewService(router.Deps{Config: &cfg, Upstreams: upstream.NewClient(), Creds: creds, CardLog: cl, Metrics: m})
	defer svc.Dispose()
	h := &Handler{Service: svc, Cfg: &cfg, Metrics: m, Token: ""}

	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "FluxRouter: all upstreams exhausted for tier") {
		t.Fatalf("exhausted message wrong: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "fluxrouter_upstream_exhausted") {
		t.Fatalf("error type wrong: %s", w.Body.String())
	}
}

func TestMetricsEndpointFormat(t *testing.T) {
	h, _, _ := setup(t, stubJevJSON(0.1, 0.01), 200, `{}`)
	// Fire one request to populate metrics.
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	r = httptest.NewRequest("GET", "/metrics", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{
		"# HELP fluxrouter_requests_total Total proxied requests",
		"# TYPE fluxrouter_requests_total counter",
		"fluxrouter_requests_total 1",
		"fluxrouter_chat_requests_total 1",
		"fluxrouter_upstream_latency_ms{quantile=\"0.5\"}",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}

var _ = jev.Options{} // silence import if stub removed later