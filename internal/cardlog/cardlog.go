// Package cardlog mirrors old/src/cardlog.ts: JSONL append + SQLite row per
// request. The DDL, INSERT, and aggregate SQL are contract — the Go binary
// must read AND write databases produced by the TS engine unchanged
// (old databases stay queryable; no migration).
package cardlog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // CGo-free SQLite driver

	"github.com/abhijeet/fluxrouter/internal/types"
)

// CardLog mirrors cardlog.ts CardLog.
type CardLog struct {
	jsonlPath string
	db        *sql.DB
	insert    *sql.Stmt
}

const ddl = `CREATE TABLE IF NOT EXISTS route_cards (
      ts TEXT NOT NULL,
      session_id TEXT NOT NULL,
      tier INTEGER NOT NULL,
      model TEXT NOT NULL,
      upstream TEXT NOT NULL,
      reason TEXT NOT NULL,
      category TEXT NOT NULL,
      complexity REAL NOT NULL,
      confidence REAL NOT NULL,
      request_tokens_est INTEGER NOT NULL,
      input_tokens INTEGER NOT NULL,
      output_tokens INTEGER NOT NULL,
      cached_input_tokens INTEGER NOT NULL DEFAULT 0,
      cost_usd REAL NOT NULL,
      projected_cost_usd REAL NOT NULL,
      latency_jev_ms INTEGER NOT NULL,
      latency_upstream_ms INTEGER NOT NULL,
      latency_total_ms INTEGER NOT NULL,
      status INTEGER NOT NULL,
      requested_model TEXT
    );
    CREATE INDEX IF NOT EXISTS idx_rc_ts ON route_cards(ts);
    CREATE INDEX IF NOT EXISTS idx_rc_session ON route_cards(session_id);
    CREATE INDEX IF NOT EXISTS idx_rc_tier ON route_cards(tier);`

const insertSQL = `INSERT INTO route_cards
      (ts, session_id, tier, model, upstream, reason, category, complexity, confidence,
       request_tokens_est, input_tokens, output_tokens, cached_input_tokens, cost_usd,
       projected_cost_usd, latency_jev_ms, latency_upstream_ms, latency_total_ms, status, requested_model)
      VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// New mirrors cardlog.ts constructor: mkdir, DDL, prepared insert.
func New(dataDir string) (*CardLog, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	jsonlPath := filepath.Join(dataDir, "route-cards.jsonl")
	dbPath := filepath.Join(dataDir, "route-cards.db")
	// modernc/sqlite needs explicit busy timeout for concurrent CLI+server use.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, err
	}
	stmt, err := db.Prepare(insertSQL)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &CardLog{jsonlPath: jsonlPath, db: db, insert: stmt}, nil
}

// Log mirrors cardlog.ts log: append JSONL (single line) then SQLite row.
// JSONL field order comes from the RouteCard struct order — byte-parity with TS.
func (c *CardLog) Log(card *types.RouteCard) error {
	line, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal route card: %w", err)
	}
	f, err := os.OpenFile(c.jsonlPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	_, err = c.insert.Exec(
		card.TS,
		card.SessionID,
		int64(card.Tier),
		card.Model,
		string(card.Upstream),
		string(card.Reason),
		card.Category,
		card.Complexity,
		card.Confidence,
		card.RequestTokensEst,
		card.Usage.Input,
		card.Usage.Output,
		card.Usage.CachedInput,
		card.CostUsd,
		card.ProjectedCostUsd,
		card.LatenciesMs.Jev,
		card.LatenciesMs.Upstream,
		card.LatenciesMs.Total,
		card.Status,
		card.RequestedModel,
	)
	return err
}

// AggRow mirrors cardlog.ts AggRow.
type AggRow struct {
	Bucket       string   `json:"bucket"`
	Requests     int64    `json:"requests"`
	CostUsd      *float64 `json:"cost_usd"`
	InputTokens  *int64   `json:"input_tokens"`
	OutputTokens *int64   `json:"output_tokens"`
	AvgLatencyMs *float64 `json:"avg_latency_ms"`
	Escalations  *int64   `json:"escalations"`
	JevFallbacks *int64   `json:"jev_fallbacks"`
}

// Aggregate mirrors cardlog.ts aggregate — the SQL is byte-identical.
func (c *CardLog) Aggregate(groupBy string) ([]AggRow, error) {
	var col string
	switch groupBy {
	case "day":
		col = "substr(ts, 1, 10)"
	case "tier":
		col = "tier"
	case "category":
		col = "category"
	case "model":
		col = "model"
	default:
		col = "session_id"
	}
	query := fmt.Sprintf(`
      SELECT %[1]s AS bucket,
             COUNT(*) AS requests,
             SUM(cost_usd) AS cost_usd,
             SUM(input_tokens) AS input_tokens,
             SUM(output_tokens) AS output_tokens,
             AVG(latency_total_ms) AS avg_latency_ms,
             SUM(CASE WHEN reason IN ('low_confidence_escalation','sticky_escape_up','cost_guard','context_gate') THEN 1 ELSE 0 END) AS escalations,
             SUM(CASE WHEN reason = 'jev_timeout_fallback' OR reason = 'jev_unavailable_fallback' THEN 1 ELSE 0 END) AS jev_fallbacks
      FROM route_cards
      GROUP BY %[1]s
      ORDER BY bucket`, col)
	rows, err := c.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AggRow
	for rows.Next() {
		var r AggRow
		if err := rows.Scan(&r.Bucket, &r.Requests, &r.CostUsd, &r.InputTokens, &r.OutputTokens, &r.AvgLatencyMs, &r.Escalations, &r.JevFallbacks); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Totals mirrors cardlog.ts Totals.
type Totals struct {
	Requests      int64    `json:"requests"`
	CostUsd       *float64 `json:"cost_usd"`
	InputTokens   *int64   `json:"input_tokens"`
	OutputTokens  *int64   `json:"output_tokens"`
	AvgLatencyMs  *float64 `json:"avg_latency_ms"`
}

// Totals mirrors cardlog.ts totals().
func (c *CardLog) Totals() (Totals, error) {
	row := c.db.QueryRow(`
      SELECT COUNT(*) AS requests,
             SUM(cost_usd) AS cost_usd,
             SUM(input_tokens) AS input_tokens,
             SUM(output_tokens) AS output_tokens,
             AVG(latency_total_ms) AS avg_latency_ms
      FROM route_cards`)
	var t Totals
	err := row.Scan(&t.Requests, &t.CostUsd, &t.InputTokens, &t.OutputTokens, &t.AvgLatencyMs)
	return t, err
}

// Close mirrors cardlog.ts close.
func (c *CardLog) Close() error {
	if c.insert != nil {
		c.insert.Close()
	}
	return c.db.Close()
}