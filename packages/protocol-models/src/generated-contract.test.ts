import test from "node:test";
import assert from "node:assert/strict";
import type { Capabilities } from "./index.js";

test("generated capabilities represent an empty Windows service", () => {
  const value: Capabilities = {
    platform: "windows",
    transports: ["named-pipe"],
    toolchains: [],
    frameworks: [],
    coverageTools: []
  };
  assert.equal(value.platform, "windows");
});
