import assert from "node:assert/strict";
import childProcess from "node:child_process";
import fs from "node:fs/promises";
import { syncBuiltinESMExports } from "node:module";
import { resolve } from "node:path";
import test from "node:test";

const url = new URL("./native-framework-benchmark.js", import.meta.url).href;
const { collectAuditedFrameworkBenchmark, parseAuditedFrameworkBenchmark } = await import(url);
const root = resolve(import.meta.dirname, "../../..");
const anchor = "UTIDE_CATALOG10000 719c74062e19f57e40df62f5183f591bba6dde4941406dac2452f469e89da9d4 3f9d35d363047ac75f39b8555b8ee70da4e6032501c7260f677c64a66cdbf50f 68ec3e51f2471e3eacf58b59b94d39c614992f55ec8486f91afbcadc08971c7d";
const output = `goos: windows\ngoarch: amd64\npkg: unit-test-ide.local/test-service/internal/testdomain\ncpu: Test CPU\n${[101, 102, 103].map(n => `${anchor}\nBenchmarkCatalog10000-8 1 120000 ns/op 8000 B/op ${n} allocs/op`).join("\n")}\nPASS\nok\tunit-test-ide.local/test-service/internal/testdomain\t0.1s\n`;

test("audited benchmark parses exactly three real samples bound to fixed input identity", () => {
  const result = parseAuditedFrameworkBenchmark(output);
  assert.deepEqual(result.allocationsPerOperation, [101, 102, 103]);
  assert.equal(result.catalogArtifactSha256, "3f9d35d363047ac75f39b8555b8ee70da4e6032501c7260f677c64a66cdbf50f");
  assert.equal(result.allocationBudgetPerOperation, 300000);
});

test("benchmark rejects incomplete, extra, over-budget, malformed and path-bearing evidence", () => {
  for (const value of [output.replace("103 allocs/op", "300001 allocs/op"), output.replace("103 allocs/op", "NaN allocs/op"),
    output.replace("103 allocs/op", "C:\\private allocs/op"), output.replace("103 allocs/op", "-1 allocs/op"),
    output.replace("103 allocs/op", "1.5 allocs/op"), output.replace(/BenchmarkCatalog10000-8 1 120000 ns\/op 8000 B\/op 103 allocs\/op/u, ""),
    output + "BenchmarkCatalog10000-8 1 120000 ns/op 8000 B/op 104 allocs/op\n", output.replaceAll(anchor, anchor.replace("719c", "ffff")),
    output.replaceAll(anchor, ""), output.replace("PASS", "FAIL"), output + "TOKEN=private\n",
    output.replace(anchor, `BenchmarkCatalog10000-8 UTIDE_CATALOG10000 C:\\private ${anchor}`)]) {
    assert.throws(() => parseAuditedFrameworkBenchmark(value), /benchmark/u);
  }
});

test("collector fixes command, package, bounds and offline environment and rejects caller overrides", async (t) => {
  let calls = 0;
  t.mock.method(childProcess, "execFile", (file: string, args: string[], options: any, callback: Function) => {
    calls++;
    assert.equal(file, "go");
    assert.deepEqual(args, ["test", "./apps/test-service/internal/testdomain", "-run=^$", "-bench=^BenchmarkCatalog10000$", "-benchmem", "-benchtime=1x", "-count=3"]);
    assert.equal(options.cwd, root);
    assert.equal(options.shell, false);
    assert.equal(options.timeout, 120000);
    assert.equal(options.maxBuffer, 65536);
    assert.equal(options.env.GOPROXY, "off");
    assert.equal(options.env.GOTOOLCHAIN, "local");
    assert.equal(options.env.GOFLAGS, "-mod=readonly");
    callback(null, output, "");
  });
  assert.deepEqual((await collectAuditedFrameworkBenchmark(root)).allocationsPerOperation, [101, 102, 103]);
  assert.equal(calls, 1);
  for (const override of [{ path: "fixture.json" }, { allocationsPerOperation: [1, 2, 3] }, { env: { GOFLAGS: "-overlay=private" } }, { shell: "cmd" }]) {
    await assert.rejects(collectAuditedFrameworkBenchmark(root, override), /benchmark/u);
  }
  await assert.rejects(collectAuditedFrameworkBenchmark(resolve(root, "..")), /benchmark/u);
  assert.equal(calls, 1);
});

test("collector sanitizes failed child output and rejects unexpected stderr", async (t) => {
  t.mock.method(childProcess, "execFile", (_file: string, _args: string[], _options: any, callback: Function) => callback(new Error("C:\\secret TOKEN=private"), "raw", "raw"));
  await assert.rejects(collectAuditedFrameworkBenchmark(root), (error: Error) => {
    assert.equal(error.message, "framework benchmark collection failed");
    assert.equal(error.cause, undefined);
    assert.doesNotMatch(error.stack!, /private|secret|native-framework/u);
    return true;
  });
  t.mock.method(childProcess, "execFile", (_file: string, _args: string[], _options: any, callback: Function) => callback(null, output, "private warning"));
  await assert.rejects(collectAuditedFrameworkBenchmark(root), /framework benchmark collection failed/u);
});

test("collector rejects mutable benchmark source before executing any command", async (t) => {
  t.mock.method(fs, "readFile", async () => "substituted benchmark fixture");
  syncBuiltinESMExports();
  t.after(() => { t.mock.restoreAll(); syncBuiltinESMExports(); });
  let called = false;
  t.mock.method(childProcess, "execFile", () => { called = true; throw new Error("must not run"); });
  await assert.rejects(collectAuditedFrameworkBenchmark(root), /benchmark/u);
  assert.equal(called, false);
});
