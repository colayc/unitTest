import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import test from "node:test";

const workflowPath = resolve(".github/workflows/release-inputs.yml");
const workflow = await readFile(workflowPath, "utf8").then(
  (value) => value.replace(/\r\n?/gu, "\n"),
  (error) => {
    if (error?.code === "ENOENT") assert.fail("trusted release-input producer workflow is missing");
    throw error;
  },
);
const packageJson = JSON.parse(await readFile(resolve("package.json"), "utf8"));

const actionPins = Object.freeze({
  "actions/checkout": "d23441a48e516b6c34aea4fa41551a30e30af803",
  "actions/setup-node": "249970729cb0ef3589644e2896645e5dc5ba9c38",
  "actions/upload-artifact": "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
  "actions/download-artifact": "d3f86a106a0bac45b974a628896c90dbdf5c8093",
});

function topLevelSection(name) {
  const lines = workflow.split("\n");
  const start = lines.findIndex((line) => line === `${name}:`);
  assert.notEqual(start, -1, `missing top-level ${name} section`);
  let end = lines.length;
  for (let index = start + 1; index < lines.length; index += 1) {
    if (/^[A-Za-z][A-Za-z0-9_-]*:\s*$/u.test(lines[index])) {
      end = index;
      break;
    }
  }
  return lines.slice(start, end).join("\n").trimEnd();
}

const jobsSource = topLevelSection("jobs");

function jobBlock(name) {
  const lines = jobsSource.split("\n");
  const start = lines.findIndex((line) => line === `  ${name}:`);
  assert.notEqual(start, -1, `missing ${name} job`);
  let end = lines.length;
  for (let index = start + 1; index < lines.length; index += 1) {
    if (/^  [a-z][a-z0-9-]*:\s*$/u.test(lines[index])) {
      end = index;
      break;
    }
  }
  return lines.slice(start, end).join("\n");
}

function stepBlocks(job) {
  const lines = job.split("\n");
  const starts = [];
  for (let index = 0; index < lines.length; index += 1) {
    if (/^      - (?:name|uses):/u.test(lines[index])) starts.push(index);
  }
  return starts.map((start, index) => lines.slice(start, starts[index + 1] ?? lines.length).join("\n"));
}

function namedStep(job, name) {
  const step = stepBlocks(job).find((candidate) => candidate.startsWith(`      - name: ${name}\n`));
  assert.ok(step, `missing step: ${name}`);
  return step;
}

function assertOrdered(source, labels) {
  let previous = -1;
  for (const label of labels) {
    const current = source.indexOf(label);
    assert.ok(current > previous, `${label} must occur after the preceding gate`);
    previous = current;
  }
}

function inputValue(step, name) {
  return step.match(new RegExp(`^          ${name}:\\s*([^\\n#]+?)\\s*$`, "mu"))?.[1];
}

function jobOutputKeys(job) {
  const outputBlock = job.match(/^    outputs:\n((?:      [a-z0-9_]+:[^\n]*\n?)+)/mu)?.[1] ?? "";
  return [...outputBlock.matchAll(/^      ([a-z0-9_]+):/gmu)].map((match) => match[1]);
}

test("workflow exposes only an input-free manual trigger and minimum read permissions", () => {
  assert.equal(topLevelSection("on"), "on:\n  workflow_dispatch:");
  assert.equal(topLevelSection("permissions"), "permissions:\n  actions: read\n  contents: read");
  assert.doesNotMatch(workflow, /^\s*(?:push|pull_request|schedule|workflow_call|workflow_run|release):/mu);
  assert.doesNotMatch(workflow, /^\s+inputs:/mu);
  assert.doesNotMatch(jobsSource, /^    permissions:/mu);
});

