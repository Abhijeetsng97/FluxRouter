// Package metrics mirrors old/src/metrics.ts: Prometheus text output.
// Names, HELP/TYPE lines, and the quantile formula are part of the contract
// (dashboards may scrape them) — quantile uses sorted[min(len-1, floor(q*len))],
// histogram reservoir caps at 512 samples with random replacement.
package metrics

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
)

// Metrics mirrors metrics.ts Metrics.
type Metrics struct {
	mu         sync.Mutex
	counters   map[string]float64
	histograms map[string][]float64
}

// New creates an empty registry.
func New() *Metrics {
	return &Metrics{
		counters:   map[string]float64{},
		histograms: map[string][]float64{},
	}
}

// Inc mirrors inc(name, amount = 1).
func (m *Metrics) Inc(name string, amount float64) {
	if amount == 0 {
		amount = 1 // TS default parameter semantics when called without amount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[name] += amount
}

// Observe mirrors observe: capped reservoir, random replacement at 512.
func (m *Metrics) Observe(name string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	arr, ok := m.histograms[name]
	if !ok {
		arr = make([]float64, 0, 512)
		m.histograms[name] = arr
	}
	if len(arr) < 512 {
		arr = append(arr, value)
	} else {
		arr[rand.Intn(len(arr))] = value // reservoir-ish, matches TS
	}
	m.histograms[name] = arr
}

// Snapshot renders the Prometheus text format, mirroring snapshot() exactly:
// the requests_total line first (with HELP/TYPE), then other counters
// (TYPE only, no HELP), then histograms as gauges with 0.5/0.9/0.99 quantiles.
func (m *Metrics) Snapshot() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var lines []string
	lines = append(lines,
		"# HELP fluxrouter_requests_total Total proxied requests",
		"# TYPE fluxrouter_requests_total counter",
		fmt.Sprintf("fluxrouter_requests_total %g", m.counters["requests_total"]))
	for k, v := range m.counters {
		if k == "requests_total" {
			continue
		}
		lines = append(lines,
			fmt.Sprintf("# TYPE fluxrouter_%s counter", k),
			fmt.Sprintf("fluxrouter_%s %g", k, v))
	}
	for k, arr := range m.histograms {
		sorted := make([]float64, len(arr))
		copy(sorted, arr)
		sort.Float64s(sorted)
		p := func(q float64) float64 {
			if len(sorted) == 0 {
				return 0
			}
			idx := int(math.Floor(q * float64(len(sorted))))
			if idx > len(sorted)-1 {
				idx = len(sorted) - 1
			}
			return sorted[idx]
		}
		lines = append(lines,
			fmt.Sprintf("# TYPE fluxrouter_%s gauge", k),
			fmt.Sprintf("fluxrouter_%s{quantile=\"0.5\"} %g", k, p(0.5)),
			fmt.Sprintf("fluxrouter_%s{quantile=\"0.9\"} %g", k, p(0.9)),
			fmt.Sprintf("fluxrouter_%s{quantile=\"0.99\"} %g", k, p(0.99)))
	}
	return strings.Join(lines, "\n") + "\n"
}