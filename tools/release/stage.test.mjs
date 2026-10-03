import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmod, lstat, mkdtemp, mkdir, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

import { validateCodeOssRuntime } from "./code-oss-runtime.mjs";
import { stageRelease } from "./stage.mjs";

const sourceDateEpoch = "1787616000";
const generatedAt = "2026-08-25T00:00:00.000Z";
const linuxOnly = process.platform === "linux" ? test : test.skip;
const codeOssNoticePaths = [
  "LICENSES.chromium.html",
  "resources/app/LICENSE.txt",
  "resources/app/ThirdPartyNotices.txt",
  "resources/app/extensions/git/dist/main.js.LICENSE.txt",
  "resources/app/extensions/latex/cpp-bailout-license.txt",
  "resources/app/extensions/ms-vscode.js-debug/ThirdPartyNotices.txt",
];

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function withTemporaryRoot(t, run) {
  const root = await mkdtemp(join(tmpdir(), "release-stage-"));
  t.after(async () => {
    await rm(root, { recursive: true, force: true });
  });
  await run(root);
}

async function writeFixtureFile(root, relativePath, value) {
  const filePath = join(root, ...relativePath.split("/"));
  await mkdir(dirname(filePath), { recursive: true });
  await writeFile(filePath, value);
  return filePath;
}

async function createReleaseFixture(root, platform = "windows") {
  const codeOssRoot = join(root, "inputs/code-oss");
  const launcherRelativePath = platform === "windows" ? "Code - OSS.exe" : "code-oss";
  const launcherPath = await writeFixtureFile(codeOssRoot, launcherRelativePath, "code oss runtime\n");
  if (platform === "linux") await chmod(launcherPath, 0o755);
  await writeFixtureFile(codeOssRoot, "resources/app/product.json", JSON.stringify({
    applicationName: "code-oss",
    licenseName: "MIT",
    nameShort: "Code - OSS",
  }, null, 2));
  await writeFixtureFile(codeOssRoot, "resources/app/package.json", JSON.stringify({ name: "code-oss" }, null, 2));
  await writeFixtureFile(codeOssRoot, "resources/app/LICENSE.txt", "Code - OSS license\n");
  await writeFixtureFile(codeOssRoot, "LICENSES.chromium.html", "Chromium notices\n");
  await writeFixtureFile(codeOssRoot, "resources/app/ThirdPartyNotices.txt", "App third-party notices\n");
  await writeFixtureFile(codeOssRoot, "resources/app/extensions/git/dist/main.js.LICENSE.txt", "Git extension license\n");
  await writeFixtureFile(codeOssRoot, "resources/app/extensions/latex/cpp-bailout-license.txt", "LaTeX extension license\n");
  await writeFixtureFile(codeOssRoot, "resources/app/extensions/ms-vscode.js-debug/ThirdPartyNotices.txt", "Debug extension notices\n");
  await writeFixtureFile(codeOssRoot, "resources/app/extensions/example/unlicensed.txt", "Not a license notice\n");
  await writeFixtureFile(codeOssRoot, "resources/app/extensions/example/ThirdPartyNoticeship.txt", "Not a third-party notice\n");
  await writeFixtureFile(codeOssRoot, "resources/app/extensions/example/foo-license-manager.js", "Not a license notice\n");
  await writeFixtureFile(codeOssRoot, "resources/app/extensions/example/foo.notice-board.txt", "Not a notice\n");
  await writeFixtureFile(codeOssRoot, "locales/en-US.pak", "locale data\n");
  await writeFixtureFile(codeOssRoot, "runtime.dll", "runtime library\n");
  const service = await writeFixtureFile(root, `inputs/service/unit-test-service${platform === "windows" ? ".exe" : ""}`, "service binary\n");
  const extensionRoot = join(root, "inputs/extension");
  await writeFixtureFile(extensionRoot, "dist/src/extension-entry.cjs", "exports.activate = async () => undefined;\n");
  await writeFixtureFile(extensionRoot, "dist/src/extension.js", "export const value = 1;\n");
  await writeFixtureFile(extensionRoot, "package.json", JSON.stringify({
    name: "code-oss-extension",
    publisher: "unit-test-ide",
    version: "0.0.0-test",
    main: "./dist/src/extension-entry.cjs",
  }, null, 2));
  const cmakeRoot = join(root, "inputs/cmake");
  await writeFixtureFile(cmakeRoot, "bin/cmake.exe", "cmake binary\n");
  await writeFixtureFile(cmakeRoot, "manifest.json", JSON.stringify({ tool: "cmake" }, null, 2));
  await writeFixtureFile(cmakeRoot, "licenses/LICENSE.txt", "cmake license\n");
  const coverageRoot = join(root, "inputs/coverage");
  await writeFixtureFile(coverageRoot, "app/gcovr-runner.pyz", "runner payload\n");
  await writeFixtureFile(coverageRoot, "manifest.json", JSON.stringify({ tool: "coverage" }, null, 2));
  await writeFixtureFile(coverageRoot, "licenses/NOTICE.txt", "coverage notice\n");
  const testgenRoot = join(root, "inputs/testgen");
  await writeFixtureFile(testgenRoot, "bin/clang.exe", "test-generation clang fixture\n");
  await writeFixtureFile(testgenRoot, "manifest.json", JSON.stringify({ tool: "testgen" }, null, 2));
  await writeFixtureFile(testgenRoot, "licenses/LLVM-LICENSE.txt", "test-generation license\n");
  return {
    codeOssRoot,
    codeOssSha256: sha256(await readFile(launcherPath)),
    launcherPath,
    service,
    extensionRoot,
    cmakeRoot,
    coverageRoot,
    testgenRoot,
    outRoot: join(root, "out"),
  };
}

