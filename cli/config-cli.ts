// `flux config validate`.

import { loadConfig } from "../src/config.ts";

export async function runConfigValidate(args: { flags: Record<string, string | boolean> }): Promise<void> {
  const file = (args.flags["file"] as string | undefined) ?? "fluxrouter.config.json";
  const { config, errors } = loadConfig(file);
  if (errors.length > 0) {
    console.error(`✗ ${file}: ${errors.length} error(s)`);
    for (const e of errors) console.error(`  - ${e}`);
    process.exit(2);
  }
  console.log(`✓ ${file} valid`);
  console.log(`  jev: ${config.jev.model} @ ${config.jev.baseUrl} (timeout ${config.jev.timeoutMs}ms)`);
  for (const t of config.tiers) {
    const m = t.models[0]!;
    console.log(
      `  tier ${t.id} ${t.name.padEnd(8)} → ${m.upstream}:${m.id} ($${m.in}/$${m.out} per M in/out, ctx ${m.ctx})`,
    );
  }
  console.log(`  cost cap/request: $${config.cost.perRequestCapUsd}`);
  console.log(`  eval caps: routing $${config.eval.routingCapUsd}, e2e $${config.eval.e2eCapUsd}`);
}