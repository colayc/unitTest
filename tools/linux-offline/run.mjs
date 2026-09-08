import { spawn, spawnSync } from "node:child_process";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const forbiddenEnvironment = /(?:proxy|registry|network)|^(?:npm_config_|pnpm_|yarn_|corepack_)/iu;
const fallbackPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin";

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
  validateCommand(command);
  return ["--user", "--map-root-user", "--net", "--mount-proc", "--fork", "--", ...command];
}

export function offlineSudoArguments(command, uid, gid, path = fallbackPath) {
  validateCommand(command);
  if (!Number.isSafeInteger(uid) || uid < 0 || !Number.isSafeInteger(gid) || gid < 0) throw new Error("Linux offline runner requires a valid invoking identity");
  if (typeof path !== "string" || path.length === 0 || path.includes("\0")) throw new Error("Linux offline runner requires a valid PATH");
  return [
    "--non-interactive", "--preserve-env", "--",
    "unshare", "--net", "--mount-proc", "--fork", "--",
    "setpriv", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", `--reuid=${uid}`, `--regid=${gid}`, "--clear-groups", "--",
    "/usr/bin/env", `PATH=${path}`,
    ...command
  ];
}

export function selectOfflineLauncher(command, options = {}) {
  validateCommand(command);
  const probe = options.probe ?? probeLauncher;
  const rootlessProbe = { executable: "unshare", arguments: offlineUnshareArguments(["true"]) };
  if (probe(rootlessProbe)) return { executable: "unshare", arguments: offlineUnshareArguments(command), mode: "rootless" };
  if (options.allowSudoRoot !== true) throw new Error("Linux offline runner cannot establish a rootless namespace and has no explicitly authorized sudo network namespace");
  const uid = options.uid ?? process.getuid?.();
  const gid = options.gid ?? process.getgid?.();
  const path = options.path ?? fallbackPath;
  const sudoProbe = { executable: "sudo", arguments: offlineSudoArguments(["true"], uid, gid, path) };
  if (!probe(sudoProbe)) throw new Error("Linux offline runner cannot establish a verified rootless or explicitly authorized sudo network namespace");
  return { executable: "sudo", arguments: offlineSudoArguments(command, uid, gid, path), mode: "sudo-root" };
}

export async function runOffline(command, options = {}) {
  if (process.platform !== "linux") throw new Error("Linux offline runner requires a Linux runner");
  const environment = offlineEnvironment();
  const launcher = selectOfflineLauncher(command, { ...options, path: environment.PATH, probe: options.probe ?? ((candidate) => probeLauncher(candidate, environment)) });
  process.stderr.write(`linux-offline: selected ${launcher.mode} network namespace\n`);
  await new Promise((resolvePromise, reject) => {
    const child = spawn(launcher.executable, launcher.arguments, {
      env: environment,
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

function validateCommand(command) {
  if (!Array.isArray(command) || command.length === 0 || command.some((value) => typeof value !== "string" || value.length === 0 || value.includes("\0"))) throw new Error("Linux offline runner requires a closed child command");
}

function probeLauncher(candidate, environment = offlineEnvironment()) {
  const result = spawnSync(candidate.executable, candidate.arguments, { env: environment, encoding: "utf8", shell: false, stdio: "ignore", windowsHide: true });
  return !result.error && result.status === 0;
}

async function main(arguments_) {
  const allowSudoRoot = arguments_[0] === "--allow-sudo-root";
  const commandStart = allowSudoRoot ? 1 : 0;
  if (arguments_.length < commandStart + 2 || arguments_[commandStart] !== "--") throw new Error("usage: run.mjs [--allow-sudo-root] -- <command> [arguments...]");
  await runOffline(arguments_.slice(commandStart + 1), { allowSudoRoot });
}

if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`linux-offline: ${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}