function stageFixtureRelease(input, options = {}) {
  return stageRelease(input, {
    validateTestgenBundle: async () => undefined,
    ...options,
  });
}

async function assertExtensionRejectedBeforePublication(fixture, expected) {
  await assert.rejects(
    () => stageFixtureRelease({
      platform: "windows",
      architecture: "x64",
      version: "1.2.3",
      sourceCommit: "a".repeat(40),
      sourceDateEpoch,
      ...fixture,
    }),
    expected,
  );
  await assert.rejects(
    () => lstat(join(fixture.outRoot, "staging", "1.2.3", "windows-x64")),
    /ENOENT/u,
  );
}

async function packageInputSnapshot(root, current = "") {
  const entries = await readdir(current ? join(root, ...current.split("/")) : root, { withFileTypes: true });
  entries.sort((left, right) => left.name.localeCompare(right.name, "en"));
  const snapshot = [];
  for (const entry of entries) {
    const relativePath = current ? `${current}/${entry.name}` : entry.name;
    const absolutePath = join(root, ...relativePath.split("/"));
    const info = await stat(absolutePath);
    assert.equal(info.mtime.toISOString(), generatedAt, `timestamp for ${relativePath}`);
    if (entry.isDirectory()) snapshot.push(...await packageInputSnapshot(root, relativePath));
    else snapshot.push([relativePath, (await readFile(absolutePath)).toString("base64")]);
  }
  return snapshot;
}

