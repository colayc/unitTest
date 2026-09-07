import assert from "node:assert/strict";
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
