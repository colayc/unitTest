import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const goImportAuditTool = "./tools/workspace-smoke/go-import-audit.go";
const serviceCommandPackage = "./apps/test-service/cmd/unit-test-service";
const serviceProductionRoot = "apps/test-service";
const currentServicePlatform = process.platform === "win32" ? "windows" : "linux";
const winioImportPath = "github.com/Microsoft/go-winio";
const pureParsingImports = new Set(["net/url"]);
const allowedProductionNetworkImports = new Map([
  ["apps/test-service/internal/offlineboundary/guardian_windows.go", new Set(["net", winioImportPath])],
  ["apps/test-service/internal/offlineboundary/registration_windows.go", new Set(["net", winioImportPath])],
  ["apps/test-service/internal/server/server.go", new Set(["net"])],
  ["apps/test-service/internal/server/service.go", new Set(["net"])],
  ["apps/test-service/internal/transport/listener.go", new Set(["net"])],
  ["apps/test-service/internal/transport/listener_unix.go", new Set(["net"])],
  ["apps/test-service/internal/transport/listener_windows.go", new Set(["net", winioImportPath])]
]);

function goList(arguments_) {
  return execFileSync("go", arguments_, {
    encoding: "utf8",
    windowsHide: true
  }).trim().split(/\r?\n/).filter(Boolean);
}

function goImportAudit(paths) {
  return JSON.parse(execFileSync("go", ["run", goImportAuditTool, "--", ...paths], {
    encoding: "utf8",
    windowsHide: true
  }));
}

function isNetworkCapableImport(importPath) {
  return !pureParsingImports.has(importPath)
    && (importPath === "net"
      || importPath.startsWith("net/")
      || importPath === "crypto/tls"
      || importPath.startsWith("github.com/google/go-github")
      || importPath.startsWith("golang.org/x/oauth2")
      || importPath === "golang.org/x/net"
      || importPath.startsWith("golang.org/x/net/")
      || importPath === winioImportPath);
}

function productionNetworkImportEscapes(records) {
  return records.filter(({ filename, path, alias }) =>
    isNetworkCapableImport(path)
    && (alias !== "" || !allowedProductionNetworkImports.get(filename)?.has(path))
  );
}

function calledSelectors(source, packageName) {
  return [...source.matchAll(new RegExp(`\\b${packageName}\\.([A-Za-z_]\\w*)\\s*\\(`, "g"))]
    .map((match) => match[1]);
}

async function productionGoSources(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  return Promise.all(entries
    .filter((entry) => entry.isFile() && entry.name.endsWith(".go") && !entry.name.endsWith("_test.go"))
    .map(async (entry) => ({
      filename: entry.name,
      source: await readFile(`${directory}/${entry.name}`, "utf8")
    })));
}

test("workspace pins supported toolchains", async () => {
  assert.equal((await readFile(".node-version", "utf8")).trim(), "24.18.0");
  assert.equal((await readFile(".go-version", "utf8")).trim(), "1.26.6");
  const manifest = JSON.parse(await readFile("package.json", "utf8"));
  assert.equal(manifest.packageManager, "pnpm@11.4.0");
  assert.equal(manifest.engines.node, ">=24.18.0 <25");
});

test("README contains the complete local verification gate", async () => {
  const readme = await readFile("README.md", "utf8");
  for (const command of ["pnpm check:protocol-generated", "pnpm test", "pnpm test:e2e"]) {
    assert.match(readme, new RegExp(command.replaceAll(" ", "\\s+")));
  }
});

test("Phase 3 documentation records desktop, bundle, native, and security boundaries", async () => {
  const readme = await readFile("README.md", "utf8");
  const development = await readFile("docs/development.md", "utf8");
  const security = await readFile("docs/security.md", "utf8");
  const bundle = await readFile("docs/cmake-bundle.md", "utf8");
  const native = await readFile("docs/native-e2e.md", "utf8");
  assert.match(readme, /Code-OSS desktop/);
  assert.match(readme, /最终用户运行产品不必连接 GitHub/);
  assert.doesNotMatch(readme, /不会执行工作区代码、CMake/);
  assert.match(development, /pnpm install --frozen-lockfile --offline/);
  assert.match(security, /Protocol request[\s\S]*executable[\s\S]*raw args[\s\S]*environment[\s\S]*cwd/);
  assert.match(bundle, /archive SHA-256[\s\S]*license[\s\S]*installed-file SHA-256/);
  assert.match(native, /windows-2025-vs2026[\s\S]*ubuntu-24\.04/);
  assert.match(native, /Phase 5[\s\S]*llvm-cov/);
  assert.match(native, /Phase 8[\s\S]*签名安装包/);
});