test("stageRelease copies the deterministic staging layout and writes a release manifest", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    await writeFixtureFile(
      fixture.codeOssRoot,
      "resources/app/node_modules.asar.unpacked/@vscode/ripgrep/bin/rg.exe",
      "ripgrep\n",
    );
    await writeFixtureFile(
      fixture.codeOssRoot,
      "resources/app/extensions/javascript/syntaxes/Regular Expressions (JavaScript).tmLanguage",
      "grammar\n",
    );
    const result = await stageFixtureRelease({
      platform: "windows",
      architecture: "x64",
      version: "1.2.3",
      sourceCommit: "a".repeat(40),
      sourceDateEpoch,
      ...fixture,
    });

    assert.equal(result.stagingRoot, join(fixture.outRoot, "staging", "1.2.3", "windows-x64"));
    for (const relativePath of [
      "app/code-oss-runtime/Code - OSS.exe",
      "app/code-oss-runtime/resources/app/product.json",
      "app/code-oss-runtime/locales/en-US.pak",
      "app/code-oss-runtime/runtime.dll",
      "app/code-oss-runtime/resources/app/node_modules.asar.unpacked/@vscode/ripgrep/bin/rg.exe",
      "app/code-oss-runtime/resources/app/extensions/javascript/syntaxes/Regular Expressions (JavaScript).tmLanguage",
      "app/extensions/unit-test-ide/package.json",
      "app/extensions/unit-test-ide/dist/src/extension-entry.cjs",
      "app/extensions/unit-test-ide/dist/src/extension.js",
      "service/unit-test-service.exe",
      "bundles/cmake/bin/cmake.exe",
      "bundles/cmake/manifest.json",
      "bundles/coverage/app/gcovr-runner.pyz",
      "bundles/coverage/manifest.json",
      "bundles/testgen/bin/clang.exe",
      "bundles/testgen/manifest.json",
      "licenses/cmake/licenses/LICENSE.txt",
      "licenses/coverage/licenses/NOTICE.txt",
      "licenses/testgen/licenses/LLVM-LICENSE.txt",
      ...codeOssNoticePaths.map((relativePath) => `licenses/code-oss/${relativePath}`),
      "release-manifest.json",
    ]) {
      const bytes = await readFile(join(result.stagingRoot, ...relativePath.split("/")));
      assert.ok(bytes.length > 0, relativePath);
    }

    const manifest = JSON.parse(await readFile(result.manifestPath, "utf8"));
    const runtimeArtifacts = manifest.artifacts.filter(({ relativePath }) => relativePath.startsWith("app/code-oss-runtime/"));
    const runtimeFiles = await packageInputSnapshot(join(result.stagingRoot, "app", "code-oss-runtime"));
    assert.deepEqual(
      runtimeArtifacts.map(({ relativePath }) => relativePath).sort((left, right) => left.localeCompare(right, "en")),
      runtimeFiles.map(([relativePath]) => `app/code-oss-runtime/${relativePath}`).sort((left, right) => left.localeCompare(right, "en")),
    );
    for (const artifact of runtimeArtifacts) {
      const bytes = await readFile(join(result.stagingRoot, ...artifact.relativePath.split("/")));
      assert.equal(artifact.kind, "runtime");
      assert.equal(artifact.sha256, sha256(bytes));
    }
    assert.equal(
      sha256(await readFile(join(result.stagingRoot, "service/unit-test-service.exe"))),
      sha256("service binary\n"),
    );

    assert.equal(manifest.version, "1.2.3");
    assert.equal(manifest.platform, "windows");
    assert.equal(manifest.architecture, "x64");
    assert.equal(manifest.sourceCommit, "a".repeat(40));
    assert.equal(manifest.generatedAt, generatedAt);
    assert.deepEqual(
      manifest.licenses.map(({ path }) => path),
      [
        "licenses/cmake/licenses/LICENSE.txt",
        "licenses/code-oss/LICENSES.chromium.html",
        "licenses/code-oss/resources/app/extensions/git/dist/main.js.LICENSE.txt",
        "licenses/code-oss/resources/app/extensions/latex/cpp-bailout-license.txt",
        "licenses/code-oss/resources/app/extensions/ms-vscode.js-debug/ThirdPartyNotices.txt",
        "licenses/code-oss/resources/app/LICENSE.txt",
        "licenses/code-oss/resources/app/ThirdPartyNotices.txt",
        "licenses/coverage/licenses/NOTICE.txt",
        "licenses/testgen/licenses/LLVM-LICENSE.txt",
      ],
    );
    for (const relativePath of codeOssNoticePaths) {
      const stagedPath = `licenses/code-oss/${relativePath}`;
      assert.equal(manifest.licenses.filter(({ path }) => path === stagedPath).length, 1, stagedPath);
    }
    for (const relativePath of [
      "resources/app/extensions/example/unlicensed.txt",
      "resources/app/extensions/example/ThirdPartyNoticeship.txt",
      "resources/app/extensions/example/foo-license-manager.js",
      "resources/app/extensions/example/foo.notice-board.txt",
    ]) {
      await assert.rejects(
        () => lstat(join(result.stagingRoot, "licenses", "code-oss", ...relativePath.split("/"))),
        /ENOENT/u,
      );
    }
    for (const license of manifest.licenses) {
      const bytes = await readFile(join(result.stagingRoot, ...license.path.split("/")));
      assert.equal(license.size, bytes.length);
      assert.equal(license.sha256, sha256(bytes));
    }
  });
});

test("stageRelease rejects a missing extension manifest entry before publication", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    await rm(join(fixture.extensionRoot, "dist", "src", "extension-entry.cjs"));

    await assertExtensionRejectedBeforePublication(fixture, /extension main.*required/u);
  });
});