test("workflow contains only the four closed jobs on fixed hosted runners", () => {
  const names = [...jobsSource.matchAll(/^  ([a-z][a-z0-9-]*):\s*$/gmu)].map((match) => match[1]);
  assert.deepEqual(names, ["authorize", "build-windows", "build-linux", "attest"]);
  assert.match(jobBlock("authorize"), /^    runs-on: ubuntu-24\.04$/mu);
  assert.match(jobBlock("build-windows"), /^    runs-on: windows-2022$/mu);
  assert.match(jobBlock("build-linux"), /^    runs-on: ubuntu-24\.04$/mu);
  assert.match(jobBlock("attest"), /^    runs-on: ubuntu-24\.04$/mu);
  assert.doesNotMatch(workflow, /(?:self-hosted|unit-test-wfp|windows-2025-vs2026|\b(?:windows|ubuntu)-latest\b)/iu);
});

test("authorization is fail-closed and every producer job depends on it", () => {
  const authorize = jobBlock("authorize");
  assert.doesNotMatch(authorize, /^    if:/mu);
  assert.match(authorize, /source-manifest\.mjs authorize/u);
  assert.match(authorize, /PRODUCER_REPOSITORY:\s*\$\{\{ github\.repository \}\}/u);
  assert.match(authorize, /PRODUCER_EVENT:\s*\$\{\{ github\.event_name \}\}/u);
  assert.match(authorize, /PRODUCER_REF:\s*\$\{\{ github\.ref \}\}/u);
  assert.match(authorize, /PRODUCER_WORKFLOW_REF:\s*\$\{\{ github\.workflow_ref \}\}/u);
  assert.match(authorize, /pnpm test:release-producer/u);
  assert.match(authorize, /node-version: 24\.18\.0/u);
  assert.match(authorize, /pnpm@11\.4\.0/u);
  assert.match(jobBlock("build-windows"), /^    needs: authorize$/mu);
  assert.match(jobBlock("build-linux"), /^    needs: authorize$/mu);
  assert.match(jobBlock("attest"), /^    needs:\n      - build-windows\n      - build-linux$/mu);
});

