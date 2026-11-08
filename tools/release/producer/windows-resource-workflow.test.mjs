import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const yaml = readFileSync(new URL("../../../.github/workflows/windows-portable-resources.yml", import.meta.url), "utf8");
const build = readFileSync(new URL("./windows-resource-build.ps1", import.meta.url), "utf8");

test("resource workflow is manual Windows read-only", () => {
  assert.match(yaml, /^on:\s*\n  workflow_dispatch:/m);
  assert.doesNotMatch(yaml, /^\s*(push|pull_request|schedule|workflow_run|workflow_call):/m);
  assert.equal((yaml.match(/^      expected_source_commit:/gm) ?? []).length, 1);
  assert.equal((yaml.match(/^        type:/gm) ?? []).length, 1);
  assert.match(yaml, /permissions:\n  contents: read\n  actions: read/);
  assert.match(yaml, /runs-on: windows-2022\n    timeout-minutes: 120/);
  assert.equal((yaml.match(/runs-on:/g) ?? []).length, 1);
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
  const stages = [...yaml.matchAll(/-Stage '([^']+)'/g)].map((item) => item[1]);
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
});

test("resource workflow publishes payload before origin", () => {
  assert.ok(yaml.indexOf("id: package") < yaml.indexOf("id: upload-payload"));
  assert.ok(yaml.indexOf("id: upload-payload") < yaml.indexOf("id: origin"));
  assert.ok(yaml.indexOf("id: origin") < yaml.indexOf("id: upload-origin"));
  assert.match(yaml, /RESOURCE_STATIC_VALIDATED/);
  assert.match(yaml, /path: \$\{\{ steps\.package\.outputs\.payload_directory \}\}/);
  assert.match(yaml, /WR_RESULT_SHA256: \$\{\{ steps\.package\.outputs\.result_sha256 \}\}/);
  assert.match(yaml, /WR_PAYLOAD_ARTIFACT_ID: \$\{\{ steps\.upload-payload\.outputs\.artifact-id \}\}/);
  assert.match(yaml, /WR_PAYLOAD_ARTIFACT_DIGEST: \$\{\{ steps\.upload-payload\.outputs\.artifact-digest \}\}/);
  assert.match(yaml, /path: \$\{\{ steps\.origin\.outputs\.origin_path \}\}/);
  assert.equal((yaml.match(/if: success\(\)/g) ?? []).length, 3);
  assert.equal((yaml.match(/retention-days: 7/g) ?? []).length, 3);
  assert.equal((yaml.match(/if-no-files-found: error/g) ?? []).length, 3);
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
});
