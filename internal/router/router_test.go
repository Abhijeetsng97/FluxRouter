// Router package tests: ParseChatRequest (item 9), ExtractUsage, CostFromUsage.
package router

import (
	"encoding/json"
	"testing"

	"github.com/abhijeet/fluxrouter/internal/types"
)

func TestParseChatRequestBasics(t *testing.T) {
	req := ParseChatRequest([]byte(`{"messages":[{"role":"user","content":"hi"}],"stream":true,"model":"flux"}`))
	if req == nil {
		t.Fatal("nil for valid body")
	}
	if !req.Stream || req.Model != "flux" || len(req.Messages) != 1 || req.Messages[0].Role != "user" || req.Messages[0].Content != "hi" {
		t.Fatalf("parsed wrong: %+v", req)
	}
}

func TestParseChatRequestMultimodalContentJSONified(t *testing.T) {
	// Item 9: non-string content -> JSON.stringify(content) with SOURCE KEY
	// ORDER preserved (TS JSON.parse insertion order — this is exactly what
	// the ordered reader exists to guarantee).
	req := ParseChatRequest([]byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`))
	if got := req.Messages[0].Content; got != `[{"type":"text","text":"hi"}]` {
		t.Fatalf("content = %q, want source-order JSON text", got)
	}
	// Reversed key order in the source must round-trip reversed (alpha sort
	// would silently rewrite it — the divergence the ordered reader prevents).
	req = ParseChatRequest([]byte(`{"messages":[{"content":[{"text":"hi","type":"text"}],"role":"user"}]}`))
	if got := req.Messages[0].Content; got != `[{"text":"hi","type":"text"}]` {
		t.Fatalf("content = %q, want source-order preserved", got)
	}
}

func TestParseChatRequestNullContentBecomesEmptyQuoted(t *testing.T) {
	// TS: JSON.stringify(null ?? "") === '""' — the 2-char string.
	req := ParseChatRequest([]byte(`{"messages":[{"role":"user","content":null}]}`))
	if got := req.Messages[0].Content; got != `""` {
		t.Fatalf("content = %q, want the 2-char JSON-empty-string", got)
	}
	// Missing content key behaves the same (undefined ?? "").
	req = ParseChatRequest([]byte(`{"messages":[{"role":"user"}]}`))
	if got := req.Messages[0].Content; got != `""` {
		t.Fatalf("missing content = %q, want %q", got, `""`)
	}
}

func TestParseChatRequestMissingRoleDefaultsUser(t *testing.T) {
	req := ParseChatRequest([]byte(`{"messages":[{"content":"x"},{"role":null,"content":"y"}]}`))
	if req.Messages[0].Role != "user" || req.Messages[1].Role != "user" {
		t.Fatalf("roles = %q, %q — both should be user", req.Messages[0].Role, req.Messages[1].Role)
	}
}

func TestParseChatRequestNullsReturnsNil(t *testing.T) {
	if got := ParseChatRequest(nil); got != nil {
		t.Fatalf("nil body should give nil, got %+v", got)
	}
	if got := ParseChatRequest([]byte(`{"messages":"not-an-array"}`)); got != nil {
		t.Fatalf("non-array messages should give nil, got %+v", got)
	}
	if got := ParseChatRequest([]byte(`{"nope":1}`)); got != nil {
		t.Fatalf("missing messages should give nil, got %+v", got)
	}
	if got := ParseChatRequest([]byte(`not json at all`)); got != nil {
		t.Fatalf("malformed json should give nil, got %+v", got)
	}
}

func TestExtractUsageOpenAIShape(t *testing.T) {
	var body any
	if err := json.Unmarshal([]byte(`{"usage":{"prompt_tokens":120,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":10}}}`), &body); err != nil {
		t.Fatal(err)
	}
	got := ExtractUsage(body)
	want := types.Usage{Input: 120, Output: 40, CachedInput: 10}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	if got := ExtractUsage(map[string]any{}); got != (types.Usage{}) {
		t.Fatalf("empty usage = %+v", got)
	}
}

func TestCostFromUsageCachedTier(t *testing.T) {
	m := types.TierModel{In: 1, Out: 2, CachedIn: 0.1}
	// 0.5M normal in + 0.5M cached in + 1M out
	got := CostFromUsage(types.Usage{Input: 1_000_000, Output: 1_000_000, CachedInput: 500_000}, m)
	if got != 0.5+0.05+2 {
		t.Fatalf("cost = %v, want 2.55", got)
	}
	// No cached usage -> plain math.
	got = CostFromUsage(types.Usage{Input: 1_000_000, Output: 1_000_000}, m)
	if got != 3 {
		t.Fatalf("cost = %v, want 3", got)
	}
}