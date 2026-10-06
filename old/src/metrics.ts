// Prometheus text metrics for FluxRouter.

export class Metrics {
  private counters = new Map<string, number>();
  private histograms = new Map<string, number[]>(); // capped samples

  inc(name: string, amount = 1): void {
    this.counters.set(name, (this.counters.get(name) ?? 0) + amount);
  }

  observe(name: string, value: number): void {
    let arr = this.histograms.get(name);
    if (!arr) {
      arr = [];
      this.histograms.set(name, arr);
    }
    if (arr.length < 512) arr.push(value);
    else arr[Math.floor(Math.random() * arr.length)] = value; // reservoir-ish
  }

  snapshot(): string {
    const lines: string[] = [];
    lines.push("# HELP fluxrouter_requests_total Total proxied requests");
    lines.push("# TYPE fluxrouter_requests_total counter");
    lines.push(`fluxrouter_requests_total ${this.counters.get("requests_total") ?? 0}`);
    for (const [k, v] of this.counters) {
      if (k !== "requests_total") {
        lines.push(`# TYPE fluxrouter_${k} counter`);
        lines.push(`fluxrouter_${k} ${v}`);
      }
    }
    for (const [k, arr] of this.histograms) {
      const sorted = [...arr].sort((a, b) => a - b);
      const p = (q: number) => (sorted.length ? sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))]! : 0);
      lines.push(`# TYPE fluxrouter_${k} gauge`);
      lines.push(`fluxrouter_${k}{quantile="0.5"} ${p(0.5)}`);
      lines.push(`fluxrouter_${k}{quantile="0.9"} ${p(0.9)}`);
      lines.push(`fluxrouter_${k}{quantile="0.99"} ${p(0.99)}`);
    }
    return `${lines.join("\n")}\n`;
  }
}