// package routing mirrors old/src/cost.ts: token estimation, cost projection,
// budget guards. Numbers must match the TS engine to the last bit for the
// same inputs, so keep formulas in the exact same shape.
package routing

import (
	"fmt"
	"math"
	"strconv"

	"github.com/abhijeet/fluxrouter/internal/compat"
)

// TokenRates mirrors cost.ts TokenRates.
type TokenRates struct {
	In       float64 // $/M input
	CachedIn float64 // $/M cached input (0 = not configured)
	Out      float64 // $/M output
}

// Message is the unified message shape: EstimateTokens reads Content;
// SessionIDFor reads Role + Content � one type for both (cost.ts +
// session.ts both consumed {role, content} in TS).


type Message struct {
	Role    string
	Content string
}
// EstimateTokens mirrors cost.ts estimateTokens:
// ceil((sum(chars) + 8*messages) / 4); 0 for zero messages.
// JS `.length` counts UTF-16 code units, so use compat.JsLen — parity of the
// route-card requestTokensEst field depends on this.
func EstimateTokens(messages []Message) int64 {
	if len(messages) == 0 {
		return 0
	}
	var chars int64
	for _, m := range messages {
		chars += int64(compat.JsLen(m.Content)) + 8 // +8 role/overhead
	}
	return int64(math.Ceil(float64(chars) / 4))
}

// Rates mirrors the TS inline type `{ in: number; out: number }`.
type Rates struct {
	In  float64
	Out float64
}

// ProjectedCost mirrors cost.ts projectedCost:
// (tokensIn/1e6)*in + (tokensOut/1e6)*out.
func ProjectedCost(tokensIn, tokensOut int64, rates Rates) float64 {
	return (float64(tokensIn)/1e6)*rates.In + (float64(tokensOut)/1e6)*rates.Out
}

// BudgetGuard mirrors cost.ts budgetGuard.
func BudgetGuard(projectedTotalUsd, capUsd float64) (ok bool, overBy float64) {
	over := math.Max(0, projectedTotalUsd-capUsd)
	// TS: Number(overBy.toFixed(4)) — quantize the reported overage.
	overBy = roundTo4(over)
	return over == 0, overBy
}

// roundTo4 mirrors Number(x.toFixed(4)).
func roundTo4(x float64) float64 {
	v, _ := strconv.ParseFloat(fmt.Sprintf("%.4f", x), 64)
	return v
}
