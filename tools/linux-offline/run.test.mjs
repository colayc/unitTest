import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import test from "node:test";
import { offlineEnvironment, offlineSudoArguments, offlineUnshareArguments, selectOfflineLauncher } from "./run.mjs";

test("Linux offline runner removes proxy and package-manager network inputs before child launch", () => {
  const environment = offlineEnvironment({
    HTTP_PROXY: "http://proxy.invalid",
    npm_config_registry: "https://registry.invalid",
    SAFE: "1"
  });
  assert.deepEqual(environment, { SAFE: "1", NO_PROXY: "*", no_proxy: "*", npm_config_offline: "true" });
  assert.deepEqual(offlineUnshareArguments(["node", "child.mjs"]), ["--user", "--map-root-user", "--net", "--mount-proc", "--fork", "--", "node", "child.mjs"]);
});

test("Linux offline launcher prefers rootless isolation and requires explicit authorization before sudo isolation", () => {
  const rootlessProbe = { executable: "unshare", arguments: ["--user", "--map-root-user", "--net", "--mount-proc", "--fork", "--", "true"] };
  const rootlessRun = { executable: "unshare", arguments: ["--user", "--map-root-user", "--net", "--mount-proc", "--fork", "--", "node", "child.mjs"], mode: "rootless" };
  const sudoProbe = { executable: "sudo", arguments: ["--non-interactive", "--preserve-env", "--", "unshare", "--net", "--mount-proc", "--fork", "--", "setpriv", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--reuid=1001", "--regid=121", "--clear-groups", "--", "/usr/bin/env", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "true"] };
  const sudoRun = { executable: "sudo", arguments: ["--non-interactive", "--preserve-env", "--", "unshare", "--net", "--mount-proc", "--fork", "--", "setpriv", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--reuid=1001", "--regid=121", "--clear-groups", "--", "/usr/bin/env", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "node", "child.mjs"], mode: "sudo-root" };
  assert.deepEqual(offlineSudoArguments(["node", "child.mjs"], 1001, 121), sudoRun.arguments);
  assert.ok(offlineSudoArguments(["node"], 1001, 121, "/opt/pnpm:/usr/bin").includes("PATH=/opt/pnpm:/usr/bin"));
  const rootlessProbes = [];
  const rootless = selectOfflineLauncher(["node", "child.mjs"], {
    allowSudoRoot: true,
    uid: 1001,
    gid: 121,
    probe: (candidate) => { rootlessProbes.push(candidate); return true; }
  });
  assert.deepEqual(rootless, rootlessRun);
  assert.deepEqual(rootlessProbes, [rootlessProbe]);

  const deniedProbes = [];
  assert.throws(() => selectOfflineLauncher(["node", "child.mjs"], {
    uid: 1001,
    gid: 121,
    probe: (candidate) => { deniedProbes.push(candidate); return false; }
  }), /explicitly authorized sudo network namespace/u);
  assert.equal(deniedProbes.length, 1, "sudo must not be probed without the explicit CLI authorization");

  const sudoProbes = [];
  const sudo = selectOfflineLauncher(["node", "child.mjs"], {
    allowSudoRoot: true,
    uid: 1001,
    gid: 121,
    probe: (candidate) => { sudoProbes.push(candidate); return candidate.executable === "sudo"; }
  });
  assert.deepEqual(sudo, sudoRun);
  assert.deepEqual(sudoProbes, [rootlessProbe, sudoProbe]);
});

test("Linux workflow prepares downloads before entering a namespace and wraps final native E2E", async () => {
  const workflow = await readFile(resolve(import.meta.dirname, "..", "..", ".github", "workflows", "foundation.yml"), "utf8");
  const extensionPackage = await readFile(resolve(import.meta.dirname, "..", "..", "apps", "code-oss-extension", "package.json"), "utf8");
  const linuxStart = workflow.indexOf("  verify-linux:");
  const linuxEnd = workflow.indexOf("\n  verify-framework-matrix:", linuxStart);
  const linuxJob = workflow.slice(linuxStart, linuxEnd);
  const coveragePrepare = linuxJob.indexOf("- run: pnpm prepare:coverage-bundle");
  const frameworkPrepare = linuxJob.indexOf("- name: Prepare locked Linux framework inputs");
  const goModuleDownload = linuxJob.indexOf("- name: Prepare Go module cache before Linux offline namespace");
  const namespaceProbe = linuxJob.indexOf("Verify fail-closed Linux offline namespace");
  const frameworkProducer = linuxJob.indexOf('node tools/linux-offline/run.mjs --allow-sudo-root -- pnpm prepare:native-framework-runtime -- --platform linux --candidate "${{ github.sha }}"');
  const nativeMatrix = linuxJob.indexOf("node tools/linux-offline/run.mjs --allow-sudo-root -- pnpm test:e2e:native");
  assert.ok(coveragePrepare !== -1 && frameworkPrepare !== -1 && goModuleDownload !== -1 && namespaceProbe !== -1 && coveragePrepare < namespaceProbe && frameworkPrepare < namespaceProbe && goModuleDownload < namespaceProbe);
  assert.ok(frameworkProducer > goModuleDownload && nativeMatrix > frameworkProducer, "Linux producer must run offline after dependency preparation and before native execution");
  assert.match(workflow, /node tools\/linux-offline\/run\.mjs --allow-sudo-root -- pnpm verify/u);
  assert.match(
    linuxJob,
    /^ {6}- name: Run required Linux framework matrix offline\r?\n {8}env:\r?\n {10}UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS: gcc,clang\r?\n {10}UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED: '1'\r?\n {10}UTIDE_KEEP_FRAMEWORK_FAILURE: '1'\r?\n {10}UTIDE_DEBUG_FRAMEWORK: '1'\r?\n {10}UT_DEBUG_PROCESS_HOST_FAILURES: '1'\r?\n {8}run: node tools\/linux-offline\/run\.mjs --allow-sudo-root -- pnpm test:e2e:native\s*$/mu,
  );
  assert.match(
    linuxJob,
    /^ {6}- name: Upload P4 Linux framework report\r?\n {8}uses: actions\/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7\r?\n {8}with:\r?\n {10}name: native-framework-linux\r?\n {10}path: \.native-e2e\/artifacts\/linux\/framework-report\.json\r?\n {10}if-no-files-found: error\r?\n {10}retention-days: 14\s*$/mu,
  );
  for (const command of [
    "go test ./apps/test-service/... -count=1",
    "go test -race ./apps/test-service/internal/coverageplatform ./apps/test-service/internal/coveragegcc ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/coverageexec -count=1",
    "go vet ./apps/test-service/...",
    "pnpm --filter @unit-test-ide/service-probe test",
    "pnpm --filter code-oss-extension test:coverage-service-smoke:linux",
    "pnpm --filter code-oss-extension validate:coverage-evidence:linux"
  ]) {
    assert.match(workflow, new RegExp(`node tools/linux-offline/run\\.mjs --allow-sudo-root -- ${command.replace(/[.*+?^${}()|[\\]\\]/g, "\\$&")}`), `native command is outside the explicit offline launcher: ${command}`);
  }
  assert.doesNotMatch(extensionPackage, /linux-offline[\\/]run\.mjs/u, "Linux smoke package must not nest a second offline launcher");
  assert.doesNotMatch(workflow, /\n\s*- run: pnpm test:e2e:native\n/u);
});
