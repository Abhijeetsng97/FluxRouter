// package routing test: ports the cost-related tests from old/tests/unit.test.ts.
package routing

import (
	"math"
	"testing"
)

func TestEstimateTokensMessageAwareCharsOver4(t *testing.T) {
	// TS: estimateTokens([{content:"hello world"}]) === Math.ceil((11+8)/4)
	got := EstimateTokens([]Message{{Content: "hello world"}})
	want := int64(math.Ceil(float64(11+8) / 4)) // 5
	if got != want {
		t.Fatalf("EstimateTokens = %d, want %d", got, want)
	}
	if got := EstimateTokens(nil); got != 0 {
		t.Fatalf("EstimateTokens([]) = %d, want 0", got)
	}
}

func TestProjectedCostMath(t *testing.T) {
	// 1000 in @ 0.10/M + 100 out @ 1/M
	got := ProjectedCost(1000, 100, Rates{In: 0.1, Out: 1})
	want := 1000/1e6*0.1 + 100/1e6*1
	if got != want {
		t.Fatalf("ProjectedCost = %v, want %v", got, want)
	}
}

func TestBudgetGuardRefusesOverCap(t *testing.T) {
	if ok, _ := BudgetGuard(1.5, 1); ok {
		t.Fatal("BudgetGuard(1.5, 1).ok should be false")
	}
	if ok, _ := BudgetGuard(0.99, 1); !ok {
		t.Fatal("BudgetGuard(0.99, 1).ok should be true")
	}
}

func TestUtf16Semantics(t *testing.T) {
	// TS vector: estimateTokens([{content:"héllo🎉"}]) === 4.
	// "héllo🎉" = 5 BMP chars + 1 non-BMP (2 units) = 7 UTF-16 units;
	// ceil((7 + 8) / 4) = 4. A byte-length implementation would give
	// ceil((6 + 8)/4) = 4 here too, so the distinguishing vectors live in
	// jsstr tests and the session-ID unicode vector.
	if got := estimateTokensText("héllo🎉"); got != 4 {
		t.Fatalf("estimateTokens(héllo🎉) = %d, want 4 (TS vector)", got)
	}
	// ASCII vector: ceil((11+8)/4) = 5.
	if got := estimateTokensText("hello world"); got != 5 {
		t.Fatalf("estimateTokens(hello world) = %d, want 5", got)
	}
	// Multi-message vector: ceil(((1+8)+(2+8))/4) = 5.
	if got := EstimateTokens([]Message{{Content: "a"}, {Content: "bb"}}); got != 5 {
		t.Fatalf("estimateTokens(a,bb) = %d, want 5 (TS vector)", got)
	}
}

// estimateTokensText is a direct single-string helper for tests.
func estimateTokensText(s string) int64 { return EstimateTokens([]Message{{Content: s}}) }