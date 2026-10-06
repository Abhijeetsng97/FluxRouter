// Package session test: ports session tests from old/tests/unit.test.ts.
package session

import (
	"testing"
	"time"
)

func TestSessionIDStableAcrossCalls(t *testing.T) {
	a := SessionIDFor([]Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hello"}})
	b := SessionIDFor([]Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hello"}})
	if a != b {
		t.Fatalf("session ids differ for identical input: %q vs %q", a, b)
	}
	c := SessionIDFor([]Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "different"}})
	if a == c {
		t.Fatal("session ids coincide for different first user message")
	}
}

// Recorded cross-engine vectors (old/scripts/gen-vectors.mjs → testdata/vectors.json):
//   ["sys"/"hello"]            → bb9afd98e497eec304502fca
//   ["hi"] no system           → 39d9856434cc1b39ec0dc9e7
//   ["héllo🎉 sys"/"x"]        → 7182bd825fb370f4e7bf664a   (UTF-16 length parity!)
//   ["You are a helpful…"/prime-proof] → e0fcd3a15a94ec03ba31ff7a
func TestSessionIDRecordedVectors(t *testing.T) {
	cases := []struct {
		name     string
		messages []Message
		want     string
	}{
		{
			name:     "plain ascii",
			messages: []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hello"}},
			want:     "bb9afd98e497eec304502fca",
		},
		{
			name:     "no system message",
			messages: []Message{{Role: "user", Content: "hi"}},
			want:     "39d9856434cc1b39ec0dc9e7",
		},
		{
			name:     "unicode system prompt",
			messages: []Message{{Role: "system", Content: "héllo🎉 sys"}, {Role: "user", Content: "x"}},
			want:     "7182bd825fb370f4e7bf664a",
		},
		{
			name:     "realistic prime proof",
			messages: []Message{{Role: "system", Content: "You are a helpful assistant"}, {Role: "user", Content: "Prove that there are infinitely many primes. Justify every step."}},
			want:     "e0fcd3a15a94ec03ba31ff7a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SessionIDFor(tc.messages); got != tc.want {
				t.Fatalf("SessionIDFor = %q, want recorded TS vector %q", got, tc.want)
			}
		})
	}
}

func TestStickyStorePinGetRePin(t *testing.T) {
	store := NewStore()
	defer store.Dispose()
	id := "sess1"
	if have := store.Get(id); have != nil {
		t.Fatalf("expected missing session, got %+v", have)
	}
	store.Pin(id, 0, "nano", "ollama")
	if got := store.Get(id); got == nil || got.Tier != 0 {
		t.Fatalf("pin didn't record tier 0: %+v", got)
	}
	store.RePin(id, 2, "mid", "ollama")
	if got := store.Get(id); got == nil || got.Tier != 2 {
		t.Fatalf("rePin didn't update tier: %+v", got)
	}
	if got := store.Get(id); got != nil && got.Turns != 2 {
		t.Fatalf("rePin should bump turns to 2, got %d", got.Turns)
	}
}

func TestStoreTTLExpiry(t *testing.T) {
	store := &Store{sessions: map[string]*State{}} // no sweep goroutine for the test
	id := "old"
	store.sessions[id] = &State{Tier: 1, Model: "m", Upstream: "u", PinnedAt: FormatISO(time.Now().Add(-2 * TTL)), Turns: 3}
	if got := store.Get(id); got != nil {
		t.Fatal("expired session should report missing")
	}
	if len(store.sessions) != 0 {
		t.Fatal("expired session should be deleted")
	}
}

func TestFormatISOMatchesJS(t *testing.T) {
	// JS new Date(0).toISOString() === "1970-01-01T00:00:00.000Z"
	if got := FormatISO(time.Unix(0, 0).UTC()); got != "1970-01-01T00:00:00.000Z" {
		t.Fatalf("FormatISO = %q, want 1970-01-01T00:00:00.000Z", got)
	}
}