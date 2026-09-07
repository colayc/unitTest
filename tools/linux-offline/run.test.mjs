import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import test from "node:test";
import { offlineEnvironment, offlineUnshareArguments } from "./run.mjs";

test("Linux offline runner removes proxy and package-manager network inputs before child launch", () => {
  const environment = offlineEnvironment({
    HTTP_PROXY: "http://proxy.invalid",
    npm_config_registry: "https://registry.invalid",
    SAFE: "1"
  });
  assert.deepEqual(environment, { SAFE: "1", NO_PROXY: "*", no_proxy: "*", npm_config_offline: "true" });
  assert.deepEqual(offlineUnshareArguments(["node", "child.mjs"]), ["--user", "--map-root-user", "--net", "--mount-proc", "--", "node", "child.mjs"]);
});

test("Linux workflow prepares downloads before entering a namespace and wraps final native E2E", async () => {
  const workflow = await readFile(resolve(import.meta.dirname, "..", "..", ".github", "workflows", "foundation.yml"), "utf8");
  const coveragePrepare = workflow.indexOf("- run: pnpm prepare:coverage-bundle");
  const frameworkPrepare = workflow.indexOf("- name: Prepare locked Linux framework inputs");
  const namespaceProbe = workflow.indexOf("Verify fail-closed Linux offline namespace");
  assert.ok(coveragePrepare !== -1 && frameworkPrepare !== -1 && namespaceProbe !== -1 && coveragePrepare < namespaceProbe && frameworkPrepare < namespaceProbe);
  assert.match(workflow, /node tools\/linux-offline\/run\.mjs -- pnpm test:e2e:native/u);
  assert.doesNotMatch(workflow, /\n\s*- run: pnpm test:e2e:native\n/u);
});