test("all action references are reviewed full commit pins", () => {
  const references = [...workflow.matchAll(/^\s+uses:\s*([^\s#]+)(?:\s+#.*)?$/gmu)].map((match) => match[1]);
  assert.ok(references.length > 0);
  for (const reference of references) {
    const match = /^(actions\/[a-z-]+)@([0-9a-f]{40})$/u.exec(reference);
    assert.ok(match, `action is not full-SHA pinned: ${reference}`);
    assert.equal(match[2], actionPins[match[1]], `unreviewed action pin: ${reference}`);
  }
  for (const [action, pin] of Object.entries(actionPins)) {
    assert.ok(references.includes(`${action}@${pin}`), `missing reviewed ${action} action`);
  }
});

test("workflow has no secret, local-runtime, mutable-tool, or expression-in-shell escape hatch", () => {
  assert.doesNotMatch(workflow, /secrets\./iu);
  assert.doesNotMatch(workflow, /(?:^|[\s'"])[A-Za-z]:[\\/]|release-inputs[\\/]code-oss\.exe/imu);
  assert.doesNotMatch(workflow, /github\.com\/AppImage\/appimagetool\/releases\/(?:download|latest)|\/continuous\//iu);
  assert.doesNotMatch(workflow, /^\s+run:\s*[|>]?\s*\n(?:\s{8,}.*\$\{\{.*\n)+/gmu);
  for (const jobName of ["authorize", "build-windows", "build-linux", "attest"]) {
    for (const step of stepBlocks(jobBlock(jobName))) {
      const run = step.match(/^        run:\s*[|>]?-?\s*\n([\s\S]*)/mu)?.[1];
      if (run !== undefined) assert.doesNotMatch(run, /\$\{\{/u, "GitHub expressions must enter scripts through env, not interpolation");
    }
  }
});

test("both builds use the fixed fresh checkout, toolchains, Gulp targets, and output roots", () => {
  for (const name of ["build-windows", "build-linux"]) {
    const job = jobBlock(name);
    assert.match(job, /git init \.producer[\\/]vscode/u);
    assert.match(job, /https:\/\/github\.com\/microsoft\/vscode\.git/u);
    assert.match(job, /fetch --depth=1 origin b1c0a14de1414fcdaa400695b4db1c0799bc3124/u);
    assert.match(job, /checkout --detach FETCH_HEAD/u);
    assert.match(job, /rev-parse HEAD/u);
    assert.match(job, /source-manifest\.mjs verify-checkout/u);
    assert.match(job, /node-version: 20\.14\.0/u);
    assert.match(job, /yarn@1\.22\.22/u);
    assert.match(job, /yarn install --frozen-lockfile/u);
  }
  const windows = jobBlock("build-windows");
  assert.match(windows, /yarn gulp vscode-win32-x64/u);
  assert.match(windows, /\.producer[\\/]VSCode-win32-x64/u);
  assert.match(windows, /VSCode-\*/u);
  const linux = jobBlock("build-linux");
  assert.match(linux, /yarn gulp vscode-linux-x64/u);
  assert.match(linux, /\.producer\/VSCode-linux-x64/u);
  assert.match(linux, /VSCode-\*/u);
  assert.match(linux, /apt-get install --no-install-recommends -y build-essential g\+\+ libx11-dev libx11-xcb-dev libxkbfile-dev libsecret-1-dev pkg-config python-is-python3/u);
  assert.doesNotMatch(workflow, /setup-go|fallback|--ignore-(?:engines|scripts)|(?:disable|without|no)[-_ ]spectre/iu);
});

test("Windows validates, bounds launcher execution, inventories, and stages before upload", () => {
  const job = jobBlock("build-windows");
  assertOrdered(job, [
    "name: Install fixed Yarn",
    "name: Validate Visual Studio 2022 toolchain",
    "name: Build fixed Windows Code-OSS target",
    "name: Validate Windows output root",
    "name: Validate Windows runtime",
    "name: Check Windows launcher version",
    "name: Stage and inventory Windows runtime",
    "name: Upload Windows runtime",
  ]);
  assert.match(job, /WaitForExit\(30000\)/u);
  assert.match(job, /1\.92\.0/u);
  assert.match(job, /runtime-inventory\.mjs create/u);
  assert.match(job, /\.release[\\/]producer[\\/]windows[\\/]code-oss-windows-x64/u);
  assert.match(job, /\$fileCount = \[int64\]0[\s\S]*?TryParse\([^\n]+\[ref\]\$fileCount\)/u);
  assert.match(job, /\$totalBytes = \[int64\]0[\s\S]*?TryParse\([^\n]+\[ref\]\$totalBytes\)/u);
  const outputs = ["launcher_sha256", "file_count", "total_bytes", "tree_digest"];
  assert.deepEqual(jobOutputKeys(job), outputs);
  for (const output of outputs) {
    assert.match(job, new RegExp(`^      ${output}:`, "mu"));
  }
  const preflight = namedStep(job, "Validate Visual Studio 2022 toolchain");
  assert.match(preflight, /vswhere\.exe/u);
  assert.match(preflight, /-products '\*' -version '\[17\.0,18\.0\)'[\s\S]*?-requires 'Microsoft\.VisualStudio\.Component\.VC\.Tools\.x86\.x64' 'Microsoft\.VisualStudio\.Component\.VC\.Runtimes\.x86\.x64\.Spectre'/u);
  assert.match(preflight, /\$instances\.Count -ne 1/u);
  assert.match(preflight, /installationVersion[\s\S]*?\^17\\\./u);
  assert.match(preflight, /\$failure = 'RELEASE_PRODUCER_BUILD_FAILED: Visual Studio 2022 toolchain preflight failed'/u);
  assert.match(preflight, /GYP_MSVS_VERSION=2022/u);
  assert.match(preflight, /npm_config_msvs_version=2022/u);
  assert.doesNotMatch(preflight, /(?:winget|choco|visualstudio\.microsoft\.com|vs_installer|--add|--remove|fallback|2019|2017)/iu);
});

test("Linux creates and validates modes, bounds launcher execution, inventories, and stages exact roots", () => {
  const job = jobBlock("build-linux");
  assertOrdered(job, [
    "name: Validate Linux output root",
    "name: Create Linux mode inventory",
    "name: Stage and validate Linux runtime",
    "name: Check Linux launcher version",
    "name: Inventory staged Linux runtime",
    "name: Acquire and validate appimagetool",
    "name: Upload Linux runtime",
    "name: Upload appimagetool",
  ]);
  assert.match(job, /runtime-mode-inventory\.mjs create/u);
  assert.match(job, /runtime-mode-inventory\.mjs restore/u);
  assert.match(job, /timeout 30s/u);
  assert.match(job, /1\.92\.0/u);
  assert.match(job, /code-oss-runtime-mode\.json/u);
  assert.match(job, /runtime-inventory\.mjs create/u);
  assert.match(job, /https:\/\/api\.github\.com\/repos\/AppImage\/appimagetool\/releases\/assets\/324406882/u);
  assert.match(job, /Accept: application\/octet-stream/u);
  assert.match(job, /15092216/u);
  assert.match(job, /a6d71e2b6cd66f8e8d16c37ad164658985e0cf5fcaa950c90a482890cb9d13e0/u);
  assert.match(job, /find "\$tool_root" -mindepth 1 -maxdepth 1 -printf '%f\\0'/u);
  assert.match(job, /-f "\$tool" && ! -L "\$tool"/u);
  const outputs = ["launcher_sha256", "file_count", "total_bytes", "tree_digest", "mode_inventory_sha256", "appimagetool_sha256", "appimagetool_size"];
  assert.deepEqual(jobOutputKeys(job), outputs);
  for (const output of outputs) {
    assert.match(job, new RegExp(`^      ${output}:`, "mu"));
  }
});

test("uploads have exact names, one-day failure-closed retention, and complete hidden runtime trees", () => {
  const uploads = [];
  for (const name of ["build-windows", "build-linux", "attest"]) {
    for (const step of stepBlocks(jobBlock(name))) {
      if (step.includes(`uses: actions/upload-artifact@${actionPins["actions/upload-artifact"]}`)) uploads.push(step);
    }
  }
  assert.equal(uploads.length, 4);
  const expected = new Map([
    ["code-oss-windows-x64", { hidden: true, path: ".release/producer/windows/code-oss-windows-x64" }],
    ["code-oss-linux-x64", { hidden: true, path: ".release/producer/linux/code-oss-linux-x64" }],
    ["appimagetool-linux-x64", { hidden: false, path: ".release/producer/linux/appimagetool-linux-x64/appimagetool-x86_64.AppImage" }],
    ["release-input-provenance", { hidden: false, path: ".release/attestation/release-input-provenance.json" }],
  ]);
  for (const step of uploads) {
    const name = inputValue(step, "name");
    assert.ok(expected.has(name), `unexpected upload artifact ${name}`);
    assert.equal(inputValue(step, "retention-days"), "1", `${name} retention drifted`);
    assert.equal(inputValue(step, "if-no-files-found"), "error", `${name} may silently upload no files`);
    assert.equal(inputValue(step, "path"), expected.get(name).path, `${name} upload root drifted`);
    assert.doesNotMatch(step, /^        if:\s*\$\{\{\s*always\(\)\s*\}\}/mu);
    if (expected.get(name).hidden) assert.equal(inputValue(step, "include-hidden-files"), "true", `${name} drops hidden runtime files`);
  }
});

test("attestation downloads exactly three current-run artifacts and independently revalidates them", () => {
  const job = jobBlock("attest");
  const downloads = stepBlocks(job).filter((step) => step.includes(`uses: actions/download-artifact@${actionPins["actions/download-artifact"]}`));
  assert.equal(downloads.length, 3);
  assert.deepEqual(downloads.map((step) => inputValue(step, "name")), [
    "code-oss-windows-x64",
    "code-oss-linux-x64",
    "appimagetool-linux-x64",
  ]);
  assert.deepEqual(downloads.map((step) => inputValue(step, "path")), [
    ".release/transport/windows",
    ".release/transport/linux",
    ".release/transport/appimagetool",
  ]);
  for (const step of downloads) {
    assert.equal(inputValue(step, "run-id"), undefined, "attestation must consume only artifacts from its current run");
  }
  assertOrdered(job, [
    "name: Download Windows runtime",
    "name: Download Linux runtime",
    "name: Download appimagetool",
    "name: Validate exact transport roots",
    "name: Validate transported Linux mode inventory",
    "name: Restore transported Linux modes",
    "name: Inventory transported runtimes and tool",
    "name: Compare transported summaries",
    "name: Check transported Linux launcher version",
    "name: Create and validate provenance",
    "name: Write validated workflow summary",
    "name: Upload release input provenance",
  ]);
  const modeGate = namedStep(job, "Validate transported Linux mode inventory");
  const restore = namedStep(job, "Restore transported Linux modes");
  const inventory = namedStep(job, "Inventory transported runtimes and tool");
  const compare = namedStep(job, "Compare transported summaries");
  const execute = namedStep(job, "Check transported Linux launcher version");
  assert.match(modeGate, /validateRuntimeModeInventory/u);
  assert.match(modeGate, /mode_inventory_sha256[\s\S]*?EXPECTED_MODE_INVENTORY_SHA256/u);
  assert.doesNotMatch(modeGate, /runtime-mode-inventory\.mjs restore|timeout 30s/u);
  assert.match(restore, /runtime-mode-inventory\.mjs restore/u);
  assert.doesNotMatch(restore, /timeout 30s/u);
  assert.match(inventory, /runtime-inventory\.mjs create/u);
  assert.doesNotMatch(inventory, /timeout 30s/u);
  assert.match(compare, /EXPECTED_WINDOWS_TREE_DIGEST/u);
  assert.match(compare, /EXPECTED_LINUX_TREE_DIGEST/u);
  assert.match(compare, /EXPECTED_MODE_INVENTORY_SHA256/u);
  assert.match(compare, /EXPECTED_APPIMAGETOOL_SHA256/u);
  assert.doesNotMatch(compare, /timeout 30s/u);
  assert.match(execute, /timeout 30s \.release\/transport\/linux\/runtime\/code-oss/u);
  assert.match(job, /runtime-inventory\.mjs create/u);
  assert.match(job, /provenance\.mjs create/u);
  assert.match(job, /provenance\.mjs validate/u);
  assert.match(job, /test ! -e \.release\/attestation/u);
  assert.doesNotMatch(job, /mkdir[^\n]*\.release\/attestation/u);
  assert.match(job, /find \.release\/transport\/appimagetool -mindepth 1 -maxdepth 1 -printf '%f\\0'/u);
  assert.match(job, /-f "\$tool" && ! -L "\$tool"/u);
  assert.match(job, /find \.release\/attestation -mindepth 1 -maxdepth 1 -printf '%f\\0'/u);
});

test("post-validation summary is path-free and exposes only fixed trusted fields", () => {
  const job = jobBlock("attest");
  const validateIndex = job.indexOf("provenance.mjs validate");
  const summaryIndex = job.indexOf("name: Write validated workflow summary");
  assert.ok(validateIndex !== -1 && summaryIndex > validateIndex);
  const summary = namedStep(job, "Write validated workflow summary");
  const run = summary.match(/^        run:\s*\|\s*\n([\s\S]*)/mu)?.[1];
  assert.ok(run);
  assert.doesNotMatch(run, /(?:[A-Za-z]:[\\/]|\.release[\\/]|tools[\\/]|\/home\/|\$\{\{)/u);
  const labels = [...run.matchAll(/printf -- '- ([^:]+):/gu)].map((match) => match[1]);
  assert.deepEqual(labels, [
    "Producer run ID",
    "Source commit",
    "Windows launcher SHA-256",
    "Linux launcher SHA-256",
    "appimagetool SHA-256",
    "Artifact retention",
  ]);
});

test("package scripts run the closed producer suite early and enumerate every producer contract", () => {
  assert.equal(
    packageJson.scripts["test:release-producer"],
    "node --test tools/release/producer/source-manifest.test.mjs tools/release/producer/runtime-inventory.test.mjs tools/release/producer/provenance.test.mjs tools/release/producer/trusted-run.test.mjs tools/release/producer/workflow-contract.test.mjs",
  );
  assert.match(packageJson.scripts.test, /^pnpm run test:release-producer && /u);
});
