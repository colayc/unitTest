import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const yaml = readFileSync(new URL("../../../.github/workflows/windows-portable-resources.yml", import.meta.url), "utf8");
const build = readFileSync(new URL("./windows-resource-build.ps1", import.meta.url), "utf8");

test("resource workflow is manual Windows read-only", () => {
  const resources = wiJob(yaml, "resources");
  const identity = wiJob(yaml, "windows-inputs-identity");
  const jobsStart = yaml.search(/^jobs:\r?$/m);
  assert.ok(jobsStart >= 0);
  const jobKeys = [...yaml.slice(jobsStart).matchAll(/^  ([a-z][a-z0-9-]*):\r?$/gm)].map((item) => item[1]);
  assert.deepEqual(jobKeys, ["windows-inputs-identity", "resources"]);
  assert.match(identity, /runs-on: windows-2022\r?\n    timeout-minutes: 5/);
  assert.equal((identity.match(/uses: actions\/upload-artifact@/g) ?? []).length, 1);
  assert.equal((identity.match(/retention-days: 7/g) ?? []).length, 1);
  assert.equal((identity.match(/if-no-files-found: error/g) ?? []).length, 1);
  const foundation = readFileSync(new URL("../../../.github/workflows/foundation.yml", import.meta.url), "utf8");
  for (const text of [yaml, foundation]) {
    assert.match(text, /^concurrency:\r?$/m);
    assert.match(text, /^  cancel-in-progress: false\r?$/m);
    assert.match(text, /^  queue: max\r?$/m);
    assert.match(text, /&& 'windows-inputs-read' \|\| format\('windows-inputs-no-private-\{0\}-\{1\}'/);
    const identity = wiJob(text, 'windows-inputs-identity');
    assert.doesNotMatch(identity, /^    environment:/m);
    assert.doesNotMatch(identity, /WI_INPUT_READ_TOKEN:|secrets\.WINDOWS_INPUTS_READ_TOKEN|actions\/checkout@|setup-node@|setup-go@/);
    assert.match(identity, /^    permissions:\r?\n      contents: read\r?$/m);
    assert.match(identity, /receipt_base64:.*steps\.identity\.outputs/);
    assert.match(identity, /artifact_id:.*steps\.identity-upload\.outputs/);
    assert.match(identity, /windows-inputs-identity-\$\{\{ github\.run_id \}\}-\$\{\{ github\.run_attempt \}\}/);
    assert.doesNotMatch(text, /^\s*<<:/m);
  }
  assert.equal(wiScript(foundation), wiScript(yaml));
  for (const text of [yaml, foundation]) {
    const script = wiScript(text);
    const entry = script.slice(script.indexOf("# WI_IDENTITY_ENTRY_BEGIN"));
    const metadataStart = entry.indexOf("if($env:GITHUB_JOB -ceq 'windows-inputs-identity'){");
    const guardStart = entry.indexOf("} else {", metadataStart);
    assert.ok(metadataStart >= 0 && guardStart > metadataStart);
    const metadataBranch = entry.slice(metadataStart, guardStart);
    assert.match(metadataBranch, /Get-WIIdentityBatch/);
    assert.match(metadataBranch, /Read-WIIdentityPublicLock/);
    assert.match(metadataBranch, /New-WIIdentity/);
    assert.doesNotMatch(metadataBranch, /windows-input-import\.ps1|Get-WIHostedObservation|Assert-WIHostedSourceProof|Invoke-WIHostedImport|WI_APPROVED_CANDIDATE_SHA|WI_INPUT_READ_TOKEN/);
    assert.doesNotMatch(script.slice(0, script.indexOf("# WI_IDENTITY_ENTRY_BEGIN")), /^\s*\.\s/m);
  }
  for (const key of ["verify-windows", "verify-framework-windows", "package-windows"]) {
    const body = wiJob(foundation, key);
    const deny = body.indexOf("- name: Reject failed or missing Windows input identity");
    const checkout = body.indexOf("- uses: actions/checkout@");
    const guard = body.indexOf("- name: Verify Windows input identity before credential step");
    const imported = body.indexOf("- name: Import independently approved Windows input bytes");
    assert.ok(deny >= 0 && deny < checkout && checkout < guard && guard < imported);
    assert.match(body.slice(deny, checkout), /throw 'WI_ID_UPSTREAM'/);
    const guardEnd = body.indexOf("\n      - ", guard + 1);
    assert.ok(guardEnd > guard);
    assert.doesNotMatch(body.slice(guard, guardEnd), /WI_INPUT_READ_TOKEN:|secrets\.WINDOWS_INPUTS_READ_TOKEN|WI_APPROVED_/);
    assert.match(body.slice(guard, guardEnd), /run: \*wi_identity_script/);
    assert.match(body, /needs:[\s\S]*windows-inputs-identity/);
    assert.match(body, /if: \$\{\{ !cancelled\(\)/);
    if (key === "package-windows") {
      for (const dependency of ["verify-windows", "verify-linux", "verify-release-input-run"]) {
        assert.ok(body.includes("      - " + dependency + "\n"));
        assert.ok(body.includes("needs." + dependency + ".result == 'success'"));
      }
    }
  }
  const framework = wiJob(foundation, "verify-framework-windows");
  const checkoutRef = framework.match(/^          ref: (.+)\r?$/m);
  const candidate = framework.match(/^          WI_CANDIDATE_SHA: (.+)\r?$/m);
  assert.ok(checkoutRef && candidate);
  assert.equal(checkoutRef[1], candidate[1]);
  assert.match(checkoutRef[1], /github\.event\.pull_request\.head\.sha/);
  const resourceDeny = resources.indexOf("- name: Reject failed or missing Windows input identity");
  const resourceCheckout = resources.indexOf("- name: Checkout expected committed source");
  const resourceGuard = resources.indexOf("- name: Verify Windows input identity before credential step");
  const resourceImported = resources.indexOf("- name: Source, retention and VS2022 Spectre preflight");
  assert.ok(resourceDeny >= 0 && resourceDeny < resourceCheckout && resourceCheckout < resourceGuard && resourceGuard < resourceImported);
  assert.match(resources, /needs: windows-inputs-identity\r?\n    if: \$\{\{ !cancelled\(\) \}\}/);
  assert.doesNotMatch(resources.slice(resourceGuard, resourceImported), /WI_INPUT_READ_TOKEN:|secrets\.WINDOWS_INPUTS_READ_TOKEN|WI_APPROVED_/);
  assert.match(yaml, /^on:\s*\n  workflow_dispatch:/m);
  assert.doesNotMatch(yaml, /^\s*(push|pull_request|schedule|workflow_run|workflow_call):/m);
  assert.equal((yaml.match(/^      expected_source_commit:/gm) ?? []).length, 1);
  assert.equal((yaml.match(/^        type:/gm) ?? []).length, 1);
  assert.match(yaml, /permissions:\n  contents: read\n  actions: read/);
  assert.match(resources, /runs-on: windows-2022\n    timeout-minutes: 120/);
  assert.equal((resources.match(/runs-on:/g) ?? []).length, 1);
  assert.doesNotMatch(yaml, /self-hosted|contents:\s*write|actions:\s*write|setup-go|matrix:|continue-on-error|gh release|action-gh-release|signing\.pfx|ubuntu-|macos-/);
  for (const action of yaml.matchAll(/uses: ([^\s]+)/g)) {
    assert.ok([
      "actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803",
      "actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38",
      "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
    ].includes(action[1]));
  }
  assert.match(yaml, /persist-credentials: false/);
  assert.match(yaml, /ref: \$\{\{ inputs\.expected_source_commit \}\}/);
  assert.equal((yaml.match(/node-version: 24\.18\.0/g) ?? []).length, 2);
  assert.equal((yaml.match(/node-version: 20\.14\.0/g) ?? []).length, 1);
  const stages = [...resources.matchAll(/-Stage '([^']+)'/g)].map((item) => item[1]);
  assert.deepEqual(stages, ["Preflight", "PrepareRepositoryDependencies", "VerifyChecks", "FetchSource", "VerifySource", "CodeOssDependencies", "BuildRuntime", "CheckRuntimeVersion", "PrepareBundles", "PackageResources", "WriteOrigin", "FailureReceipt"]);
  assert.match(build, /'PnpmInstall'.*'RepositoryDependencies'/);
  assert.match(build, /'--frozen-lockfile','--ignore-scripts'/);
  assert.match(build, /GITHUB_WORKFLOW_SHA/);
  assert.match(build, /GITHUB_RETENTION_DAYS/);
  assert.match(build, /VC\.Runtimes\.x86\.x64\.Spectre/);
  assert.doesNotMatch(build, /go\.exe|pnpm test|npm test|manifest\.test\.mjs/);
  const checkoutStarts = [...yaml.matchAll(/^      - name: Checkout expected committed source\r?$/gm)];
  assert.equal(checkoutStarts.length, 1);
  const checkoutStart = checkoutStarts[0].index;
  const nextStep = yaml.indexOf("\n      - name:", checkoutStart + 1);
  assert.ok(nextStep > checkoutStart);
  const checkout = yaml.slice(checkoutStart, nextStep);
  assert.match(checkout, /uses: actions\/checkout@d23441a48e516b6c34aea4fa41551a30e30af803/);
  assert.match(checkout, /^        env:\r?$/m);
  for (const [name, value] of [
    ["GIT_CONFIG_COUNT", "2"], ["GIT_CONFIG_KEY_0", "core.autocrlf"],
    ["GIT_CONFIG_VALUE_0", "false"], ["GIT_CONFIG_KEY_1", "core.eol"],
    ["GIT_CONFIG_VALUE_1", "lf"],
  ]) {
    const line = "          " + name + ": '" + value + "'";
    assert.equal(checkout.split(/\r?\n/u).filter((item) => item === line).length, 1);
    assert.equal(yaml.split(/\r?\n/u).filter((item) => item.trimStart().startsWith(name + ":")).length, 1);
  }
  assert.doesNotMatch(checkout, /git config|--global|--system|--worktree/);
  assert.doesNotMatch(yaml, /core\.autocrlf.*(?:true|input)|GIT_CONFIG_VALUE_1:\s*['"]?crlf/);
  assert.match(yaml, /^    environment: windows-inputs-read$/m);
  assert.equal((yaml.match(/^          WI_INPUT_READ_TOKEN:/gm) ?? []).length, 1);
  const preflightStep = yaml.slice(yaml.indexOf("- name: Source, retention"), yaml.indexOf("- name: Repository Node"));
  assert.match(preflightStep, /WI_APPROVED_CANDIDATE_SHA:/);
  assert.match(preflightStep, /WI_APPROVED_WORKFLOW_SHA:/);
  const closure = build.match(/function Get-WRInputPaths \{[\s\S]*?return ,@\(([\s\S]*?)\n    \)/);
  assert.ok(closure);
  const paths = [...closure[1].matchAll(/^        '([^']+)'$/gm)].map((item) => item[1]);
  assert.equal(paths.length, 58);
  assert.equal(new Set(paths).size, 58);
  for (const path of [
    "tools/release/producer/windows-input-source.json",
    "tools/release/producer/windows-input-contract.ps1",
    "tools/release/producer/windows-input-pack.ps1",
    "tools/release/producer/windows-input-import.ps1",
    "tools/release/producer/windows-input-models.ps1",
    "tools/framework-bundle/manifest.json",
  ]) assert.ok(paths.includes(path));
  const preflight = build.slice(build.indexOf("function Initialize-WRPreflight"), build.indexOf("function Read-WRStageContext"));
  assert.ok(preflight.indexOf("Assert-WRInputSnapshot $Context") < preflight.indexOf("Read-WITransfer "));
  assert.match(preflight, /Get-WRRemainingSeconds \$Context 900/);
  assert.ok(preflight.indexOf("Read-WITransfer ") < preflight.lastIndexOf("Assert-WRInputSnapshot $Context"));
});

test("resource workflow publishes payload before origin", () => {
  const resources = wiJob(yaml, "resources");
  assert.ok(yaml.indexOf("id: package") < yaml.indexOf("id: upload-payload"));
  assert.ok(yaml.indexOf("id: upload-payload") < yaml.indexOf("id: origin"));
  assert.ok(yaml.indexOf("id: origin") < yaml.indexOf("id: upload-origin"));
  assert.match(yaml, /RESOURCE_STATIC_VALIDATED/);
  assert.match(yaml, /path: \$\{\{ steps\.package\.outputs\.payload_directory \}\}/);
  assert.match(yaml, /WR_RESULT_SHA256: \$\{\{ steps\.package\.outputs\.result_sha256 \}\}/);
  assert.match(yaml, /WR_PAYLOAD_ARTIFACT_ID: \$\{\{ steps\.upload-payload\.outputs\.artifact-id \}\}/);
  assert.match(yaml, /WR_PAYLOAD_ARTIFACT_DIGEST: \$\{\{ steps\.upload-payload\.outputs\.artifact-digest \}\}/);
  assert.match(yaml, /path: \$\{\{ steps\.origin\.outputs\.origin_path \}\}/);
  assert.equal((resources.match(/if: success\(\)/g) ?? []).length, 3);
  assert.equal((resources.match(/retention-days: 7/g) ?? []).length, 3);
  assert.equal((resources.match(/if-no-files-found: error/g) ?? []).length, 3);
  assert.match(yaml, /name: windows-portable-resources-\$\{\{ github.run_id \}\}-\$\{\{ github.run_attempt \}\}/);
  assert.match(yaml, /name: windows-portable-resources-origin-\$\{\{ github.run_id \}\}-\$\{\{ github.run_attempt \}\}/);
  assert.match(yaml, /if: failure\(\)/);
  assert.doesNotMatch(yaml, /path: (?:\.|\*|\.release|\.producer)(?:\s|\/)/);
  assert.doesNotMatch(yaml, /include-hidden-files: true|if: always\(\)/);
  assert.match(yaml, /id: failure\r?\n        if: failure\(\)/);
  assert.match(yaml, /path: \$\{\{ steps\.failure\.outputs\.failure_directory \}\}/);
  assert.doesNotMatch(yaml, /failure-collection-work/);
  assert.ok(build.indexOf("function Read-WRFailureCollection") >= 0);
  const failureBranchStart = build.indexOf("if($Stage -ceq 'FailureReceipt')");
  const failureBranchEnd = build.indexOf("$script:WRActiveContext=$context", failureBranchStart);
  assert.ok(failureBranchStart >= 0 && failureBranchEnd > failureBranchStart);
  const branch = build.slice(failureBranchStart, failureBranchEnd);
  assert.ok(branch.indexOf("$verified=Read-WRFailureCollection $context") >= 0);
  assert.ok(branch.indexOf("$verified=Read-WRFailureCollection $context") <
            branch.indexOf("Write-WROutput 'failure_directory' $verified.directory"));
  assert.doesNotMatch(branch, /return Get-BasicBinding/);
  const foundation = readFileSync(new URL("../../../.github/workflows/foundation.yml", import.meta.url), "utf8");
  assert.equal((foundation.match(/^          WI_INPUT_READ_TOKEN:/gm) ?? []).length, 3);
  for (const job of ["verify-windows", "verify-framework-windows", "package-windows"]) {
    const start = foundation.indexOf("\n  " + job + ":");
    assert.ok(start >= 0);
    const tail = foundation.slice(start + 1);
    const next = tail.search(/\n  [a-z][a-z0-9-]*:/u);
    const body = next < 0 ? tail : tail.slice(0, next);
    assert.match(body, /windows-inputs-read.*windows-inputs-no-access/);
    assert.match(body, /if:.*WI_PRIVATE_INPUT_ROUTE == 'true'/);
    assert.ok(body.indexOf("WI_INPUT_READ_TOKEN:") > body.indexOf("steps:"));
    assert.match(body, /persist-credentials: false/);
    assert.match(body, /Invoke-WIHostedImport/);
    if (job !== "package-windows") assert.match(body, /WI_FORK_UNAPPROVED/);
    if (job === "verify-windows") assert.match(body, /push.*refs\/heads\/master.*unit-test-wfp/);
    if (job === "verify-framework-windows") {
      assert.match(body, /WI_CACHE_RESTORE_DISABLED/);
      assert.match(body, /if:.*WI_PRIVATE_INPUT_ROUTE != 'true'/);
    }
    assert.doesNotMatch(body, /continue-on-error|pull_request_target|name: windows-inputs-artifact/);
  }
});

// Append to the existing file; keep its two existing test names.
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve, sep } from "node:path";
import { spawnSync } from "node:child_process";

function wiJob(text, key) {
  const start = text.indexOf("\n  " + key + ":");
  assert.ok(start >= 0, "missing job " + key);
  const tail = text.slice(start + 1);
  const end = tail.search(/\n  [a-z][a-z0-9-]*:/u);
  return end < 0 ? tail : tail.slice(0, end);
}
function wiScript(text) {
  const body = wiJob(text, "windows-inputs-identity");
  const match = body.match(/^        run: &wi_identity_script \|\r?\n((?:^          .*(?:\r?\n|$)|^\r?\n)*)/m);
  assert.ok(match, "literal identity script");
  return match[1].split(/\r?\n/).map((line) => line.startsWith("          ") ? line.slice(10) : line).join("\n").trim();
}
function wiCore(text) {
  const script = wiScript(text);
  const begin = script.indexOf("# WI_IDENTITY_CORE_BEGIN");
  const end = script.indexOf("# WI_IDENTITY_CORE_END");
  assert.ok(begin >= 0 && end > begin);
  return script.slice(begin, end + "# WI_IDENTITY_CORE_END".length);
}
test("Windows input identity batch binding models", (t) => {
  const foundation = readFileSync(new URL("../../../.github/workflows/foundation.yml", import.meta.url), "utf8");
  assert.equal(wiScript(foundation), wiScript(yaml), "both workflows share the exact literal implementation");
  const core = wiCore(yaml);
  const pwsh = "C:/Program Files/PowerShell/7/pwsh.exe";
  const root = resolve(tmpdir());
  const dir = mkdtempSync(join(root, "wi-identity-models-"));
  assert.ok(dir.startsWith(root + sep));
  let success = false;
  const hash = "4cfc73931b46693f738181f7479d910cc01a29f3f515d0d3dc0d053fcb1f1678";
  const base = () => ({
    repository: "colayc/unitTest", repositoryId: "1307775492",
    event: "pull_request", ref: "refs/pull/40/merge",
    headRepository: "colayc/unitTest", headSha: "a".repeat(40),
    workflowRef: "colayc/unitTest/.github/workflows/foundation.yml@refs/pull/40/merge",
    workflowSha: "b".repeat(40), triggerSha: "c".repeat(40),
    runId: "123", runAttempt: "1", expectedSourceCommit: "",
    runnerEnvironment: "github-hosted", runnerOS: "Windows"
  });
  const manual = { ...base(), event: "workflow_dispatch", ref: "refs/heads/master",
    workflowRef: "colayc/unitTest/.github/workflows/foundation.yml@refs/heads/master" };
  const resource = { ...manual, workflowRef: "colayc/unitTest/.github/workflows/windows-portable-resources.yml@refs/heads/master",
    workflowSha: "a".repeat(40), triggerSha: "a".repeat(40), expectedSourceCommit: "a".repeat(40) };
  const cases = [
    { name: "PR H and W differ", live: base(), job: "verify-windows", change: "", expected: "OK" },
    { name: "manual complete batch", live: manual, job: "package-windows", change: "", expected: "OK" },
    { name: "master producer identity", live: resource, job: "resources", change: "", expected: "OK" },
    ...[
      ["stale attempt", "attempt", "WI_ID_BINDING"],
      ["wrong run", "run", "WI_ID_BINDING"],
      ["wrong workflow SHA", "workflow", "WI_ID_BINDING"],
      ["wrong head", "head", "WI_ID_BINDING"],
       ["wrong ref", "ref", "WI_ID_ROUTE"],
      ["extra job", "job", "WI_ID_JOB"],
      ["fork", "fork", "WI_ID_ROUTE"],
      ["duplicate consumer", "duplicate", "WI_ID_SCHEMA"],
      ["lock drift", "lock", "WI_ID_BINDING"],
       ["unknown field", "extra", "WI_ID_SCHEMA"],
       ["missing receipt", "missing", "WI_ID_SCHEMA"]
    ].map(([name, change, expected]) => ({ name, live: base(), job: "verify-windows", change, expected }))
  ];
  const harness = [
    "param([string]$CorePath,[string]$FixturePath)",
    "$ErrorActionPreference='Stop'",
    ". $CorePath",
    "$f=Get-Content -LiteralPath $FixturePath -Raw|ConvertFrom-Json -AsHashtable",
    "$L=$f.live;$Job=$f.job;$Actual='OK'",
    "try {",
    "$R=New-WIIdentity $L 417 '" + hash + "'",
    "switch -CaseSensitive ($f.change) {",
    "'attempt' {$L.runAttempt='2'}",
    "'run' {$L.runId='124'}",
    "'workflow' {$L.workflowSha=('d'*40)}",
    "'head' {$L.headSha=('d'*40)}",
     "'ref' {$L.ref='refs/pull/41/merge'}",
    "'job' {$Job='package-windows'}",
    "'fork' {$L.headRepository='other/fork'}",
    "'duplicate' {$R.allowedConsumerJobKeys=@('verify-windows','verify-windows')}",
    "'lock' {$R.sourceLockSha256=('0'*64)}",
    "'extra' {$R['extra']='x'}",
     "'missing' {$R=$null}",
    "}",
    "$R=Read-WIIdentityJson ($R|ConvertTo-Json -Depth 8 -Compress)",
    "Assert-WIIdentity $R $L $Job",
    "} catch {$Actual=$_.Exception.Message}",
    "if($Actual -cne $f.expected){Write-Output ('WI_MODEL_MISMATCH:'+ $f.name+':'+$Actual);exit 1}",
    "Write-Output $f.name;exit 0"
  ].join("\n");
  try {
    const corePath = join(dir, "core.ps1"), harnessPath = join(dir, "model.ps1");
    writeFileSync(corePath, core, { encoding: "utf8", flag: "wx" });
    writeFileSync(harnessPath, harness, { encoding: "utf8", flag: "wx" });
    for (let i = 0; i < cases.length; i++) {
      const file = join(dir, "case-" + i + ".json");
      writeFileSync(file, JSON.stringify(cases[i]), { encoding: "utf8", flag: "wx" });
      const env = Object.fromEntries(["SystemRoot", "WINDIR", "TEMP", "TMP"].filter((k) => process.env[k] !== undefined).map((k) => [k, process.env[k]]));
      env.PATH = "C:/Program Files/PowerShell/7;" + join(process.env.SystemRoot, "System32");
      const r = spawnSync(pwsh, ["-NoProfile", "-NonInteractive", "-File", harnessPath, "-CorePath", corePath, "-FixturePath", file],
        { env, shell: false, windowsHide: true, encoding: "utf8", timeout: 30000, maxBuffer: 65536 });
      t.diagnostic(JSON.stringify({ case: cases[i].name, status: r.status, signal: r.signal, error: r.error?.message, stdout: r.stdout, stderr: r.stderr }));
      assert.equal(r.error, undefined, "model process failed");
      assert.equal(r.status, 0, "first unexpected model failure");
    }
    success = true;
  } finally {
    if (success) rmSync(dir, { recursive: true, force: false });
    else t.diagnostic("PRESERVED_FAILURE_FIXTURES=" + dir);
  }
});
