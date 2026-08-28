import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  loadSourceManifest,
  validateProducerInvocation,
  validateSourceManifest,
  verifyCodeOssCheckout,
} from "./source-manifest.mjs";

const expectedManifest = {
  schemaVersion: 1,
  codeOss: {
    repository: "https://github.com/microsoft/vscode.git",
    commit: "b1c0a14de1414fcdaa400695b4db1c0799bc3124",
    version: "1.92.0",
    nodeVersion: "20.14.0",
    yarnVersion: "1.22.22",
    windowsTarget: "vscode-win32-x64",
    windowsOutput: "VSCode-win32-x64",
    linuxTarget: "vscode-linux-x64",
    linuxOutput: "VSCode-linux-x64",
  },
  appimagetool: {
    repository: "AppImage/appimagetool",
    assetId: 324406882,
    assetName: "appimagetool-x86_64.AppImage",
    size: 15092216,
    sha256: "a6d71e2b6cd66f8e8d16c37ad164658985e0cf5fcaa950c90a482890cb9d13e0",
  },
};

const acceptedInvocation = {
  repository: "colayc/unitTest",
  event: "workflow_dispatch",
  ref: "refs/heads/master",
  workflowRef: "colayc/unitTest/.github/workflows/release-inputs.yml@refs/heads/master",
};

function copy(value) {
  return JSON.parse(JSON.stringify(value));
}

function expectConfigFailure(run) {
  assert.throws(run, (error) => error?.code === "RELEASE_PRODUCER_CONFIG_INVALID");
}

async function expectUntrustedFailure(run) {
  await assert.rejects(run, (error) => error?.code === "RELEASE_PRODUCER_UNTRUSTED");
}

async function expectConfigFailureAsync(run) {
  await assert.rejects(run, (error) => error?.code === "RELEASE_PRODUCER_CONFIG_INVALID");
}

