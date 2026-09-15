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
  assert.equal(
    manifest.scripts["prepare:release-manifest"],
    "node tools/release/manifest.mjs --config tools/release/release-config.json",
  );
});

test("Phase 9 performance baseline job contract is fixed and reviewed", async () => {
  const workflow = await readFile(".github/workflows/phase9-gates.yml", "utf8");
  const start = workflow.indexOf("  phase9-performance:");
  assert.notEqual(start, -1, "phase9-performance job is missing");
  const remainder = workflow.slice(start + 1);
  const next = remainder.search(/\r?\n {2}[a-z][a-z0-9-]*:\s*$/mu);
  const job = workflow.slice(start, next === -1 ? undefined : start + 1 + next);
  assert.match(job, /^ {4}runs-on: ubuntu-24\.04\s*$/mu);
  assert.match(job, /^ {4}timeout-minutes: 30\s*$/mu);
  assert.match(job, /^ {6}- run: pnpm install --frozen-lockfile\s*$/mu);
  assert.match(job, /^ {6}- run: pnpm test:phase9:performance\s*$/mu);
  assert.match(job, /^ {6}- run: node tools\/phase9\/performance\.mjs --out \.superpowers\/phase9\/performance\/baseline\.json\s*$/mu);
  assert.match(job, /^ {10}node-version: 24\.18\.0\s*$/mu);
  assert.match(job, /^ {10}cache: pnpm\s*$/mu);
  assert.match(job, /^ {10}name: phase9-performance-baseline\s*$/mu);
  assert.match(job, /^ {10}path: \.superpowers\/phase9\/performance\/baseline\.json\s*$/mu);
  assert.match(job, /^ {10}if-no-files-found: error\s*$/mu);
  assert.match(job, /^ {10}retention-days: 14\s*$/mu);
  for (const sha of [
    "actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803",
    "actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38",
    "pnpm/action-setup@f40ffcd9367d9f12939873eb1018b921a783ffaa",
    "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
  ]) assert.match(job, new RegExp(sha.replaceAll("@", "\\@")));
});

