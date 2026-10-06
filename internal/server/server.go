// Package server mirrors old/src/index.ts: the HTTP layer. Auth middleware,
// four routes, deliberate header rebuild (catalog item 12), SSE pass-through.
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/abhijeet/fluxrouter/internal/cardlog"
	"github.com/abhijeet/fluxrouter/internal/config"
	"github.com/abhijeet/fluxrouter/internal/env"
	"github.com/abhijeet/fluxrouter/internal/metrics"
	"github.com/abhijeet/fluxrouter/internal/models"
	"github.com/abhijeet/fluxrouter/internal/router"
	"github.com/abhijeet/fluxrouter/internal/types"
	"github.com/abhijeet/fluxrouter/internal/upstream"
)

// stripHeaders mirrors router.ts STRIPPED — upstream hop headers never pass
// through because Go (like undici) auto-decodes the body.
var stripHeaders = map[string]bool{
	"content-length":      true,
	"content-encoding":    true,
	"transfer-encoding":   true,
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"upgrade":             true,
}

// errorBody mirrors the TS c.json shapes.
func errorBody(message, errType string) map[string]any {
	return map[string]any{"error": map[string]any{"message": message, "type": errType}}
}

// writeJSON writes a JSON body with status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Handler wraps the router service for HTTP serving.
type Handler struct {
	Service *router.Service
	Cfg     *config.Config
	Metrics *metrics.Metrics
	Token   string // FLUX_AUTH_TOKEN ("" = auth disabled)
}

// buildResponseHeaders mirrors router.ts execute's header rebuild, then adds
// the X-Flux-Route-* decision headers.
func buildResponseHeaders(attempt *upstream.Result, decision types.RouteDecision, sessionID string) http.Header {
	h := http.Header{}
	for k, v := range attempt.Headers {
		if stripHeaders[strings.ToLower(k)] {
			continue
		}
		h.Set(k, v)
	}
	h.Set("X-Flux-Route-Tier", strconvItoa(int(decision.Tier)))
	h.Set("X-Flux-Route-Model", decision.Model)
	h.Set("X-Flux-Route-Reason", string(decision.Reason))
	h.Set("X-Flux-Session", sessionID)
	return h
}

func strconvItoa(n int) string { return fmt.Sprintf("%d", n) }

// ServeHTTP implements the mux.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Auth middleware (mirrors index.ts app.use).
	if h.Token != "" {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+h.Token {
			writeJSON(w, 401, errorBody("Unauthorized", "fluxrouter_auth"))
			return
		}
	}

	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/v1/models":
		writeJSON(w, 200, models.BuildResponse(*h.Cfg))
	case r.Method == http.MethodGet && path == "/metrics":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, h.Metrics.Snapshot())
	case r.Method == http.MethodGet && path == "/health":
		writeJSON(w, 200, map[string]any{"ok": true})
	case r.Method == http.MethodPost && path == "/v1/chat/completions":
		h.handleChat(w, r)
	default:
		// Hono returns JSON 404; keep the shape.
		writeJSON(w, 404, map[string]any{"error": map[string]any{"message": "Not Found", "type": "fluxrouter_not_found"}})
	}
}

func (h *Handler) handleChat(w http.ResponseWriter, r *http.Request) {
	h.Metrics.Inc("chat_requests_total", 1)
	startTs := time.Now()

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, 400, errorBody("Invalid JSON body", "fluxrouter_bad_request"))
		return
	}
	var body map[string]any
	// TS c.req.json() would throw on empty body too — both map to 400.
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, 400, errorBody("Invalid JSON body", "fluxrouter_bad_request"))
		return
	}
	req := router.ParseChatRequest(body)
	if req == nil {
		writeJSON(w, 400, errorBody("messages[] is required", "fluxrouter_bad_request"))
		return
	}

	exec, err := h.Service.Execute(r.Context(), body, req, req.Model, startTs)
	if err != nil {
		h.Metrics.Inc("server_errors_total", 1)
		writeJSON(w, 500, errorBody(err.Error(), "fluxrouter_internal"))
		return
	}
	attempt := exec.Attempt
	headers := buildResponseHeaders(attempt, exec.Decision, exec.SessionID)

	if attempt.Body != nil {
		// Streaming: copy chunk-by-chunk, flushing immediately (SSE parity —
		// chunks must reach the client as upstream produces them).
		defer attempt.Body.Close()
		for k, vs := range headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		// TS sets Content-Type only for buffered JSON; streams keep the
		// upstream's content-type (already in attempt.Headers).
		w.WriteHeader(attempt.Status)
		buf := make([]byte, 32*1024)
		flusher, _ := w.(http.Flusher)
		for {
			n, err := attempt.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if err != nil {
				return
			}
		}
	}
	// Buffered JSON path.
	payload := attempt.JSON
	if payload == nil {
		payload = map[string]any{}
	}
	headers.Set("Content-Type", "application/json")
	for k, vs := range headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(attempt.Status)
	_ = json.NewEncoder(w).Encode(payload)
}

