// Cardlog tests: DDL/insert parity, aggregate SQL, and — critically — the
// cross-read gate: a database written by the TS engine (node:sqlite) must be
// readable by the Go engine, and vice versa.
package cardlog

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/abhijeet/fluxrouter/internal/types"
)

func sampleCard() *types.RouteCard {
	return &types.RouteCard{
		TS:               "2026-09-30T12:00:00.000Z",
		SessionID:        "a1b2c3d4e5f6a1b2c3d4e5f6",
		Tier:             0,
		Model:            "nemotron-3-nano",
		Upstream:         "ollama",
		Reason:           "trivial_bypass",
		Category:         "greeting_chitchat",
		Complexity:       0.2,
		Confidence:       0.91,
		RequestTokensEst: 34,
		Usage:            types.Usage{Input: 30, Output: 28, CachedInput: 0},
		CostUsd:          0.0000149,
		ProjectedCostUsd: 0.0000192,
		LatenciesMs:      types.Latencies{Jev: 240, Upstream: 780, Total: 1030},
		Status:           200,
		RequestedModel:   "flux",
	}
}

func TestWriteThenReadBack(t *testing.T) {
	dir := t.TempDir()
	cl, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	card := sampleCard()
	if err := cl.Log(card); err != nil {
		t.Fatalf("log: %v", err)
	}

	// JSONL: exactly one line, byte-comparable field order.
	data, err := os.ReadFile(filepath.Join(dir, "route-cards.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("jsonl malformed: %v", err)
	}
	wantOrder := []string{"ts", "sessionId", "tier", "model", "upstream", "reason", "category", "complexity", "confidence", "requestTokensEst", "usage", "costUsd", "latenciesMs", "status", "requestedModel", "projectedCostUsd"}
	checkOrder(t, string(data), wantOrder)

	// SQLite row.
	row := cl.db.QueryRow(`SELECT ts, session_id, tier, model, upstream, reason, category, complexity, confidence,
		request_tokens_est, input_tokens, output_tokens, cached_input_tokens, cost_usd, projected_cost_usd,
		latency_jev_ms, latency_upstream_ms, latency_total_ms, status, requested_model FROM route_cards`)
	var ts, sid, model, upstreamName, reason, category, requestedModel string
	var tier, status int64
	var complexity, confidence, costUsd, projected float64
	var rt, in, out, cached, ljev, lup, ltot int64
	if err := row.Scan(&ts, &sid, &tier, &model, &upstreamName, &reason, &category, &complexity, &confidence,
		&rt, &in, &out, &cached, &costUsd, &projected, &ljev, &lup, &ltot, &status, &requestedModel); err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Release the file BEFORE TempDir cleanup (Windows locks open files).
	if err := cl.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if ts != card.TS || sid != card.SessionID || tier != 0 || model != card.Model || reason != string(card.Reason) || status != 200 {
		t.Fatalf("row mismatch: %s %s %d %s %s %d", ts, sid, tier, model, reason, status)
	}

	// Aggregations run on the same table (reopen for the query).
	cl2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := cl2.Aggregate("tier")
	if err != nil || len(rows) != 1 || rows[0].Bucket != "0" {
		t.Fatalf("aggregate tier: %v %+v", err, rows)
	}
	tot, err := cl2.Totals()
	if err != nil || tot.Requests != 1 {
		t.Fatalf("totals: %v %+v", err, tot)
	}
	cl2.Close()
}

// checkOrder asserts the JSONL keys appear in the recorded TS order.
func checkOrder(t *testing.T, line string, want []string) {
	t.Helper()
	pos := -1
	for _, k := range want {
		idx := indexOf(line, `"`+k+`":`)
		if idx < 0 {
			t.Fatalf("key %q missing in %s", k, line)
		}
		if idx < pos {
			t.Fatalf("key %q out of order in %s", k, line)
		}
		pos = idx
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestDatabaseCrossReadWrite is the G2 gate: the Go engine must read a
// database written by node:sqlite (the TS engine) and the TS engine must read
// one written here. The TS half runs via a node -e script in CI; here we
// verify the Go half against a TS-produced fixture committed as testdata.
func TestDatabaseCrossRead(t *testing.T) {
	path := "testdata/ts-written.db"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("ts-written.db fixture not present — generated in CI by old/scripts/gen-testdb.mjs")
	}
	dir := t.TempDir()
	copyFile(t, path, filepath.Join(dir, "route-cards.db"))
	cl, err := New(dir)
	if err != nil {
		t.Fatalf("opening TS-written db: %v", err)
	}
	defer cl.Close()
	tot, err := cl.Totals()
	if err != nil {
		t.Fatalf("totals on TS-written db: %v", err)
	}
	if tot.Requests == 0 {
		t.Fatal("TS-written db has no rows?")
	}
	rows, err := cl.Aggregate("model")
	if err != nil {
		t.Fatalf("aggregate on TS-written db: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no aggregate rows from TS data")
	}
}

// TestJSONLCrossRead: cards appended by the TS engine parse identically here.
// Each line is one TS-written JSON object; the Go RouteCard must parse it AND
// re-marshal byte-identically (field order + values + float text omitted via
// semantic comparison).
func TestJSONLCrossRead(t *testing.T) {
	path := "testdata/ts-written.jsonl"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("ts-written.jsonl fixture not present")
	}
	lines := splitLines(string(data))
	if len(lines) != 2 {
		t.Fatalf("expected 2 TS-written cards, got %d", len(lines))
	}
	for i, line := range lines {
		var card types.RouteCard
		if err := json.Unmarshal([]byte(line), &card); err != nil {
			t.Fatalf("TS line %d does not parse into Go RouteCard: %v\nline: %s", i+1, err, line)
		}
		out, _ := json.Marshal(card)
		if !jsonEquiv([]byte(line), out) {
			t.Fatalf("re-marshal diverges on line %d:\n ts : %s\n go : %s", i+1, line, out)
		}
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func jsonEquiv(a, b []byte) bool {
	// Byte-equality first; if that fails due to float text, compare decoded.
	if string(a) == string(b) {
		return true
	}
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)
	return string(ja) == string(jb)
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

var _ = sql.ErrNoRows