async function withTemporaryDirectory(t, run) {
  const directory = await mkdtemp(join(tmpdir(), "source-manifest-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await run(directory);
}

async function writeFixtureFile(root, relativePath, value) {
  const path = join(root, ...relativePath.split("/"));
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, value);
  return path;
}

async function createCheckoutFixture(root, { yarnrc = 'target "30.1.2"\n', gulp = null } = {}) {
  await writeFixtureFile(root, ".nvmrc", "20.14.0\n");
  await writeFixtureFile(root, "package.json", JSON.stringify({ version: "1.92.0" }));
  await writeFixtureFile(root, ".yarnrc", yarnrc);
  await writeFixtureFile(root, "build/gulpfile.vscode.js", gulp ?? [
    "const BUILD_TARGETS = [",
    "  { platform: 'win32', arch: 'x64' },",
    "  { platform: 'linux', arch: 'x64' },",
    "];",
    "BUILD_TARGETS.forEach(buildTarget => {",
    "  const platform = buildTarget.platform;",
    "  const arch = buildTarget.arch;",
    "  const destinationFolderName = `VSCode${dashed(platform)}${dashed(arch)}`;",
    "  const tasks = [packageTask(platform, arch, sourceFolderName, destinationFolderName, opts)];",
    "  const vscodeTask = task.define(`vscode${dashed(platform)}${dashed(arch)}${dashed(minified)}`, task.series(...tasks));",
    "  gulp.task(vscodeTask);",
    "});",
  ].join("\n"));
}

function cli(script, args, options = {}) {
  return spawnSync(process.execPath, [script, ...args], {
    encoding: "utf8",
    windowsHide: true,
    ...options,
  });
}

test("validateSourceManifest returns a frozen canonical copy", () => {
  const manifest = validateSourceManifest(copy(expectedManifest));
  assert.deepEqual(manifest, expectedManifest);
  assert.notEqual(manifest, expectedManifest);
  assert.notEqual(manifest.codeOss, expectedManifest.codeOss);
  assert.equal(Object.isFrozen(manifest), true);
  assert.equal(Object.isFrozen(manifest.codeOss), true);
  assert.equal(Object.isFrozen(manifest.appimagetool), true);
});

test("validateSourceManifest rejects every changed field and non-closed shape", () => {
  for (const [section, fields] of Object.entries({
    root: { schemaVersion: 2 },
    codeOss: {
      repository: "http://github.com/microsoft/vscode.git",
      commit: "B1C0A14DE1414FCDAA400695B4DB1C0799BC3124",
      version: "1.92.1",
      nodeVersion: "20.14.1",
      yarnVersion: "1.22.23",
      windowsTarget: "vscode-win32-arm64",
      windowsOutput: "VSCode-win32-arm64",
      linuxTarget: "vscode-linux-arm64",
      linuxOutput: "VSCode-linux-arm64",
    },
    appimagetool: {
      repository: "git@github.com:AppImage/appimagetool.git",
      assetId: 324406882.5,
      assetName: "appimagetool.AppImage",
      size: -1,
      sha256: "A6D71E2B6CD66F8E8D16C37AD164658985E0CF5FCAA950C90A482890CB9D13E0",
    },
  })) {
    for (const [field, replacement] of Object.entries(fields)) {
      const value = copy(expectedManifest);
      if (section === "root") value[field] = replacement;
      else value[section][field] = replacement;
      expectConfigFailure(() => validateSourceManifest(value));
    }
  }
  for (const mutate of [
    (value) => { value.extra = true; },
    (value) => { value.codeOss.extra = true; },
    (value) => { value.appimagetool.extra = true; },
    (value) => { value.codeOss.repository = "file:///local/vscode"; },
    (value) => { value.appimagetool.repository = "C:/local/appimagetool"; },
    (value) => { value.codeOss.commit = "b1c0a14"; },
    (value) => { value.appimagetool.sha256 = "a".repeat(63); },
    (value) => { value.appimagetool.assetId = Number.MAX_SAFE_INTEGER + 1; },
    (value) => { Object.defineProperty(value, "hidden", { value: true }); },
    (value) => { value[Symbol("hidden")] = true; },
    (value) => { Object.defineProperty(value.codeOss, "hidden", { value: true }); },
    (value) => { value.appimagetool[Symbol("hidden")] = true; },
  ]) {
    const value = copy(expectedManifest);
    mutate(value);
    expectConfigFailure(() => validateSourceManifest(value));
  }
});

test("loadSourceManifest accepts only a real canonical JSON file", async (t) => {
  await withTemporaryDirectory(t, async (root) => {
    const manifestPath = await writeFixtureFile(root, "source-manifest.json", `${JSON.stringify(expectedManifest)}\n`);
    assert.deepEqual(await loadSourceManifest(manifestPath), expectedManifest);
    await writeFile(manifestPath, "{ invalid JSON");
    await expectConfigFailureAsync(() => loadSourceManifest(manifestPath));
    const targetPath = await writeFixtureFile(root, "target.json", `${JSON.stringify(expectedManifest)}\n`);
    const linkPath = join(root, "linked.json");
    await t.test("linked file", async (subtest) => {
      try {
        await symlink(targetPath, linkPath, "file");
      } catch (error) {
        if (error?.code === "EPERM") {
          subtest.skip("symbolic links unavailable");
          return;
        }
        throw error;
      }
      await expectConfigFailureAsync(() => loadSourceManifest(linkPath));
    });
    const manifestDirectory = join(root, "manifest-directory");
    await writeFixtureFile(manifestDirectory, "source-manifest.json", `${JSON.stringify(expectedManifest)}\n`);
    const linkedDirectory = join(root, "linked-manifest-directory");
    try {
      await symlink(manifestDirectory, linkedDirectory, "junction");
    } catch (error) {
      if (error?.code === "EPERM") {
        t.skip("directory links unavailable");
        return;
      }
      throw error;
    }
    await expectConfigFailureAsync(() => loadSourceManifest(join(linkedDirectory, "source-manifest.json")));
  });
});

test("validateProducerInvocation accepts only the fixed producer identity", () => {
  assert.deepEqual(validateProducerInvocation(acceptedInvocation), {
    repository: acceptedInvocation.repository,
    event: acceptedInvocation.event,
    ref: acceptedInvocation.ref,
    workflowRef: acceptedInvocation.workflowRef,
    provenancePath: ".github/workflows/release-inputs.yml",
  });
  for (const [field, value] of Object.entries({
    repository: "Colayc/unitTest",
    event: "push",
    ref: "refs/heads/main",
    workflowRef: "colayc/unitTest/.github/workflows/release-inputs.yml@main",
  })) {
    const invocation = { ...acceptedInvocation, [field]: value };
    assert.throws(() => validateProducerInvocation(invocation), (error) => error?.code === "RELEASE_PRODUCER_UNTRUSTED");
  }
  assert.throws(() => validateProducerInvocation({ ...acceptedInvocation, extra: true }), (error) => error?.code === "RELEASE_PRODUCER_UNTRUSTED");
});

test("verifyCodeOssCheckout checks the fixed upstream metadata", async (t) => {
  await withTemporaryDirectory(t, async (root) => {
    await createCheckoutFixture(root, { yarnrc: 'cache-folder ".yarn-cache"\r\ntarget "30.1.2"\r\n' });
    const result = await verifyCodeOssCheckout({ root, actualCommit: expectedManifest.codeOss.commit, manifest: expectedManifest });
    assert.deepEqual(result, { commit: expectedManifest.codeOss.commit, version: "1.92.0" });
    const cases = [
      ["node", ".nvmrc", "20.14.1\n"],
      ["version", "package.json", JSON.stringify({ version: "1.92.1" })],
      ["electron", ".yarnrc", 'target "30.1.3"\n'],
      ["duplicate electron target", ".yarnrc", 'target "30.1.2"\ntarget "30.1.2"\n'],
      ["windows target", "build/gulpfile.vscode.js", "const BUILD_TARGETS = [{ platform: 'win32', arch: 'arm64' }];"],
      ["windows output", "build/gulpfile.vscode.js", "const destinationFolderName = `VSCode-win32-arm64`;"],
      ["linux target", "build/gulpfile.vscode.js", "const BUILD_TARGETS = [{ platform: 'linux', arch: 'arm64' }];"],
      ["linux output", "build/gulpfile.vscode.js", "const destinationFolderName = `VSCode-linux-arm64`;"],
    ];
    for (const [, relativePath, content] of cases) {
      await createCheckoutFixture(root);
      await writeFixtureFile(root, relativePath, content);
      await expectUntrustedFailure(() => verifyCodeOssCheckout({ root, actualCommit: expectedManifest.codeOss.commit, manifest: expectedManifest }));
    }
    await createCheckoutFixture(root, { gulp: [
      "const BUILD_TARGETS = [",
      "  { platform: 'win32', arch: 'arm64' },",
      "  { platform: 'linux', arch: 'arm64' },",
      "];",
      "// 'vscode-win32-x64' 'VSCode-win32-x64' 'vscode-linux-x64' 'VSCode-linux-x64'",
      "const deadTargets = ['vscode-win32-x64', 'VSCode-win32-x64', 'vscode-linux-x64', 'VSCode-linux-x64'];",
      "BUILD_TARGETS.forEach(buildTarget => {",
      "  const platform = buildTarget.platform;",
      "  const arch = buildTarget.arch;",
      "  const destinationFolderName = `VSCode${dashed(platform)}${dashed(arch)}`;",
      "  const tasks = [packageTask(platform, arch, sourceFolderName, destinationFolderName, opts)];",
      "  const vscodeTask = task.define(`vscode${dashed(platform)}${dashed(arch)}${dashed(minified)}`, task.series(...tasks));",
      "});",
    ].join("\n") });
    await expectUntrustedFailure(() => verifyCodeOssCheckout({ root, actualCommit: expectedManifest.codeOss.commit, manifest: expectedManifest }));
    await createCheckoutFixture(root);
    await expectUntrustedFailure(() => verifyCodeOssCheckout({ root, actualCommit: "b1c0a14", manifest: expectedManifest }));
  });
});

test("verifyCodeOssCheckout rejects a linked intermediate directory", async (t) => {
  await withTemporaryDirectory(t, async (root) => {
    await createCheckoutFixture(root);
    const outsideBuild = join(root, "outside-build");
    await writeFixtureFile(outsideBuild, "gulpfile.vscode.js", [
      "// 'vscode-win32-x64' 'VSCode-win32-x64' 'vscode-linux-x64' 'VSCode-linux-x64'",
      "const BUILD_TARGETS = [{ platform: 'win32', arch: 'x64' }, { platform: 'linux', arch: 'x64' }];",
      "BUILD_TARGETS.forEach(buildTarget => {",
      "  const platform = buildTarget.platform; const arch = buildTarget.arch;",
      "  const destinationFolderName = `VSCode${dashed(platform)}${dashed(arch)}`;",
      "  const tasks = [packageTask(platform, arch, sourceFolderName, destinationFolderName, opts)];",
      "  const vscodeTask = task.define(`vscode${dashed(platform)}${dashed(arch)}${dashed(minified)}`, task.series(...tasks));",
      "});",
    ].join("\n"));
    await rm(join(root, "build"), { recursive: true, force: true });
    try {
      await symlink(outsideBuild, join(root, "build"), "junction");
    } catch (error) {
      if (error?.code === "EPERM") {
        t.skip("directory links unavailable");
        return;
      }
      throw error;
    }
    await expectUntrustedFailure(() => verifyCodeOssCheckout({ root, actualCommit: expectedManifest.codeOss.commit, manifest: expectedManifest }));
  });
});

test("CLI exports fixed outputs and keeps failures path-free and atomic", async (t) => {
  await withTemporaryDirectory(t, async (root) => {
    const script = fileURLToPath(new URL("./source-manifest.mjs", import.meta.url));
    const manifestPath = await writeFixtureFile(root, "source-manifest.json", `${JSON.stringify(expectedManifest)}\n`);
    const outputPath = join(root, "github-output.txt");
    const exported = cli(script, ["export", "--manifest", manifestPath, "--github-output", outputPath]);
    assert.equal(exported.status, 0, exported.stderr);
    const output = await readFile(outputPath, "utf8");
    const expectedNames = [
      "code_oss_repository", "code_oss_commit", "code_oss_version", "code_oss_node_version", "code_oss_yarn_version", "code_oss_windows_target", "code_oss_windows_output", "code_oss_linux_target", "code_oss_linux_output", "appimagetool_repository", "appimagetool_asset_id", "appimagetool_asset_name", "appimagetool_size", "appimagetool_sha256",
    ];
    assert.deepEqual(output.trim().split(/\r?\n/u).map((line) => line.split("=", 1)[0]), expectedNames);
    assert.deepEqual(output.trim().split(/\r?\n/u).map((line) => line.slice(line.indexOf("=") + 1)), [
      expectedManifest.codeOss.repository,
      expectedManifest.codeOss.commit,
      expectedManifest.codeOss.version,
      expectedManifest.codeOss.nodeVersion,
      expectedManifest.codeOss.yarnVersion,
      expectedManifest.codeOss.windowsTarget,
      expectedManifest.codeOss.windowsOutput,
      expectedManifest.codeOss.linuxTarget,
      expectedManifest.codeOss.linuxOutput,
      expectedManifest.appimagetool.repository,
      String(expectedManifest.appimagetool.assetId),
      expectedManifest.appimagetool.assetName,
      String(expectedManifest.appimagetool.size),
      expectedManifest.appimagetool.sha256,
    ]);
    assert.equal(output.includes("%"), false);

    await writeFile(outputPath, "unchanged\n");
    const hostile = cli(script, ["authorize", "--repository", "colayc/unitTest\ninjected=value", "--event", acceptedInvocation.event, "--ref", acceptedInvocation.ref, "--workflow-ref", acceptedInvocation.workflowRef], { env: { ...process.env, GITHUB_OUTPUT: outputPath } });
    assert.notEqual(hostile.status, 0);
    assert.match(hostile.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
    assert.equal(hostile.stderr.includes(root), false);
    assert.equal(await readFile(outputPath, "utf8"), "unchanged\n");

    for (const hostileAssetName of ["appimagetool%0A.AppImage", "appimagetool\ninjected=value.AppImage"]) {
      const hostileManifest = copy(expectedManifest);
      hostileManifest.appimagetool.assetName = hostileAssetName;
      const hostileManifestPath = await writeFixtureFile(root, `hostile-${hostileAssetName.length}.json`, JSON.stringify(hostileManifest));
      const rejected = cli(script, ["export", "--manifest", hostileManifestPath, "--github-output", outputPath]);
      assert.notEqual(rejected.status, 0);
      assert.match(rejected.stderr, /^RELEASE_PRODUCER_CONFIG_INVALID: [^\r\n]+\r?\n$/u);
      assert.equal(rejected.stderr.includes(root), false);
      assert.equal(await readFile(outputPath, "utf8"), "unchanged\n");
    }

    const failedExport = cli(script, ["export", "--manifest", join(root, "missing.json"), "--github-output", outputPath]);
    assert.notEqual(failedExport.status, 0);
    assert.match(failedExport.stderr, /^RELEASE_PRODUCER_CONFIG_INVALID: [^\r\n]+\r?\n$/u);
    assert.equal(failedExport.stderr.includes(root), false);
    assert.equal(await readFile(outputPath, "utf8"), "unchanged\n");
  });
});

test("CLI authorize and verify-checkout emit no root path", async (t) => {
  await withTemporaryDirectory(t, async (root) => {
    const script = fileURLToPath(new URL("./source-manifest.mjs", import.meta.url));
    const authorize = cli(script, ["authorize", "--repository", acceptedInvocation.repository, "--event", acceptedInvocation.event, "--ref", acceptedInvocation.ref, "--workflow-ref", acceptedInvocation.workflowRef]);
    assert.equal(authorize.status, 0, authorize.stderr);
    assert.deepEqual(JSON.parse(authorize.stdout), { provenancePath: ".github/workflows/release-inputs.yml" });

    const manifestPath = await writeFixtureFile(root, "source-manifest.json", `${JSON.stringify(expectedManifest)}\n`);
    const checkoutRoot = join(root, "vscode");
    await createCheckoutFixture(checkoutRoot);
    const verified = cli(script, ["verify-checkout", "--manifest", manifestPath, "--root", checkoutRoot, "--actual-commit", expectedManifest.codeOss.commit]);
    assert.equal(verified.status, 0, verified.stderr);
    assert.equal(verified.stdout.includes(checkoutRoot), false);
    assert.deepEqual(JSON.parse(verified.stdout), { commit: expectedManifest.codeOss.commit, version: "1.92.0" });
    const rejected = cli(script, ["verify-checkout", "--manifest", manifestPath, "--root", checkoutRoot, "--actual-commit", "b1c0a14"]);
    assert.notEqual(rejected.status, 0);
    assert.match(rejected.stderr, /^RELEASE_PRODUCER_UNTRUSTED: [^\r\n]+\r?\n$/u);
    assert.equal(rejected.stderr.includes(root), false);
  });
});
