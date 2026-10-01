// Minimal typed command framework for the flux CLI (no heavy deps).

export interface FlagDef {
  name: string;
  arg?: string;
  description?: string;
}

export interface CommandDef {
  name: string;
  description?: string;
  flags?: FlagDef[];
  subcommands?: CommandDef[];
  run: (args: ParsedArgs) => void | Promise<void>;
}

export interface ParsedArgs {
  _: string[];
  flags: Record<string, string | boolean>;
  raw?: string[];
}

export function parse(args: string[], defs: FlagDef[] = []): ParsedArgs {
  const positional: string[] = [];
  const flags: Record<string, string | boolean> = {};
  for (let i = 0; i < args.length; i++) {
    const a = args[i]!;
    if (a.startsWith("--")) {
      const key = a.slice(2);
      const def = defs.find((d) => d.name === `--${key}`);
      if (def?.arg) {
        const val = args[++i];
        flags[key] = val ?? "";
      } else {
        flags[key] = true;
      }
    } else {
      positional.push(a);
    }
  }
  return { _: positional, flags };
}

export function defineCommand(def: CommandDef): CommandDef {
  return def;
}

export interface ProgramDef {
  name: string;
  version: string;
  description?: string;
  commands: CommandDef[];
  args: string[];
}

async function renderHelp(prog: ProgramDef): Promise<void> {
  console.log(`${prog.name} v${prog.version}`);
  console.log("\nCommands:");
  for (const c of prog.commands) {
    console.log(`  ${pad(c.name)}${c.description ?? ""}`);
    if (c.subcommands) {
      for (const s of c.subcommands) console.log(`    ${pad(s.name, 18)}${s.description ?? ""}`);
    }
    for (const f of c.flags ?? []) {
      console.log(`    ${pad(f.name + (f.arg ? ` <${f.arg}>` : ""), 18)}${f.description ?? ""}`);
    }
  }
}

function pad(s: string, n = 10): string {
  return s.padEnd(n, " ");
}

export async function run(prog: ProgramDef): Promise<void> {
  const [cmdName, ...rest] = prog.args;
  if (!cmdName || cmdName === "--help" || cmdName === "help") {
    await renderHelp(prog);
    return;
  }
  const cmd = prog.commands.find((c) => c.name === cmdName);
  if (!cmd) {
    console.error(`unknown command: ${cmdName}`);
    await renderHelp(prog);
    process.exit(2);
  }
  // Subcommand dispatch.
  if (cmd.subcommands && rest) {
    const [subName, ...subRest] = rest;
    const sub = cmd.subcommands.find((s) => s.name === subName);
    if (sub) {
      const parsed = parse(subRest, sub.flags ?? []);
      await sub.run(parsed);
      return;
    }
    console.error(`unknown ${cmd.name} subcommand: ${subName}`);
    process.exit(2);
  }
  const parsed = parse(rest, cmd.flags);
  await cmd.run(parsed);
}