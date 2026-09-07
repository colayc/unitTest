import { spawn, spawnSync } from "node:child_process";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const forbiddenEnvironment = /(?:proxy|registry|network)|^(?:npm_config_|pnpm_|yarn_|corepack_)/iu;

/** Produces the only environment inherited by native Linux process trees. */
export function offlineEnvironment(source = process.env) {
  const environment = {};
  for (const [key, value] of Object.entries(source)) {
    if (value !== undefined && !forbiddenEnvironment.test(key)) environment[key] = value;
  }
  environment.NO_PROXY = "*";
  environment.no_proxy = "*";
  environment.npm_config_offline = "true";
  return environment;
}

/** Network namespaces are established before the requested process starts. */
export function offlineUnshareArguments(command) {
  if (!Array.isArray(command) || command.length === 0 || command.some((value) => typeof value !== "string" || value.length === 0 || value.includes("\0"))) throw new Error("Linux offline runner requires a closed child command");
  return ["--user", "--map-root-user", "--net", "--mount-proc", "--", ...command];
}

export async function runOffline(command) {
  if (process.platform !== "linux") throw new Error("Linux offline runner requires a Linux runner");
  const availability = spawnSync("unshare", ["--version"], { encoding: "utf8", shell: false, windowsHide: true });
  if (availability.error || availability.status !== 0) throw new Error("Linux offline runner cannot establish a verified network namespace");
  await new Promise((resolvePromise, reject) => {
    const child = spawn("unshare", offlineUnshareArguments(command), {
      env: offlineEnvironment(),
      shell: false,
      stdio: "inherit",
      windowsHide: true
    });
    child.once("error", (error) => reject(new Error(`Linux offline runner failed before child launch: ${error.message}`)));
    child.once("exit", (code, signal) => {
      if (code === 0) resolvePromise();
      else reject(new Error(`Linux offline child failed (${signal ?? code ?? "unknown"})`));
    });
  });
}

async function main(arguments_) {
  if (arguments_.length < 2 || arguments_[0] !== "--") throw new Error("usage: run.mjs -- <command> [arguments...]");
  await runOffline(arguments_.slice(1));
}

if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`linux-offline: ${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}
