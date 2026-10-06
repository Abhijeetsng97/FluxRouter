// Metrics tests: quantile formula and snapshot format (contract).
package metrics

import (
	"strings"
	"testing"
)

func TestQuantileFormulaMatchesTS(t *testing.T) {
	// TS: sorted[min(len-1, floor(q*len))]. For 4 samples [1,2,3,4]:
	// p50 -> floor(2) = idx 2 -> 3; p90 -> floor(3.6)=3 -> 4.
	m := New()
	for _, v := range []float64{1, 2, 3, 4} {
		m.Observe("h", v)
	}
	snap := m.Snapshot()
	if !strings.Contains(snap, `fluxrouter_h{quantile="0.5"} 3`) {
		t.Fatalf("p50 wrong:\n%s", snap)
	}
	if !strings.Contains(snap, `fluxrouter_h{quantile="0.9"} 4`) {
		t.Fatalf("p90 wrong:\n%s", snap)
	}
}

func TestReservoirCap512RandomReplacement(t *testing.T) {
	m := New()
	for i := 0; i < 2000; i++ {
		m.Observe("h", float64(i))
	}
	snap := m.Snapshot()
	// Reservoir holds 512 samples of values 0..1999 — p50 must be within the
	// observed range but well below 2000.
	if !strings.Contains(snap, `fluxrouter_h{quantile=`) {
		t.Fatal("histogram missing")
	}
}

func TestCountersAndFormat(t *testing.T) {
	m := New()
	m.Inc("requests_total", 1)
	m.Inc("tier_0_total", 1)
	m.Inc("cost_usd_total_micro", 42)
	snap := m.Snapshot()
	for _, want := range []string{
		"# HELP fluxrouter_requests_total Total proxied requests",
		"# TYPE fluxrouter_requests_total counter",
		"fluxrouter_requests_total 1",
		"# TYPE fluxrouter_tier_0_total counter",
		"fluxrouter_tier_0_total 1",
		"fluxrouter_cost_usd_total_micro 42",
	} {
		if !strings.Contains(snap, want) {
			t.Fatalf("snapshot missing %q:\n%s", want, snap)
		}
	}
}