test("stageRelease rejects an extension manifest entry outside dist before publication", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    await writeFixtureFile(fixture.extensionRoot, "extension-entry.cjs", "exports.activate = async () => undefined;\n");
    await writeFixtureFile(fixture.extensionRoot, "package.json", JSON.stringify({
      name: "code-oss-extension",
      publisher: "unit-test-ide",
      version: "0.0.0-test",
      main: "./extension-entry.cjs",
    }, null, 2));

    await assertExtensionRejectedBeforePublication(fixture, /extension main|unsafe staged path/u);
  });
});

test("stageRelease rejects absolute and traversal extension manifest entries before publication", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    for (const [label, main] of [
      ["absolute", join(root, "absolute-extension-entry.cjs")],
      ["traversal", "../outside-extension-entry.cjs"],
    ]) {
      await t.test(label, async () => {
        const fixture = await createReleaseFixture(join(root, label));
        const outsideEntry = label === "absolute"
          ? main
          : join(dirname(fixture.extensionRoot), "outside-extension-entry.cjs");
        await mkdir(dirname(outsideEntry), { recursive: true });
        await writeFile(outsideEntry, "exports.activate = async () => undefined;\n");
        await writeFixtureFile(fixture.extensionRoot, "package.json", JSON.stringify({
          name: "code-oss-extension",
          publisher: "unit-test-ide",
          version: "0.0.0-test",
          main,
        }, null, 2));

        await assertExtensionRejectedBeforePublication(fixture, /extension main|unsafe staged path/u);
      });
    }
  });
});

linuxOnly("stageRelease preserves the complete executable Linux runtime", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root, "linux");
    const result = await stageFixtureRelease({
      platform: "linux",
      architecture: "x64",
      version: "1.2.3",
      sourceCommit: "a".repeat(40),
      sourceDateEpoch,
      ...fixture,
    });

    await stat(join(result.stagingRoot, "app", "code-oss-runtime", "code-oss"));
    await stat(join(result.stagingRoot, "app", "code-oss-runtime", "resources", "app", "product.json"));
    await assert.rejects(() => stat(join(result.stagingRoot, "app", "code-oss")), /ENOENT/u);
    const manifest = JSON.parse(await readFile(result.manifestPath, "utf8"));
    const launcher = manifest.artifacts.find(({ relativePath }) => relativePath === "app/code-oss-runtime/code-oss");
    assert.equal(launcher?.kind, "runtime");
    assert.equal(launcher?.executable, true);
  });
});

test("stageRelease rejects the removed single-file codeOss input", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    const { codeOssRoot, codeOssSha256, ...validFixture } = fixture;

    await assert.rejects(
      () => stageRelease({
        platform: "windows",
        architecture: "x64",
        version: "1.2.3",
        sourceCommit: "a".repeat(40),
        sourceDateEpoch,
        ...validFixture,
        codeOss: fixture.launcherPath,
      }),
      (error) => {
        assert.equal(error?.code, "RELEASE_INPUT_MISSING");
        assert.match(error.message, /codeOssRoot is required/u);
        return true;
      },
    );

    await assert.rejects(
      () => readFile(join(fixture.outRoot, "staging", "1.2.3", "windows-x64", "release-manifest.json")),
      /ENOENT/u,
    );
  });
});

test("stageRelease requires an explicit verified test-generation bundle root", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    const { testgenRoot, ...withoutTestgen } = fixture;
    await assert.rejects(
      () => stageRelease({
        platform: "windows",
        architecture: "x64",
        version: "1.2.3",
        sourceCommit: "a".repeat(40),
        sourceDateEpoch,
        ...withoutTestgen,
      }),
      (error) => error?.code === "RELEASE_INPUT_MISSING" && /testgenRoot/u.test(error.message),
    );
  });
});

test("stageRelease rejects an unverified test-generation bundle before publication", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    await assert.rejects(
      () => stageRelease({
        platform: "windows",
        architecture: "x64",
        version: "1.2.3",
        sourceCommit: "a".repeat(40),
        sourceDateEpoch,
        ...fixture,
      }),
      /manifest|Clang|bundle/u,
    );
    await assert.rejects(
      () => lstat(join(fixture.outRoot, "staging", "1.2.3", "windows-x64")),
      /ENOENT/u,
    );
  });
});

