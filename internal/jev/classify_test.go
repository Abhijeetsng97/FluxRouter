// Package jev tests: buildJevState parity against recorded TS vectors
// (catalog item 8) and ParseJevResponse behavior.
package jev

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/abhijeet/fluxrouter/internal/types"
)

func TestBuildJevStateRecordedVectors(t *testing.T) {
	var vectors struct {
		JevStates []string `json:"jevStates"`
	}
	data, err := os.ReadFile("../testdata/vectors.json")
	if err != nil {
		t.Fatalf("vectors.json missing: %v", err)
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatalf("vectors.json malformed: %v", err)
	}

	// Case 1: short single message.
	got1 := BuildJevState([]Message{{Role: "user", Content: "hello"}}, 2000)
	if got1 != vectors.JevStates[0] {
		t.Fatalf("state 1 = %q, want %q", got1, vectors.JevStates[0])
	}

	// Case 2: long single message → head+tail.
	long := strings.Repeat("a", 6000) + "MIDDLE" + strings.Repeat("b", 6000)
	got2 := BuildJevState([]Message{{Role: "user", Content: long}}, 2000)
	if got2 != vectors.JevStates[1] {
		t.Fatalf("state 2 mismatch: len(go)=%d len(ts)=%d", len(got2), len(vectors.JevStates[1]))
	}
	if !strings.HasPrefix(got2, "[LAST]") || !strings.Contains(got2, "…[truncated]…") {
		t.Fatalf("state 2 shape wrong: %q", got2[:80])
	}
	if strings.Contains(got2, "MIDDLE") {
		t.Fatal("state 2 should exclude the middle (head+tail only)")
	}

	// Case 3: two long messages → hist truncated at quarter, last at half.
	got3 := BuildJevState([]Message{
		{Role: "system", Content: strings.Repeat("s", 3000)},
		{Role: "user", Content: "question " + strings.Repeat("c", 3000)},
	}, 2000)
	if got3 != vectors.JevStates[2] {
		t.Fatalf("state 3 mismatch: len(go)=%d len(ts)=%d", len(got3), len(vectors.JevStates[2]))
	}
	if !strings.HasPrefix(got3, "[HIST-1]") || !strings.Contains(got3, "[LAST]") {
		t.Fatal("state 3 labels wrong")
	}
}

func TestParseJevResponseFullShape(t *testing.T) {
	score := 1.3
	conf90, conf80 := 0.9, 0.8
	noul := 0.05
	resp := types.JevResponse{
		Model: "jev-1.13.0",
		Answers: map[string]types.JevAnswer{
			"category":  {Type: "choice", Choice: "math", Confidence: &conf90, Probabilities: map[string]float64{"math": 0.9}},
			"complexity": {Type: "score", Score: &score, Confidence: &conf80},
			"is_trivial": {Type: "noul", Noul: &noul},
		},
		Usage: types.JevUsage{InputTokens: 200, OutputTokens: 30},
	}
	r, err := ParseJevResponse(resp)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if r.Category != "math" {
		t.Fatalf("category = %q, want math", r.Category)
	}
	if r.Complexity != 1.3 {
		t.Fatalf("complexity = %v, want 1.3", r.Complexity)
	}
	if r.TrivialNoul != 0.05 {
		t.Fatalf("trivialNoul = %v, want 0.05", r.TrivialNoul)
	}
	if min(r.CategoryConfidence, r.ComplexityConfidence) != 0.8 {
		t.Fatalf("min confidence = %v, want 0.8", min(r.CategoryConfidence, r.ComplexityConfidence))
	}
}

func TestParseJevResponseThrowsOnMissingAnswers(t *testing.T) {
	_, err := ParseJevResponse(types.JevResponse{Model: "x", Answers: map[string]types.JevAnswer{}})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected 'missing' error, got %v", err)
	}
	var ue *UnavailableError
	if !errors.As(err, &ue) {
		t.Fatalf("error should be UnavailableError, got %T", err)
	}
}

func TestParseJevResponseDefaults(t *testing.T) {
	// choice missing → "other"; score/noul/confidence missing → 0.
	resp := types.JevResponse{
		Model: "m",
		Answers: map[string]types.JevAnswer{
			"category":  {Type: "choice"},
			"complexity": {Type: "score"},
			"is_trivial": {Type: "noul"},
		},
	}
	r, err := ParseJevResponse(resp)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if r.Category != "other" || r.Complexity != 0 || r.TrivialNoul != 0 || r.ComplexityConfidence != 0 || r.CategoryConfidence != 0 {
		t.Fatalf("defaults wrong: %+v", r)
	}
}