test("Hosted CI pins native toolchain runners and enforces the complete matrix", async () => {
  const workflow = await readFile(".github/workflows/foundation.yml", "utf8");
  assert.doesNotMatch(workflow, /\b(?:windows|ubuntu)-latest\b/);
  assert.match(workflow, /^\s{2}verify-windows:\s*$/m);
  assert.match(workflow, /^\s{4}runs-on:\s*windows-2025-vs2026\s*$/m);
  assert.match(
    workflow,
    /^\s{6}UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS:\s*msvc,clang-cl\s*$/m,
  );
  assert.match(workflow, /^\s{2}verify-linux:\s*$/m);
  assert.match(workflow, /^\s{4}runs-on:\s*ubuntu-24\.04\s*$/m);
  assert.match(
    workflow,
    /^\s{6}UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS:\s*gcc,clang\s*$/m,
  );

  for (const [job, platform] of [
    ["verify-windows", "windows"],
    ["verify-linux", "linux"],
  ]) {
    const start = workflow.indexOf(`  ${job}:`);
    assert.notEqual(start, -1);
    const nextJob = workflow.indexOf("\n  verify-", start + 3);
    const source = workflow.slice(start, nextJob === -1 ? undefined : nextJob);
    const verify = source.indexOf("pnpm verify");
    const prepare = source.indexOf("pnpm prepare:cmake-bundle");
    const native = source.indexOf("pnpm test:e2e:native");
    assert.ok(verify !== -1 && prepare > verify && native > prepare);
    assert.match(source, /uses:\s*actions\/upload-artifact@v7/);
    assert.match(source, /if:\s*always\(\)/);
    assert.match(
      source,
      new RegExp(`path:\\s*\\.native-e2e/artifacts/${platform}/toolchain-report\\.json`),
    );
    assert.match(source, /run:\s*git diff --exit-code/);

    if (platform === "windows") {
      const privilegedWfp = source.indexOf("TestPrivilegedWindowsWFPDynamicLifecycle");
      const typescriptWfp = source.indexOf("test:wfp-integration");
      const coverageSmoke = source.indexOf("test:coverage-service-smoke");
      const legacyCleanup = source.indexOf("Legacy cleanup Windows offline boundary residue");
      const serviceSmoke = source.indexOf("test:service-smoke");
      assert.ok(
        coverageSmoke !== -1 && legacyCleanup > coverageSmoke && serviceSmoke > legacyCleanup,
        "Windows CI must keep coverage smoke ahead of legacy residue cleanup and later Service checks"
      );
      assert.ok(
        privilegedWfp !== -1 && typescriptWfp > privilegedWfp && coverageSmoke > typescriptWfp,
        "required privileged Go and TypeScript WFP integration must run before coverage Service/native smoke"
      );
      const privilegedStep = source.slice(source.lastIndexOf("      - ", privilegedWfp), coverageSmoke);
      assert.match(privilegedStep, /UNIT_TEST_IDE_WFP_INTEGRATION_REQUIRED:\s*["']?1["']?/u);
      const coverageReport = source.indexOf("coverage-execution-report.json");
      assert.notEqual(coverageReport, -1);
      const uploadStep = source.slice(source.lastIndexOf("      - ", coverageReport), coverageReport);
      assert.match(uploadStep, /steps\.windows-coverage-smoke\.outcome\s*==\s*'success'/u, "WFP evidence upload must depend on the required verified smoke step");
      assert.match(source, /coverage-execution-windows-[\s\S]*if-no-files-found:\s*error/u, "required verified Windows runs must fail closed without evidence");
      const cleanupStep = source.slice(source.lastIndexOf("      - ", legacyCleanup), serviceSmoke);
      assert.match(cleanupStep, /windows-offline-boundary\.ps1/u);
      assert.match(cleanupStep, /-Action\s+LegacyCleanup/u);
      assert.match(cleanupStep, /Join-Path\s+\$PWD\s+'\.native-e2e\/runtime\/windows-firewall-guardians'/u);
      assert.match(cleanupStep, /-StateRoot\s+\$stateRoot/u);
      assert.doesNotMatch(source, /-Action\s+Guard/u, "production CI must not revive the legacy PowerShell boundary");
      assert.doesNotMatch(cleanupStep, /CleanupAll/u, "legacy cleanup must be bounded to known historical residue only");
    }
  }
});

test("dependency metadata uses the official npm registry", async () => {
  assert.equal((await readFile(".npmrc", "utf8")).trim(), "registry=https://registry.npmjs.org/");
  assert.doesNotMatch(await readFile("pnpm-lock.yaml", "utf8"), /registry\.npmmirror\.com/);
});

test("documentation records pre-write token file preparation", async () => {
  const readme = await readFile("README.md", "utf8");
  const decision = await readFile("docs/decisions/0001-local-ipc-and-protocol-v1.md", "utf8");
  assert.match(readme, /--prepare-token-file/);
  assert.match(decision, /--prepare-token-file/);
  assert.doesNotMatch(readme, /removes inherited Windows permissions after writing/i);
});

test("compiled Service runtime excludes HTTP, TLS, GitHub, and OAuth client stacks", () => {
  const dependencies = goList(["list", "-deps", serviceCommandPackage]);
  const forbidden = dependencies.filter((dependency) =>
    dependency === "net/http"
    || dependency.startsWith("net/http/")
    || dependency === "crypto/tls"
    || dependency.startsWith("github.com/google/go-github")
    || dependency.startsWith("golang.org/x/oauth2")
  );
  assert.deepEqual(forbidden, [], `${currentServicePlatform} Service dependency graph contains outbound client stacks`);
});

test("Go import audit parses commented raw and aliased import specs before policy checks", async () => {
  const directory = await mkdtemp(join(tmpdir(), "go-import-audit-"));
  const filename = join(directory, "mutant.go");
  try {
    await writeFile(filename, [
      "package mutant",
      "",
      "import (",
      "  netx `net` // trailing comment",
      '  . "net/http" // trailing comment',
      ")",
      "",
      "var _ netx.Conn",
      "var _ = MethodGet",
      ""
    ].join("\n"));
    const records = goImportAudit([filename]);
    const normalizedFilename = filename.replaceAll("\\", "/");
    assert.deepEqual(records, [
      { filename: normalizedFilename, path: "net", alias: "netx" },
      { filename: normalizedFilename, path: "net/http", alias: "." }
    ]);
    assert.deepEqual(
      productionNetworkImportEscapes(records),
      records,
      "named and dot aliases in unknown files must fail production import policy"
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("network import policy permits pure URL parsing but still rejects transport stacks", () => {
  assert.equal(isNetworkCapableImport("net/url"), false);
  for (const importPath of ["net", "net/http", "net/http/httptest", "crypto/tls"]) {
    assert.equal(isNetworkCapableImport(importPath), true, `${importPath} must remain network-capable`);
  }
});

test("production packages constrain network-capable code to local IPC boundaries", async () => {
  const productionImports = goImportAudit([serviceProductionRoot]);
  assert.deepEqual(
    productionNetworkImportEscapes(productionImports),
    [],
    "production Go files import network-capable code outside the filename-level local IPC allowlist"
  );

  const serverSources = await productionGoSources("apps/test-service/internal/server");
  const allowedServerNetSelectors = new Set(["Addr", "Conn", "ErrClosed", "Error", "Listener"]);
  const escapedServerSelectors = serverSources.flatMap(({ filename, source }) =>
    [...source.matchAll(/\bnet\.([A-Za-z_]\w*)/g)]
      .map((match) => ({ filename, selector: match[1] }))
      .filter(({ selector }) => !allowedServerNetSelectors.has(selector))
  );
  assert.deepEqual(
    escapedServerSelectors,
    [],
    "internal/server must use net only for local IPC connection, interface, type, and error handling"
  );
});

test("cross-platform transports constrain network-capable calls to local IPC", async () => {
  const transportSources = await productionGoSources("apps/test-service/internal/transport");
  const allowedSelectors = new Map([
    ["listener.go", { net: ["Listener"], winio: [] }],
    ["listener_unix.go", { net: ["Listener", "DialTimeout", "Listen"], winio: [] }],
    ["listener_windows.go", { net: ["Listener"], winio: ["ListenPipe", "PipeConfig"] }]
  ]);
  for (const { filename, source } of transportSources) {
    const selectors = allowedSelectors.get(filename) ?? { net: [], winio: [] };
    assert.deepEqual(
      [...source.matchAll(/\bnet\.([A-Za-z_]\w*)/g)].map((match) => match[1]),
      selectors.net,
      `${filename} uses net outside its local IPC boundary`
    );
    assert.deepEqual(
      [...source.matchAll(/\bwinio\.([A-Za-z_]\w*)/g)].map((match) => match[1]),
      selectors.winio,
      `${filename} uses go-winio outside its local IPC boundary`
    );
  }

  const unixSource = transportSources.find(({ filename }) => filename === "listener_unix.go")?.source;
  const windowsSource = transportSources.find(({ filename }) => filename === "listener_windows.go")?.source;
  assert.ok(unixSource, "Unix transport source is missing");
  assert.ok(windowsSource, "Windows transport source is missing");
  assert.deepEqual(calledSelectors(unixSource, "net"), ["DialTimeout", "Listen"]);
  assert.deepEqual(
    [...unixSource.matchAll(/\bnet\.(DialTimeout|Listen)\(\s*"([^"]+)"/g)]
      .map((match) => ({ call: match[1], network: match[2] })),
    [
      { call: "DialTimeout", network: "unix" },
      { call: "Listen", network: "unix" }
    ],
    "Unix transport must dial and listen only on Unix sockets"
  );
  assert.deepEqual(calledSelectors(windowsSource, "net"), []);
  assert.deepEqual(
    calledSelectors(windowsSource, "winio"),
    ["ListenPipe"],
    "Windows transport must only listen on a local Named Pipe"
  );
});

test("guardian Windows boundary constrains network-capable selectors to local IPC only", async () => {
  const source = await readFile("apps/test-service/internal/offlineboundary/guardian_windows.go", "utf8");
  assert.deepEqual(
    [...source.matchAll(/\bnet\.([A-Za-z_]\w*)/g)].map((match) => match[1]),
    ["Listener", "Conn", "Conn", "Conn", "Conn", "Conn"],
    "guardian_windows.go must use net only for local pipe listener/connection types"
  );
  assert.deepEqual(
    [...source.matchAll(/\bwinio\.([A-Za-z_]\w*)/g)].map((match) => match[1]),
    ["ListenPipe", "PipeConfig", "DialPipeContext"],
    "guardian_windows.go must use go-winio only for local Named Pipe setup/dial"
  );
});

test("guardian executable registration constrains network-capable selectors to local IPC only", async () => {
  const source = await readFile("apps/test-service/internal/offlineboundary/registration_windows.go", "utf8");
  assert.deepEqual(
    [...source.matchAll(/\bnet\.([A-Za-z_]\w*)/g)].map((match) => match[1]),
    ["Listener", "Conn"],
    "registration_windows.go must use net only for local pipe listener/connection types"
  );
  assert.deepEqual(
    [...source.matchAll(/\bwinio\.([A-Za-z_]\w*)/g)].map((match) => match[1]),
    ["ListenPipe", "PipeConfig", "DialPipeContext"],
    "registration_windows.go must use go-winio only for local Named Pipe setup/dial"
  );
  assert.deepEqual(
    calledSelectors(source, "winio"),
    ["ListenPipe", "DialPipeContext"],
    "executable registration must only listen and dial on local Named Pipes"
  );
});