test("stage CLI rejects --code-oss and requires both runtime-root flags", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    const oldFlag = spawnSync(process.execPath, [
      resolve("tools/release/stage.mjs"),
      "--platform", "windows",
      "--architecture", "x64",
      "--version", "1.2.3",
      "--code-oss", "runtime.exe",
    ], {
      cwd: resolve("."),
      encoding: "utf8",
      windowsHide: true,
    });
    assert.equal(oldFlag.status, 1);
    assert.match(oldFlag.stderr, /unknown stage flag: --code-oss/u);

    for (const [omittedFlag, requiredKey] of [
      ["--code-oss-root", "codeOssRoot"],
      ["--code-oss-sha256", "codeOssSha256"],
    ]) {
      const argumentsList = [
        resolve("tools/release/stage.mjs"),
        "--platform", "windows",
        "--architecture", "x64",
        "--version", "1.2.3",
        "--code-oss-root", fixture.codeOssRoot,
        "--code-oss-sha256", fixture.codeOssSha256,
        "--service", fixture.service,
        "--cmake-root", fixture.cmakeRoot,
        "--coverage-root", fixture.coverageRoot,
        "--testgen-root", fixture.testgenRoot,
        "--out", fixture.outRoot,
      ];
      const flagIndex = argumentsList.indexOf(omittedFlag);
      argumentsList.splice(flagIndex, 2);
      const result = spawnSync(process.execPath, argumentsList, {
        cwd: resolve("."),
        encoding: "utf8",
        windowsHide: true,
      });
      assert.equal(result.status, 1);
      assert.match(result.stderr, new RegExp(`${requiredKey} is required`, "u"));
    }
  });
});

test("stageRelease rejects leading-space source components", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    await writeFixtureFile(fixture.cmakeRoot, " leading.txt", "unsafe bundle path\n");

    await assert.rejects(
      () => stageFixtureRelease({
        platform: "windows",
        architecture: "x64",
        version: "1.2.3",
        sourceCommit: "a".repeat(40),
        sourceDateEpoch,
        ...fixture,
      }),
      /unsafe staged path:  leading\.txt/u,
    );
  });
});

test("stageRelease rejects source components outside the portable ASCII set", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    for (const [label, relativePath] of [
      ["invalid character", "hash#name.txt"],
      ["non-ASCII", "caf\u00e9.txt"],
    ]) {
      await t.test(label, async () => {
        const fixture = await createReleaseFixture(join(root, label));
        await writeFixtureFile(fixture.cmakeRoot, relativePath, "unsafe bundle path\n");

        await assert.rejects(
          () => stageFixtureRelease({
            platform: "windows",
            architecture: "x64",
            version: "1.2.3",
            sourceCommit: "a".repeat(40),
            sourceDateEpoch,
            ...fixture,
          }),
          /unsafe staged path/u,
        );
      });
    }
  });
});

test("stage CLI rejects duplicate flags instead of overwriting", () => {
  const result = spawnSync(process.execPath, [
    resolve("tools/release/stage.mjs"),
    "--platform", "windows",
    "--platform", "linux",
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    windowsHide: true,
  });

  assert.equal(result.status, 1);
  assert.match(result.stderr, /duplicate stage flag: --platform/u);
});

test("stageRelease publishes no root after a post-copy launcher digest mismatch", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    let validationCalls = 0;
    const validateRuntime = async (options) => {
      validationCalls += 1;
      if (validationCalls === 2) {
        await writeFile(join(options.root, "Code - OSS.exe"), "mutated staged launcher\n");
      }
      return validateCodeOssRuntime(options);
    };

    await assert.rejects(
      () => stageFixtureRelease({
        platform: "windows",
        architecture: "x64",
        version: "1.2.3",
        sourceCommit: "a".repeat(40),
        sourceDateEpoch,
        ...fixture,
      }, { validateRuntime }),
      (error) => error?.code === "RELEASE_INPUT_DIGEST_MISMATCH",
    );
    assert.equal(validationCalls, 2);
    const parentRoot = join(fixture.outRoot, "staging", "1.2.3");
    await assert.rejects(() => lstat(join(parentRoot, "windows-x64")), /ENOENT/u);
    assert.equal((await readdir(parentRoot)).some((name) => name.startsWith(".stage-")), false);
  });
});