test("Phase 9 audit workflow is read-only, fixed-coordinate, and fail-closed", async () => {
  const workflow = await readFile(".github/workflows/phase9-gates.yml", "utf8");

  assert.match(
    workflow,
    /^on:\r?\n {2}pull_request:\r?\n {2}push:\r?\n {4}branches: \[master\]\r?\n {2}workflow_dispatch:\s*$/mu,
  );
  assert.deepEqual(workflow.match(/^permissions:\s*$/gmu), ["permissions:"]);
  assert.match(workflow, /permissions:\r?\n {2}actions: read\r?\n {2}contents: read/u);
  assert.match(workflow, /^ {4}runs-on: ubuntu-24\.04\s*$/mu);
  assert.match(workflow, /^ {4}timeout-minutes: 15\s*$/mu);
  assert.doesNotMatch(workflow, /secrets\./u);
  assert.doesNotMatch(workflow, /repository_dispatch|workflow_call/u);
  assert.doesNotMatch(workflow, /^\s*inputs:\s*$/mu);
  assert.doesNotMatch(workflow, /continue-on-error/u);
  assert.doesNotMatch(workflow, /\beval\b|\bsh\s+-c\b/u);

  const jobSource = (name) => {
    const start = workflow.indexOf(`  ${name}:`);
    assert.notEqual(start, -1, `Phase 9 execution job ${name} is missing`);
    const remainder = workflow.slice(start + 1);
    const next = remainder.search(/\r?\n {2}[a-z][a-z0-9-]*:\s*$/mu);
    return workflow.slice(start, next === -1 ? undefined : start + 1 + next);
  };
  const setupPins = [
    "actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803",
    "actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38",
    "pnpm/action-setup@f40ffcd9367d9f12939873eb1018b921a783ffaa",
  ];
  const setupGoPin = "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16";
  for (const [name, commands] of [
    ["phase9-offline", ["node --test tools/phase9/validate.test.mjs tools/phase9/audit.test.mjs"]],
    ["phase9-matrix-e2e", ["pnpm test:e2e"]],
    ["phase9-fault-injection", [
      "pnpm prepare:cmake-bundle",
      "go mod download",
      "node tools/linux-offline/run.mjs --allow-sudo-root -- pnpm test:e2e:native",
    ]],
    ["phase9-upstream-codeoss", ["pnpm test:workspace"]],
  ]) {
    const source = jobSource(name);
    assert.match(source, new RegExp(`^  ${name}:\\s*$`, "mu"), `${name} must retain its exact job name`);
    assert.match(source, /^ {4}runs-on: ubuntu-24\.04\s*$/mu, `${name} must use the fixed Ubuntu runner`);
    assert.match(source, /^ {4}timeout-minutes: 30\s*$/mu, `${name} must use the fixed timeout`);
    assert.doesNotMatch(source, /^\s+inputs:/mu, `${name} must not accept dynamic inputs`);
    assert.doesNotMatch(source, /secrets\.|\$\{\{/u, `${name} must not interpolate dynamic data`);
    for (const pin of setupPins) {
      assert.equal(source.split(pin).length - 1, 1, `${name} must use ${pin} exactly once`);
    }
    assert.match(source, /^ {10}fetch-depth: 0\s*$/mu, `${name} must fetch fixed checkout history`);
    assert.match(source, /^ {10}persist-credentials: false\s*$/mu, `${name} checkout must not persist credentials`);
    assert.match(source, /^ {10}node-version: 24\.18\.0\s*$/mu, `${name} must use the pinned Node version`);
    assert.match(source, /^ {10}cache: pnpm\s*$/mu, `${name} must use the pnpm cache`);
    if (name === "phase9-fault-injection") {
      assert.equal(source.split(setupGoPin).length - 1, 1, `${name} must use pinned setup-go exactly once`);
      assert.match(
        source,
        /^ {6}- uses: actions\/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6\r?\n {8}with:\r?\n {10}go-version: 1\.26\.6\r?\n {10}cache-dependency-path: apps\/test-service\/go\.sum\s*$/mu,
      );
      assert.equal([...source.matchAll(/^[ \t]+env:/gmu)].length, 2, `${name} must have exactly two static environment blocks`);
      assert.match(
        source,
        /^ {6}- name: Prepare Go modules before namespace entry\r?\n {8}working-directory: apps\/test-service\r?\n {8}env:\r?\n {10}GOENV: "off"\r?\n {10}GOTOOLCHAIN: local\r?\n {8}run: go mod download\s*$/mu,
      );
      assert.match(
        source,
        /^ {6}- name: Run native fault injection offline\r?\n {8}env:\r?\n {10}GOENV: "off"\r?\n {10}GOTOOLCHAIN: local\r?\n {8}run: node tools\/linux-offline\/run\.mjs --allow-sudo-root -- pnpm test:e2e:native\s*$/mu,
      );
      assert.ok(source.indexOf(setupGoPin) < source.indexOf("go mod download"), `${name} must set up Go before downloading modules`);
    } else {
      assert.equal(source.split(setupGoPin).length - 1, 0, `${name} must not set up Go`);
      assert.doesNotMatch(source, /^\s+env:/mu, `${name} must not define an environment`);
    }
    assert.deepEqual(
      [...source.matchAll(/^[ \t]+(?:-[ \t]+)?run:[ \t]*(.*?)[ \t]*$/gmu)].map((match) => match[1]),
      ["pnpm install --frozen-lockfile", ...commands],
      `${name} must install from the lockfile and run only its exact fixed command`,
    );
  }
  assert.equal(workflow.split(setupGoPin).length - 1, 1, `${setupGoPin} must appear exactly once`);

  for (const [pin, expectedCount] of [
    ["actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803", 6],
    ["actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38", 6],
    ["pnpm/action-setup@f40ffcd9367d9f12939873eb1018b921a783ffaa", 6],
    ["actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a", 2],
  ]) {
    assert.equal(workflow.split(pin).length - 1, expectedCount, `${pin} must appear exactly ${expectedCount} times`);
  }
  assert.match(workflow, /fetch-depth: 0/u);
  assert.match(workflow, /persist-credentials: false/u);
  assert.match(workflow, /node-version: 24\.18\.0/u);
  assert.match(workflow, /pnpm install --frozen-lockfile/u);

  const validation = workflow.indexOf("node tools/phase9/validate.mjs");
  const requestsOutput = workflow.indexOf("--requests-out .superpowers/phase9/requests.json");
  const firstApi = workflow.indexOf('gh api "repos/colayc/unitTest/actions/runs/$run_id"');
  const runIdGuard = workflow.indexOf('[[ "$run_id" =~ ^[1-9][0-9]*$ ]]');
  const umask = workflow.indexOf("umask 077");
  assert.ok(validation >= 0, "offline Phase 9 validation is missing");
  assert.ok(requestsOutput > validation, "validation must write the fixed request file");
  assert.ok(umask >= 0 && umask < firstApi, "umask 077 must precede every GitHub API call");
  assert.ok(runIdGuard >= 0 && runIdGuard < firstApi, "canonical run-ID validation must precede every GitHub API call");
  for (const endpoint of [
    'gh api "repos/colayc/unitTest/actions/runs/$run_id" > ".superpowers/phase9/snapshots/$run_id/run.json"',
    'gh api "repos/colayc/unitTest/actions/runs/$run_id/jobs?per_page=100" > ".superpowers/phase9/snapshots/$run_id/jobs.json"',
    'gh api "repos/colayc/unitTest/actions/runs/$run_id/artifacts?per_page=100" > ".superpowers/phase9/snapshots/$run_id/artifacts.json"',
  ]) {
    assert.match(workflow, new RegExp(endpoint.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&"), "u"));
  }
  assert.match(workflow, /GH_TOKEN: \$\{\{ github\.token \}\}/u);

  assert.match(workflow, /node tools\/phase9\/audit\.mjs/u);
  assert.match(workflow, /--recorded-matrix \.superpowers\/phase9\/recorded-matrix\.json/u);
  assert.doesNotMatch(workflow, /--recorded-matrix docs\/superpowers\/evidence\/phase9\/gate-matrix\.json/u);
  assert.match(workflow, /--receipts docs\/superpowers\/evidence\/phase9\/receipts/u);
  assert.match(workflow, /--snapshots \.superpowers\/phase9\/snapshots/u);
  assert.match(workflow, /--json-out \.superpowers\/phase9\/audit\/phase9-gate-matrix\.json/u);
  assert.match(workflow, /--markdown-out \.superpowers\/phase9\/audit\/phase9-gate-matrix\.md/u);

  const uploadStart = workflow.indexOf("- name: Upload Phase 9 audit evidence");
  const upload = workflow.slice(uploadStart);
  assert.ok(uploadStart >= 0, "audit upload step is missing");
  assert.match(upload, /if: always\(\)/u);
  assert.match(upload, /name: phase9-gate-audit-\$\{\{ github\.run_attempt \}\}/u);
  assert.match(upload, /\.superpowers\/phase9\/audit\/phase9-gate-matrix\.json/u);
  assert.match(upload, /\.superpowers\/phase9\/audit\/phase9-gate-matrix\.md/u);
  assert.match(upload, /if-no-files-found: error/u);
  assert.match(upload, /retention-days: 14/u);
});

test("P4 native framework matrix workflow is opt-in, fixed, and fail-closed until real reports exist", async () => {
  const workflow = await readFile(".github/workflows/foundation.yml", "utf8");
  const start = workflow.indexOf("  verify-framework-matrix:");
  assert.notEqual(start, -1, "verify-framework-matrix job is missing");
  const remainder = workflow.slice(start + 1);
  const next = remainder.search(/\r?\n {2}[a-z][a-z0-9-]*:\s*$/mu);
  const job = workflow.slice(start, next === -1 ? undefined : start + 1 + next);

  assert.match(job, /^ {4}if: \$\{\{ vars\.UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED == '1' \}\}\s*$/mu);
  assert.match(job, /^ {4}needs:\r?\n {6}- verify-linux\r?\n {6}- verify-windows\s*$/mu);
  assert.match(job, /^ {4}runs-on: ubuntu-24\.04\s*$/mu);
  assert.match(job, /^ {4}timeout-minutes: 30\s*$/mu);
  assert.doesNotMatch(job, /secrets\.|continue-on-error|workflow_dispatch|inputs:/u);
  for (const pin of [
    "actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803",
    "pnpm/action-setup@f40ffcd9367d9f12939873eb1018b921a783ffaa",
    "actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38",
    "actions/download-artifact@d3f86a106a0bac45b974a628896c90dbdf5c8093",
    "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
  ]) assert.match(job, new RegExp(pin));
  for (const artifact of ["native-framework-linux", "native-framework-windows", "native-framework-matrix-report"]) {
    assert.match(job, new RegExp(`^ {10}name: ${artifact}\\s*$`, "mu"));
  }
  assert.match(job, /^ {6}- run: pnpm install --frozen-lockfile\s*$/mu);
  assert.match(
    job,
    /node tools\/phase9\/p4-report\.mjs \\\r?\n {12}--windows \.native-e2e\/framework-inputs\/windows\/framework-report\.json \\\r?\n {12}--linux \.native-e2e\/framework-inputs\/linux\/framework-report\.json \\\r?\n {12}--candidate "\$GITHUB_SHA" \\\r?\n {12}--out \.superpowers\/phase9\/p4\/native-framework-matrix-report\.json/u,
  );
  assert.match(job, /^ {10}path: \.superpowers\/phase9\/p4\/native-framework-matrix-report\.json\s*$/mu);

  for (const [platform, path] of [
    ["windows", ".native-e2e/artifacts/windows/framework-report.json"],
    ["linux", ".native-e2e/artifacts/linux/framework-report.json"],
  ]) {
    assert.match(workflow, new RegExp(
      `^ {6}- name: Upload P4 ${platform === "windows" ? "Windows" : "Linux"} framework report\\r?\\n`
      + ` {8}if: \\$\\{\\{ always\\(\\) && vars\\.UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED == '1' \\}\\}\\r?\\n`
      + ` {8}uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7\\r?\\n`
      + ` {8}with:\\r?\\n {10}name: native-framework-${platform}\\r?\\n {10}path: ${path}\\r?\\n`
      + " {10}if-no-files-found: error\\r?\\n {10}retention-days: 14\\s*$",
      "mu",
    ));
  }
});

test("root verification runs Phase 9 contracts and preserves the exact deferred boundary", async () => {
  const [manifest, registry, matrix] = await Promise.all([
    readFile("package.json", "utf8").then(JSON.parse),
    readFile("tools/phase9/gates.json", "utf8").then(JSON.parse),
    readFile("docs/superpowers/evidence/phase9/gate-matrix.json", "utf8").then(JSON.parse),
  ]);
  assert.equal(
    manifest.scripts["check:phase9-gates"],
    "node tools/phase9/render.mjs --registry tools/phase9/gates.json --baseline docs/superpowers/evidence/phase9/baseline.json --receipts docs/superpowers/evidence/phase9/receipts --repository-root . --json-out docs/superpowers/evidence/phase9/gate-matrix.json --markdown-out docs/superpowers/evidence/phase9/gate-matrix.md --check",
  );
  assert.equal(
    manifest.scripts["test:phase9-gates"],
    "node --test tools/phase9/validate.test.mjs tools/phase9/audit.test.mjs",
  );
  assert.ok(
    manifest.scripts.test.indexOf("pnpm run test:phase9-gates")
      < manifest.scripts.test.indexOf("pnpm run test:workspace"),
    "Phase 9 tests must run before workspace tests",
  );
  assert.ok(
    manifest.scripts.verify.indexOf("pnpm check:coverage-generated")
      < manifest.scripts.verify.indexOf("pnpm check:phase9-gates"),
    "the Phase 9 matrix check must follow generated protocol and coverage checks",
  );
  assert.ok(
    manifest.scripts.verify.indexOf("pnpm check:phase9-gates")
      < manifest.scripts.verify.indexOf("pnpm build"),
    "the Phase 9 matrix check must remain a pre-build verification gate",
  );

  const deferredIds = [
    "P8-DOCS-CLOSEOUT",
    "P8-LEGAL-THIRD-PARTY",
    "P8-SIGN-WINDOWS",
  ];
  assert.deepEqual(registry.allowedDeferredGateIds, deferredIds);
  assert.deepEqual(
    registry.gates.filter(({ disposition }) => disposition === "deferred").map(({ id }) => id).sort(),
    deferredIds,
  );
  assert.deepEqual(
    matrix.gates.filter(({ status }) => status === "DEFERRED").map(({ id }) => id).sort(),
    deferredIds,
  );
  assert.equal(matrix.counts.deferred, 3);
});

test("release manifest contract stays pinned to the repository product identity", async () => {
  const [config, schema] = await Promise.all([
    JSON.parse(await readFile("tools/release/release-config.json", "utf8")),
    JSON.parse(await readFile("tools/release/manifest.schema.json", "utf8")),
  ]);
  assert.deepEqual(config, {
    schemaVersion: 1,
    product: "unit-test-ide",
    inputPath: "release-input.json",
    outputPath: "manifest.generated.json",
  });
  assert.equal(schema.properties.product.const, "unit-test-ide");
  assert.equal(schema.properties.schemaVersion.const, 1);
  assert.equal(schema.additionalProperties, false);
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

test("Hosted CI pins native toolchain runners and gates unstable Windows native E2E", async () => {
  const workflow = await readFile(".github/workflows/foundation.yml", "utf8");
  assert.doesNotMatch(workflow, /\b(?:windows|ubuntu)-latest\b/);
  assert.match(workflow, /^\s{2}verify-windows:\s*$/m);
  assert.match(workflow, /^\s{4}runs-on:\s*\$\{\{\s*github\.event_name\s*==\s*['"]push['"]\s*&&\s*github\.ref\s*==\s*['"]refs\/heads\/master['"]\s*&&\s*['"]unit-test-wfp['"]\s*\|\|\s*['"]windows-2025-vs2026['"]\s*\}\}\s*$/m);
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
    const prepare = source.indexOf("tools/cmake-bundle/prepare.mjs");
    const native = source.indexOf("pnpm test:e2e:native");
    assert.ok(verify > prepare && prepare !== -1 && native > verify);
    assert.match(source, /path:\s*\.bundled-tools\/cmake/);
    assert.match(source, /GITHUB_PATH/);
    assert.match(source, /uses:\s*actions\/upload-artifact@v7/);
    assert.match(source, /if:\s*always\(\)/);
    assert.match(
      source,
      new RegExp(`path:\\s*\\.native-e2e/artifacts/${platform}/toolchain-report\\.json`),
    );
    assert.match(source, /run:\s*git diff --exit-code/);

    if (platform === "windows") {
      const nativeStep = source.slice(source.lastIndexOf("      - ", native), source.indexOf("      - if:", native));
      assert.match(
        nativeStep,
        /if:\s*\$\{\{\s*vars\.UNIT_TEST_IDE_WINDOWS_NATIVE_E2E_REQUIRED\s*==\s*['"]1['"]\s*\}\}/u,
        "Windows native E2E must be opt-in on public hosted runners",
      );
      const nativeArtifact = source.indexOf("name: native-toolchain-windows-");
      assert.notEqual(nativeArtifact, -1);
      assert.match(
        source.slice(Math.max(0, nativeArtifact - 320), nativeArtifact),
        /if:\s*\$\{\{\s*always\(\)\s*&&\s*vars\.UNIT_TEST_IDE_WINDOWS_NATIVE_E2E_REQUIRED\s*==\s*['"]1['"]\s*\}\}/u,
        "Windows native evidence upload must follow the opt-in gate",
      );
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
      assert.match(
        privilegedStep,
        /UNIT_TEST_IDE_WFP_INTEGRATION_REQUIRED:\s*\$\{\{\s*github\.event_name\s*==\s*['"]push['"]\s*&&\s*github\.ref\s*==\s*['"]refs\/heads\/master['"]\s*&&\s*['"]1['"]\s*\|\|\s*['"]0['"]\s*\}\}/u,
        "WFP integration must be strict on every trusted master push"
      );
      const coverageStep = source.slice(source.lastIndexOf("      - ", coverageSmoke), legacyCleanup);
      assert.match(
        coverageStep,
        /if:\s*\$\{\{\s*github\.event_name\s*==\s*['"]push['"]\s*&&\s*github\.ref\s*==\s*['"]refs\/heads\/master['"]\s*\}\}/u,
        "Windows LLVM coverage must run on every trusted master push"
      );
      const coverageReport = source.indexOf("coverage-execution-report.json");
      assert.notEqual(coverageReport, -1);
      const uploadStep = source.slice(source.lastIndexOf("      - ", coverageReport), coverageReport);
      assert.match(uploadStep, /steps\.windows-coverage-evidence\.outcome\s*==\s*'success'/u, "WFP evidence upload must depend on validation of the exact report bytes");
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

test("coverage acceptance is a required, closed-evidence cross-platform CI gate", async () => {
  const workflow = await readFile(".github/workflows/foundation.yml", "utf8");
  const jobs = [...workflow.matchAll(/^ {2}([a-z][a-z0-9-]*):\s*$/gmu)].map((match) => match[1]);
  assert.equal(jobs.filter((name) => name === "coverage-linux-gcc").length, 1);

  const jobSource = (name) => {
    const start = workflow.indexOf(`  ${name}:`);
    assert.notEqual(start, -1, `foundation job ${name} is missing`);
    const remainder = workflow.slice(start + 1);
    const next = remainder.search(/\n {2}[a-z][a-z0-9-]*:\s*$/mu);
    return workflow.slice(start, next === -1 ? undefined : start + 1 + next);
  };

  const linux = jobSource("coverage-linux-gcc");
  assert.match(linux, /^ {4}name:\s*coverage-linux-gcc\s*$/mu);
  assert.match(linux, /^ {4}runs-on:\s*ubuntu-24\.04\s*$/mu);
  assert.doesNotMatch(linux, /continue-on-error|ToolchainUnavailable|\bSKIP\b/u);
  for (const command of [
    "go test ./apps/test-service/... -count=1",
    "go test -race ./apps/test-service/internal/coverageplatform ./apps/test-service/internal/coveragegcc ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/coverageexec -count=1",
    "go vet ./apps/test-service/...",
    "pnpm --filter @unit-test-ide/service-probe test",
    "pnpm --filter code-oss-extension test:coverage-service-smoke:linux",
    "pnpm --filter code-oss-extension validate:coverage-evidence:linux",
  ]) {
    assert.match(linux, new RegExp(command.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")), `missing required command: ${command}`);
  }
  const bootstrap = linux.indexOf("pnpm prepare:coverage-bundle");
  const framework = linux.indexOf("pnpm prepare:framework-bundle");
  const goDownload = linux.indexOf("go mod download");
  const native = linux.indexOf("test:coverage-service-smoke:linux");
  const validator = linux.indexOf("validate:coverage-evidence:linux");
  assert.ok(bootstrap !== -1 && framework !== -1 && goDownload !== -1);
  assert.ok(native > bootstrap && native > framework && native > goDownload);
  assert.ok(validator > native, "the exact published evidence must be validated after native execution");
  assert.match(linux, /name:\s*linux-gcc-coverage-report-\$\{\{ github\.run_attempt \}\}/u);
  const report = linux.indexOf("linux-gcc-coverage-report.json");
  assert.notEqual(report, -1);
  const upload = linux.slice(linux.lastIndexOf("      - ", report), report + 300);
  assert.match(upload, /if:\s*always\(\)/u);
  assert.match(upload, /if-no-files-found:\s*error/u);

  const windows = jobSource("verify-windows");
  const privileged = windows.indexOf("TestPrivilegedWindowsWFPDynamicLifecycle");
  const smoke = windows.indexOf("test:coverage-service-smoke");
  const evidenceValidator = windows.indexOf("validate:coverage-evidence:windows");
  assert.ok(privileged !== -1 && smoke > privileged && evidenceValidator > smoke);
  const privilegedStep = windows.slice(windows.lastIndexOf("      - ", privileged), smoke);
  assert.match(privilegedStep, /github\.event_name\s*==\s*['"]push['"][\s\S]*github\.ref\s*==\s*['"]refs\/heads\/master['"][\s\S]*['"]1['"]\s*\|\|\s*['"]0['"]/u);
  const smokeStep = windows.slice(windows.lastIndexOf("      - ", smoke), evidenceValidator);
  assert.match(smokeStep, /github\.event_name\s*==\s*['"]push['"][\s\S]*github\.ref\s*==\s*['"]refs\/heads\/master['"]/u);
  assert.doesNotMatch(smokeStep, /vars\.UNIT_TEST_IDE_WFP_INTEGRATION_REQUIRED/u);

  for (const retained of ["verify-windows", "verify-linux", "package-windows", "package-linux", "release-qualification"]) {
    assert.ok(jobs.includes(retained), `existing ${retained} job must remain present`);
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

test("release documentation describes complete digest-pinned Code-OSS runtime artifacts", async () => {
  const security = await readFile("docs/security.md", "utf8");
  const roadmap = await readFile("docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md", "utf8");

  assert.match(security, /完整 Code-OSS runtime 目录制品/u);
  assert.match(security, /app\/code-oss-runtime\/Code - OSS\.exe/u);
  assert.match(security, /app\/code-oss-runtime\/code-oss/u);
  assert.match(roadmap, /完整 Code-OSS runtime 目录制品/u);
  assert.match(roadmap, /Phase 8 仍未完成/u);
});

test("trusted producer documentation keeps unsigned qualification operational, closed, and incomplete", async () => {
  const [readme, security, roadmap, foundation] = await Promise.all([
    readFile("README.md", "utf8"),
    readFile("docs/security.md", "utf8"),
    readFile("docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md", "utf8"),
    readFile(".github/workflows/foundation.yml", "utf8"),
  ]);

  const producerProcedure = readme.slice(readme.indexOf("## 受信任的 Code-OSS 发布输入"));
  assert.notEqual(producerProcedure, readme, "README must own the qualification procedure in a dedicated section");
  assert.match(producerProcedure, /\.github\/workflows\/release-inputs\.yml/u);
  assert.match(producerProcedure, /Trusted Code-OSS release inputs/u);
  assert.match(producerProcedure, /b1c0a14de1414fcdaa400695b4db1c0799bc3124/u);
  assert.match(producerProcedure, /windows-2022/u);
  assert.match(producerProcedure, /Visual Studio 2022[\s\S]*C\+\+[\s\S]*Spectre/u);
  assert.match(producerProcedure, /ubuntu-24\.04/u);
  for (const artifactName of ["code-oss-windows-x64", "code-oss-linux-x64", "appimagetool-linux-x64"]) {
    assert.match(producerProcedure, new RegExp(artifactName));
  }
  assert.match(producerProcedure, /保留.*1.*天|1.*天.*保留/u);
  assert.match(producerProcedure, /Start-ClosedWorkflowRun -Workflow 'release-inputs\.yml' -ExpectedWorkflowName 'Trusted Code-OSS release inputs'/u);
  assert.match(producerProcedure, /function Start-ClosedWorkflowRun/u);
  assert.match(producerProcedure, /gh run list[\s\S]*--limit 100[\s\S]*--json databaseId,headSha,event,headBranch,workflowName/u);
  assert.match(producerProcedure, /https:\/\/github\\\.com\/colayc\/unitTest\/actions\/runs\/\(\?<runId>\[1-9\]\[0-9\]\*\)/u);
  assert.match(producerProcedure, /gh run view \$runId --repo colayc\/unitTest --json databaseId,headSha,event,headBranch,workflowName/u);
  assert.match(producerProcedure, /headSha[\s\S]*event[\s\S]*headBranch[\s\S]*workflowName/u);
  assert.match(producerProcedure, /candidate.*Count -gt 1|Count -gt 1.*candidate/u);
  assert.match(producerProcedure, /did not resolve exactly one newly dispatched run/u);
  assert.doesNotMatch(producerProcedure, /gh run list[^\n]*--limit\s+1(?![0-9])/u);
  assert.match(producerProcedure, /release_version=0\.1\.0/u);
  assert.match(producerProcedure, /release_signing_required=0/u);
  assert.match(producerProcedure, /Start-ClosedWorkflowRun -Workflow 'foundation\.yml' -ExpectedWorkflowName 'foundation'/u);
  assert.match(producerProcedure, /gh api "repos\/colayc\/unitTest\/actions\/runs\/\$releaseRunId\/artifacts"/u);
  assert.match(producerProcedure, /重新运行.*新的.*producer|新的.*producer.*artifact|fresh producer artifact/u);
  assert.match(producerProcedure, /release-inputs\/code-oss\.exe.*不是允许|不是允许.*release-inputs\/code-oss\.exe/u);

  const releaseTrustBoundary = security.slice(security.indexOf("## 发布输入信任边界"));
  assert.notEqual(releaseTrustBoundary, security, "security.md must own the producer trust boundary");
  assert.match(releaseTrustBoundary, /GitHub Actions.*API|GitHub.*run.*API/u);
  assert.match(releaseTrustBoundary, /provenance/u);
  assert.match(releaseTrustBoundary, /post-transport|传输后/u);
  assert.match(releaseTrustBoundary, /release-manifest|package manifest|包.*manifest/u);
  assert.match(releaseTrustBoundary, /本地.*Code-OSS runtime.*禁止|禁止.*本地.*Code-OSS runtime/u);
  assert.match(releaseTrustBoundary, /release-inputs\/code-oss\.exe.*禁止|禁止.*release-inputs\/code-oss\.exe/u);
  assert.match(releaseTrustBoundary, /GitHub Release.*不|不.*GitHub Release/u);
  assert.match(releaseTrustBoundary, /生产发布.*不|不.*生产发布/u);
  assert.match(releaseTrustBoundary, /不使用.*self-hosted runner|禁止使用.*self-hosted runner/u);

  const phaseEight = roadmap.slice(roadmap.indexOf("## Phase 8"));
  assert.notEqual(phaseEight, roadmap, "roadmap must retain a dedicated Phase 8 status section");
  assert.match(phaseEight, /签名/u);
  assert.match(phaseEight, /license|许可|法律/u);
  assert.match(phaseEight, /仍未完成|尚未完成|未完成/u);

  const documentedReleaseData = `${producerProcedure}\n${releaseTrustBoundary}\n${phaseEight}`;
  assert.doesNotMatch(documentedReleaseData, /actions\/runs\/[1-9][0-9]*/u, "documentation must not commit a real workflow run ID");
  const documentedDigests = [...documentedReleaseData.matchAll(/\b[0-9a-f]{64}\b/gu)].map((match) => match[0]);
  assert.deepEqual(
    documentedDigests,
    documentedDigests.filter((digest) => digest === "a6d71e2b6cd66f8e8d16c37ad164658985e0cf5fcaa950c90a482890cb9d13e0"),
    "documentation may only carry the fixed appimagetool source digest, never a run-specific digest",
  );

  const jobSource = (name) => {
    const start = foundation.indexOf(`  ${name}:`);
    assert.notEqual(start, -1, `foundation job ${name} is missing`);
    const remainder = foundation.slice(start + 1);
    const next = remainder.search(/\n {2}[a-z][a-z0-9-]*:\s*$/mu);
    return foundation.slice(start, next === -1 ? undefined : start + 1 + next);
  };
  assert.match(foundation, /\.release\/producer-verification/u, "foundation must isolate producer metadata from tracked release evidence");
  assert.doesNotMatch(foundation, /\[\[ ! -e \.release && ! -L \.release \]\]/u, "foundation must not require the tracked .release root to be absent");
  for (const [job, artifact] of [
    ["install-smoke-windows", "install-smoke-windows-${{ github.run_attempt }}"],
    ["install-smoke-linux", "install-smoke-linux-${{ github.run_attempt }}"],
    ["release-qualification", "release-qualification-${{ github.run_attempt }}"],
    ["release-qualification", "qualified-release-${{ steps.canonical-release-version.outputs.version }}-${{ github.run_attempt }}"],
  ]) {
    const source = jobSource(job);
    const artifactAt = source.indexOf(`name: ${artifact}`);
    assert.notEqual(artifactAt, -1, `${job} must upload ${artifact}`);
    const stepStart = source.lastIndexOf("      - ", artifactAt);
    const nextStep = source.indexOf("\n      - ", artifactAt);
    const uploadStep = source.slice(stepStart, nextStep === -1 ? undefined : nextStep);
    assert.match(uploadStep, /uses:\s*actions\/upload-artifact@v7/u);
    assert.match(uploadStep, /^\s{10}retention-days:\s*1\s*$/mu, `${artifact} must retain unsigned qualification evidence for exactly one day`);
  }
});

test("release-input documentation binds run attempts and provenance to immutable artifact identities", async () => {
  const [readme, security, roadmap, qualificationPlan] = await Promise.all([
    readFile("README.md", "utf8"),
    readFile("docs/security.md", "utf8"),
    readFile("docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md", "utf8"),
    readFile("docs/superpowers/plans/2026-08-28-trusted-code-oss-release-input-production.md", "utf8"),
  ]);
  const requiredFacts = [
    /run ID alone.*(?:not|不).*complete artifact identity|run ID.*不足以.*完整.*artifact.*identity/iu,
    /run attempt.*(?:bound|绑定).*end to end|run attempt.*端到端.*绑定/iu,
    /immutable artifact IDs?.*upload digests|不可变.*artifact ID.*上传.*摘要/iu,
    /transport names?.*suffixed by.*run attempt|传输.*名称.*追加.*run attempt/iu,
    /producer re-?run.*new foundation dispatch|producer.*重跑.*新的.*foundation.*dispatch/iu,
    /package jobs?.*revalidate.*attempt.*before and after download|package.*(?:下载前后|前后).*重新验证.*attempt/iu,
  ];
  for (const [name, text] of [["README", readme], ["security guidance", security], ["roadmap", roadmap]]) {
    for (const fact of requiredFacts) {
      assert.match(text, fact, `${name} must document each attempt-bound release-input fact independently`);
    }
  }

  for (const text of [readme, qualificationPlan]) {
    assert.doesNotMatch(text, /gh\s+run\s+download[^\n]*--name\s+release-input-provenance(?:\s|$)/iu);
  }

  const taskNine = qualificationPlan.slice(qualificationPlan.indexOf("### Task 9:"));
  assert.notEqual(taskNine, qualificationPlan, "qualification plan must retain Task 9");
  const stepThree = taskNine.slice(taskNine.indexOf("- [ ] **Step 3:"), taskNine.indexOf("- [ ] **Step 4:"));
  assert.notEqual(stepThree, taskNine, "Task 9 must retain a separate producer provenance Step 3");
  const stepThreeCommands = stepThree.match(/```powershell\r?\n([\s\S]*?)```/u)?.[1];
  assert.ok(stepThreeCommands, "Task 9 Step 3 must retain a PowerShell evidence command block");
  for (const expected of [
    '$producerRun = gh api "repos/colayc/unitTest/actions/runs/$producerRunId" | ConvertFrom-Json',
    '$producerRunAttempt = [int64]$producerRun.run_attempt',
    '$provenanceTransportName = "release-input-provenance-$producerRunAttempt"',
    'artifacts?per_page=100',
    '$artifactPage.total_count -ne $artifactPage.artifacts.Count',
    '$artifactPage.total_count -gt 100',
    '$_.expired -eq $false',
    '$provenanceMatches.Count -ne 1',
    '$provenanceArtifactId = [string]$provenanceMatches[0].id',
    'actions/artifacts/$provenanceArtifactId/zip',
    'Invoke-WebRequest',
    'Authorization = "Bearer $(gh auth token)"',
    'New-Item -ItemType Directory -Force -Path $producerEvidence | Out-Null',
    'Expand-Archive',
  ]) {
    assert.ok(stepThreeCommands.includes(expected), `Task 9 Step 3 must document ${expected}`);
  }
  assert.ok(
    stepThreeCommands.indexOf('New-Item -ItemType Directory -Force -Path $producerEvidence | Out-Null') < stepThreeCommands.indexOf('Invoke-WebRequest'),
    "Task 9 Step 3 must create the evidence directory before downloading the ZIP",
  );
  assert.match(stepThree, /expired\s+-eq\s+\$false/iu);
  assert.match(stepThree, /must not fall back to name selection/iu);
  assert.doesNotMatch(stepThreeCommands, /\bgh\s+run\s+download\b/iu);
  assert.doesNotMatch(stepThreeCommands, /--(?:name|artifact-id)\b/iu);
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