// Run mirrors index.ts main: env → config → boot checks → serve.
func Run(argv []string, version string) int {
	opts := parseOpts(argv)
	dotenv := env.Load(opts.envFile)
	if len(dotenv.Loaded) > 0 {
		fmt.Printf("Loaded env file(s): %s (real environment variables take precedence)\n", strings.Join(dotenv.Loaded, ", "))
	}
	cfgPath := opts.config
	if cfgPath == "" {
		cfgPath = tryFindConfig()
	}
	loadRes := config.LoadConfig(cfgPath)
	if len(loadRes.Errors) > 0 {
		fmt.Fprintln(os.Stderr, "FluxRouter config errors:")
		for _, e := range loadRes.Errors {
			fmt.Fprintln(os.Stderr, "  - "+e)
		}
		return 2
	}
	cfg := loadRes.Config
	if opts.port != 0 {
		cfg.Server.Port = opts.port
	}
	if opts.host != "" {
		cfg.Server.Host = opts.host
	}

	if cfg.Server.Host != "127.0.0.1" && os.Getenv("FLUX_AUTH_TOKEN") == "" {
		fmt.Fprintln(os.Stderr, "Refusing to bind non-loopback host without FLUX_AUTH_TOKEN set (see ADR / README security section).")
		return 2
	}

	missing := missingKeys(cfg)
	if len(missing) > 0 {
		fmt.Fprintln(os.Stderr, "Missing API keys in environment:")
		for _, k := range missing {
			fmt.Fprintln(os.Stderr, "  - "+k)
		}
		fmt.Fprintln(os.Stderr, "Set them in your shell, or copy .env.example to .env and fill it in.")
		return 2
	}
	// Optional lanes: warn but do not block boot.
	if os.Getenv(cfg.Upstreams.OpenRouter.ApiKeyEnv) == "" {
		fmt.Fprintf(os.Stderr, "warning: optional key(s) not set: %s\n", cfg.Upstreams.OpenRouter.ApiKeyEnv)
		fmt.Fprintln(os.Stderr, "  OpenRouter failover/burst lane is disabled; failover will use Ollama tiers only.")
	}

	cardLog, err := cardlog.New(cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cardlog init failed:", err)
		return 1
	}
	m := metrics.New()
	creds := upstream.Credentials{
		Ollama:     upstream.Endpoint{BaseURL: cfg.Upstreams.Ollama.BaseURL, APIKey: os.Getenv(cfg.Upstreams.Ollama.ApiKeyEnv)},
		OpenRouter: upstream.Endpoint{BaseURL: cfg.Upstreams.OpenRouter.BaseURL, APIKey: os.Getenv(cfg.Upstreams.OpenRouter.ApiKeyEnv)},
	}
	upClient := upstream.NewClient()
	svc := router.NewService(router.Deps{
		Config:    &cfg,
		Upstreams: upClient,
		Creds:     creds,
		CardLog:   cardLog,
		Metrics:   m,
	})
	defer svc.Dispose()
	defer upClient.CloseAll()

	handler := &Handler{Service: svc, Cfg: &cfg, Metrics: m, Token: os.Getenv("FLUX_AUTH_TOKEN")}

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{Addr: addr, Handler: handler}

	fmt.Printf("FluxRouter v%s listening on http://%s\n", version, addr)
	fmt.Printf("  OpenAI base URL: http://%s/v1\n", addr)
	fmt.Printf("  model alias: %s (routing by Jev classification)\n", types.FLUXModelAlias)
	fmt.Printf("  route cards: %s/route-cards.{jsonl,db}\n", cfg.DataDir)

	// Graceful shutdown mirrors index.ts: SIGINT/SIGTERM -> close.
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sigCh:
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "server error:", err)
			svc.Dispose()
			_ = cardLog.Close()
			return 1
		}
		return 0
	}
	fmt.Println("\nShutting down…")
	_ = srv.Close()
	svc.Dispose()
	_ = cardLog.Close()
	return 0
}

type cliOptions struct {
	config   string
	port     int64
	host     string
	envFile  string
}

func parseOpts(argv []string) cliOptions {
	var o cliOptions
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch a {
		case "--config", "-c":
			if i+1 < len(argv) {
				i++
				o.config = argv[i]
			}
		case "--port", "-p":
			if i+1 < len(argv) {
				i++
				if n, err := jsonNumber(argv[i]); err == nil {
					o.port = n
				}
			}
		case "--host":
			if i+1 < len(argv) {
				i++
				o.host = argv[i]
			}
		case "--env-file":
			if i+1 < len(argv) {
				i++
				o.envFile = argv[i]
			}
		}
	}
	return o
}

func jsonNumber(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

func tryFindConfig() string {
	for _, c := range []string{"fluxrouter.config.json", "config/fluxrouter.config.json"} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func missingKeys(cfg config.Config) []string {
	return []string{cfg.Upstreams.Ollama.ApiKeyEnv, "TYPESAFE_API_KEY"}
}