test("stage CLI accepts a valid invocation and stages the release tree", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    const preparedTestgenRoot = resolve(".superpowers/cache/testgen-bundle/22.1.8/windows-x64");
    try {
      await stat(preparedTestgenRoot);
    } catch (error) {
      if (error?.code === "ENOENT") return t.skip("verified Windows Clang bundle is not prepared");
      throw error;
    }
    const extensionDistRoot = resolve("apps/code-oss-extension/dist");
    await rm(extensionDistRoot, { recursive: true, force: true });
    await writeFixtureFile(
      extensionDistRoot,
      "src/extension-entry.cjs",
      "exports.activate = async () => undefined;\n",
    );
    await writeFixtureFile(extensionDistRoot, "src/extension.js", "export const cli = true;\n");
    t.after(async () => {
      await rm(extensionDistRoot, { recursive: true, force: true });
    });
    const result = spawnSync(process.execPath, [
      resolve("tools/release/stage.mjs"),
      "--platform", "windows",
      "--architecture", "x64",
      "--version", "1.2.3",
      "--code-oss-root", fixture.codeOssRoot,
      "--code-oss-sha256", fixture.codeOssSha256,
      "--service", fixture.service,
      "--cmake-root", fixture.cmakeRoot,
      "--coverage-root", fixture.coverageRoot,
      "--testgen-root", preparedTestgenRoot,
      "--out", fixture.outRoot,
    ], {
      cwd: resolve("."),
      encoding: "utf8",
      env: { ...process.env, SOURCE_DATE_EPOCH: sourceDateEpoch },
      windowsHide: true,
    });

    assert.equal(result.status, 0, result.stderr);
    const stagingRoot = join(fixture.outRoot, "staging", "1.2.3", "windows-x64");
    assert.equal(result.stdout.trim(), stagingRoot);
    const manifest = JSON.parse(await readFile(join(stagingRoot, "release-manifest.json"), "utf8"));
    assert.equal(manifest.version, "1.2.3");
    assert.equal(manifest.platform, "windows");
    assert.equal(manifest.architecture, "x64");
  });
});

test("release-staged Clang bundle opens with the real Go product consumer", async (t) => {
  const platform = process.platform === "win32" ? "windows" : process.platform === "linux" ? "linux" : "";
  if (!platform) return t.skip("unsupported host platform");
  const preparedTestgenRoot = resolve(`.superpowers/cache/testgen-bundle/22.1.8/${platform}-x64`);
  try {
    await stat(preparedTestgenRoot);
  } catch (error) {
    if (error?.code === "ENOENT") return t.skip("verified host Clang bundle is not prepared");
    throw error;
  }
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root, platform);
    const staged = await stageRelease({
      platform,
      architecture: "x64",
      version: "1.2.3",
      sourceCommit: "a".repeat(40),
      sourceDateEpoch,
      ...fixture,
      testgenRoot: preparedTestgenRoot,
    });
    const consumer = spawnSync("go", [
      "test", "./apps/test-service/internal/testgenbundle", "-run", "^TestOpenStagedBundleFromEnvironment$", "-count=1",
    ], {
      cwd: resolve("."),
      encoding: "utf8",
      env: { ...process.env, TESTGEN_STAGED_BUNDLE_ROOT: join(staged.stagingRoot, "bundles", "testgen") },
      windowsHide: true,
    });
    assert.equal(consumer.status, 0, `${consumer.stdout}\n${consumer.stderr}`);
    assert.match(consumer.stdout, /ok\s+unit-test-ide\.local\/test-service\/internal\/testgenbundle/u);
  });
});

test("stageRelease fails closed when SOURCE_DATE_EPOCH is absent", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    await assert.rejects(
      () => stageFixtureRelease({
        platform: "windows",
        architecture: "x64",
        version: "1.2.3",
        sourceCommit: "a".repeat(40),
        ...fixture,
      }),
      /SOURCE_DATE_EPOCH/u,
    );
  });
});

test("identical complete runtimes produce byte-identical normalized staging trees", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createReleaseFixture(root);
    const common = {
      platform: "windows",
      architecture: "x64",
      version: "1.2.3",
      sourceCommit: "a".repeat(40),
      sourceDateEpoch,
      ...fixture,
    };
    const first = await stageFixtureRelease({ ...common, outRoot: join(root, "first") });
    const second = await stageFixtureRelease({ ...common, outRoot: join(root, "second") });

    assert.deepEqual(await readFile(first.manifestPath), await readFile(second.manifestPath));
    assert.deepEqual(
      await packageInputSnapshot(first.stagingRoot),
      await packageInputSnapshot(second.stagingRoot),
    );
  });
});
