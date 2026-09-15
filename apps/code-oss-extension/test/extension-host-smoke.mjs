import { spawn } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { redactServiceError } from "../dist/src/service-resources.js";
import { waitForActivation } from "./extension-host-smoke-support.mjs";

const executable = process.env.CODE_OSS_EXECUTABLE?.trim();
if (!executable) {
  console.log("SKIP: CODE_OSS_EXECUTABLE is not configured");
  process.exit(0);
}

const repositoryRoot = resolve(import.meta.dirname, "../../..");
const extensionPath = join(repositoryRoot, "apps", "code-oss-extension");
const smokeRoot = await mkdtemp(join(tmpdir(), "unit-test-ide-extension-host-"));
const workspace = join(smokeRoot, "workspace");
const userDataDirectory = join(smokeRoot, "user-data");
const extensionsDirectory = join(smokeRoot, "extensions");
const sensitive = [executable, repositoryRoot, extensionPath, smokeRoot, workspace, userDataDirectory, extensionsDirectory];

function redactedFailure(message, output = "") {
  return redactServiceError(new Error(`${message}; process-output=${output}`), sensitive);
}

function waitForExit(child, timeoutMs) {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve();
  return new Promise((resolveExit, rejectExit) => {
    const timer = setTimeout(() => {
      cleanup();
      rejectExit(redactedFailure("Code-OSS did not exit after harness shutdown"));
    }, timeoutMs);
    const cleanup = () => {
      clearTimeout(timer);
      child.off("exit", onExit);
      child.off("error", onError);
    };
    const onExit = () => { cleanup(); resolveExit(); };
    const onError = (error) => { cleanup(); rejectExit(redactedFailure(error.message)); };
    child.once("exit", onExit);
    child.once("error", onError);
  });
}

let child;
try {
  await mkdir(join(workspace, ".vscode"), { recursive: true });
  await mkdir(userDataDirectory, { recursive: true });
  await mkdir(extensionsDirectory, { recursive: true });
  await writeFile(
    join(workspace, ".vscode", "settings.json"),
    `${JSON.stringify({ "unitTestIde.autoStart": false }, null, 2)}\n`,
    "utf8"
  );

  const args = [
    `--extensionDevelopmentPath=${extensionPath}`,
    "--extensionDevelopmentKind=workspace",
    workspace,
    "--verbose",
    "--disable-workspace-trust",
    "--skip-welcome",
    "--skip-release-notes",
    `--user-data-dir=${userDataDirectory}`,
    `--extensions-dir=${extensionsDirectory}`
  ];
  child = spawn(executable, args, {
    cwd: repositoryRoot,
    env: { ...process.env, UNIT_TEST_IDE_HOST_SMOKE: "1" },
    shell: false,
    windowsHide: true,
    stdio: ["ignore", "pipe", "pipe"]
  });

  await waitForActivation(
    child,
    join(userDataDirectory, "User", "globalStorage", "unit-test-ide.code-oss-extension", "activation.marker"),
    redactedFailure
  );
  child.kill("SIGTERM");
  try {
    await waitForExit(child, 5_000);
  } catch {
    child.kill("SIGKILL");
    await waitForExit(child, 5_000);
  }
  console.log("PASS: Code-OSS Extension Host activation marker observed and host process exited");
} catch (error) {
  if (child && child.exitCode === null && child.signalCode === null) {
    child.kill("SIGKILL");
    await waitForExit(child, 5_000).catch(() => undefined);
  }
  const safe = error instanceof Error
    ? redactServiceError(error, sensitive)
    : redactedFailure(String(error));
  console.error(`FAIL: ${safe.message}`);
  process.exitCode = 1;
} finally {
  try {
    await rm(smokeRoot, { recursive: true, force: true, maxRetries: 5, retryDelay: 50 });
  } catch (error) {
    const safe = error instanceof Error
      ? redactServiceError(error, sensitive)
      : redactedFailure(String(error));
    console.error(`FAIL: ${safe.message}`);
    process.exitCode = 1;
  }
}
