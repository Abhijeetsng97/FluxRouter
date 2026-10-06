// E2E binary test harness: stub Ollama + stub Jev, boots the real Go binary,
// fires real requests. Run: go run ./internal/e2e (or `go test ./internal/e2e/`).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "E2E FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("E2E PASS")
}

func run() error {
	dir, _ := os.MkdirTemp("", "flux-e2e")
	defer os.RemoveAll(dir)

	// Stub upstream: OpenAI-shaped 200 with usage.
	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"up-1","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"stub reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":0}}}`, model)
	}))
	defer upSrv.Close()

	// Stub Jev: complexity from the last message marker.
	jevSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		state, _ := body["state"].(string)
		complexity := 0.1
		if strings.Contains(state, "HARD") {
			complexity = 1.9
		}
		fmt.Fprintf(w, `{"model":"jev-1.13.0","answers":{"category":{"type":"choice","choice":"other","confidence":0.9},"complexity":{"type":"score","score":%s,"confidence":0.9},"is_trivial":{"type":"noul","noul":0.01}},"usage":{"input_tokens":50,"output_tokens":10}}`, jsonNum(complexity))
	}))
	defer jevSrv.Close()

	// Build config pointing at the stubs.
	cfg := map[string]any{
		"jev":      map[string]any{"baseUrl": jevSrv.URL},
		"upstreams": map[string]any{"ollama": map[string]any{"baseUrl": upSrv.URL}},
		"server":   map[string]any{"port": 18787},
		"dataDir":  dir,
	}
	cfgJSON, _ := json.MarshalIndent(cfg, "", " ")
	cfgPath := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(cfgPath, cfgJSON, 0o644); err != nil {
		return err
	}

	// Environment with fake keys.
	env := append(os.Environ(),
		"OLLAMA_API_KEY=test-ollama-key",
		"TYPESAFE_API_KEY=test-typesafe-key",
	)

	// Start the binary.
	bin := filepath.Join(dir, "fluxrouter.exe")
	build := exec.Command("go", "build", "-o", bin, "./cmd/fluxrouter")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "serve", "--config", cfgPath, "--port", "18787")
	cmd.Env = env
	cmd.Dir = "."
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	// Wait for boot.
	base := "http://127.0.0.1:18787"
	ready := false
	for i := 0; i < 50; i++ {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		return fmt.Errorf("server never became healthy")
	}

	// 1. Health.
	resp, err := http.Get(base + "/health")
	if err != nil {
		return err
	}
	var hb map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&hb)
	resp.Body.Close()
	if hb["ok"] != true {
		return fmt.Errorf("health body = %v", hb)
	}

	// 2. Models alias.
	resp2, _ := http.Get(base + "/v1/models")
	var mb struct {
		Data []struct { ID string `json:"id"` } `json:"data"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&mb)
	resp2.Body.Close()
	if len(mb.Data) != 1 || mb.Data[0].ID != "flux" {
		return fmt.Errorf("models = %+v", mb.Data)
	}

	// 3. Chat: easy prompt -> tier 0.
	chat := func(prompt string) (int, http.Header, map[string]any) {
		payload, _ := json.Marshal(map[string]any{
			"model": "flux",
			"messages": []map[string]any{{"role": "user", "content": prompt}},
		})
		resp, err := http.Post(base+"/v1/chat/completions", "application/json", bytes.NewReader(payload))
		if err != nil {
			return -1, nil, nil
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, resp.Header, body
	}
	status, hdr, body := chat("hello there")
	if status != 200 {
		return fmt.Errorf("easy chat status = %d: %v", status, body)
	}
	if got := hdr.Get("X-Flux-Route-Tier"); got != "0" {
		return fmt.Errorf("easy prompt routed to tier %q, want 0", got)
	}
	if got := hdr.Get("X-Flux-Route-Model"); got != "nemotron-3-nano:30b" {
		return fmt.Errorf("easy prompt model = %q", got)
	}
	if got := hdr.Get("X-Flux-Route-Reason"); got != "policy" {
		return fmt.Errorf("easy prompt reason = %q", got)
	}

	// Hard prompt -> tier 2 (mark via HARD keyword).
	_, hdr2, _ := chat("HARD: prove P=NP with full formal detail")
	if got := hdr2.Get("X-Flux-Route-Tier"); got != "2" {
		return fmt.Errorf("hard prompt tier = %q, want 2", got)
	}

	// 4. Metrics show requests.
	resp3, _ := http.Get(base + "/metrics")
	met := make([]byte, 4096)
	n, _ := resp3.Body.Read(met)
	resp3.Body.Close()
	if !strings.Contains(string(met[:n]), "fluxrouter_requests_total 2") {
		return fmt.Errorf("metrics missing requests_total 2:\n%s", string(met[:n]))
	}

	// 5. Route cards on disk: 2 cards.
	cards, err := os.ReadFile(filepath.Join(dir, "route-cards.jsonl"))
	if err != nil {
		return fmt.Errorf("route cards missing: %w", err)
	}
	lines := strings.Count(strings.TrimSpace(string(cards)), "\n") + 1
	if lines != 2 {
		return fmt.Errorf("expected 2 cards, got %d", lines)
	}
	var card1 map[string]any
	if err := json.Unmarshal([]byte(strings.Split(strings.TrimSpace(string(cards)), "\n")[0]), &card1); err != nil {
		return err
	}
	if card1["model"] != "nemotron-3-nano:30b" || card1["status"] != float64(200) {
		return fmt.Errorf("card fields wrong: %v", card1)
	}
	if card1["reason"] != "policy" {
		return fmt.Errorf("card reason = %v", card1["reason"])
	}
	return nil
}

func jsonNum(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}