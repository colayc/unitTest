import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import {
  publishEvidenceAtomically,
  validateCoverageEvidenceBytes
} from "./coverage-service-smoke-support.js";

interface Options {
  readonly platform: "linux" | "windows";
  readonly input: string;
  readonly output?: string;
}

function parseArguments(arguments_: readonly string[]): Options {
  let platform: Options["platform"] | undefined;
  let input: string | undefined;
  let output: string | undefined;
  for (let index = 0; index < arguments_.length; index += 2) {
    const name = arguments_[index];
    const value = arguments_[index + 1];
    if (value === undefined) throw new Error("coverage evidence validator arguments must be name/value pairs");
    if (name === "--platform" && (value === "linux" || value === "windows")) platform = value;
    else if (name === "--input") input = resolve(value);
    else if (name === "--output") output = resolve(value);
    else throw new Error(`unknown coverage evidence validator argument: ${name}`);
  }
  if (platform === undefined || input === undefined) {
    throw new Error("usage: coverage-evidence-validator --platform <linux|windows> --input <path> [--output <path>]");
  }
  if (platform === "windows" && output !== undefined) {
    throw new Error("Windows evidence validation does not create a second report");
  }
  return { platform, input, ...(output === undefined ? {} : { output }) };
}

export async function main(arguments_: readonly string[] = process.argv.slice(2)): Promise<void> {
  const options = parseArguments(arguments_);
  const bytes = await readFile(options.input);
  validateCoverageEvidenceBytes(options.platform, bytes);
  if (options.output !== undefined) {
    if (options.output === options.input) throw new Error("coverage evidence output must differ from its input");
    await publishEvidenceAtomically(options.output, bytes);
  }
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(import.meta.filename)) {
  main().catch((error: unknown) => {
    process.stderr.write(`coverage-evidence-validator: ${error instanceof Error ? error.stack ?? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}

export const __testing = Object.freeze({ parseArguments });
