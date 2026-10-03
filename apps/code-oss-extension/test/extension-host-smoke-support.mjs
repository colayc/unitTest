import { readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { EXTENSION_ACTIVATION_MARKER } from "../dist/src/extension.js";

export function resolveExtensionUnderTest(repositoryRoot, configuredPath) {
  const selectedPath = configuredPath?.trim();
  return selectedPath
    ? resolve(selectedPath)
    : join(repositoryRoot, "apps", "code-oss-extension");
}

function boundedOutput(current, chunk) {
  const next = current + String(chunk);
  return next.length <= 131_072 ? next : next.slice(-131_072);
}

export function waitForActivation(
  child,
  markerPath,
  redactFailure,
  { timeoutMs = 30_000, pollIntervalMs = 50 } = {}
) {
  return new Promise((resolveActivation, rejectActivation) => {
    let output = "";
    let settled = false;
    let invalidMarkerObserved = false;
    let markerPoll;
    const timer = setTimeout(
      () => finish(new Error(invalidMarkerObserved
        ? "Code-OSS activation marker file remained invalid"
        : "activation marker timed out")),
      timeoutMs
    );
    const cleanup = () => {
      clearTimeout(timer);
      clearTimeout(markerPoll);
      child.stdout.off("data", onData);
      child.stderr.off("data", onData);
      child.off("error", onError);
      child.off("exit", onExit);
    };
    const finish = (error) => {
      if (settled) return;
      settled = true;
      cleanup();
      if (error) rejectActivation(redactFailure(error.message, output));
      else resolveActivation();
    };
    const onData = (chunk) => {
      output = boundedOutput(output, chunk);
    };
    const onError = (error) => finish(error);
    const onExit = (code, signal) => finish(new Error(
      `Code-OSS exited before activation marker with code ${String(code)} and signal ${String(signal)}`
    ));
    const pollMarker = async () => {
      if (settled) return;
      try {
        const marker = await readFile(markerPath, "utf8");
        if (marker !== `${EXTENSION_ACTIVATION_MARKER}\n`) {
          invalidMarkerObserved = true;
          markerPoll = setTimeout(() => void pollMarker(), pollIntervalMs);
          return;
        }
        finish();
      } catch (error) {
        if (error?.code !== "ENOENT") {
          finish(error);
          return;
        }
        markerPoll = setTimeout(() => void pollMarker(), pollIntervalMs);
      }
    };
    child.stdout.on("data", onData);
    child.stderr.on("data", onData);
    child.once("error", onError);
    child.once("exit", onExit);
    void pollMarker();
  });
}
