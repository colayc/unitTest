import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import type { Diagnostic } from "@unit-test-ide/protocol-models";

import {
  __testing,
  copyNativeFixture,
  normalizeNativeDiagnostic,
  type GoldenDiagnosticExpectation,
} from "./native-fixture.js";

const repositoryRoot = resolve(import.meta.dirname, "../../..");
const fixtureRoot = join(repositoryRoot, "testdata", "toolchains");
const errorSeverity = "error" as Diagnostic["severity"];

async function sha256(path: string): Promise<string> {
  return createHash("sha256").update(await readFile(path)).digest("hex");
}

test("copyNativeFixture creates an isolated workspace without modifying the repository fixture", async (t) => {
  const parent = await mkdtemp(join(tmpdir(), "native-fixture-copy-"));
  t.after(() => rm(parent, { recursive: true, force: true }));
  const source = join(fixtureRoot, "preset-project", "src", "main.cpp");
  const before = await sha256(source);

  const destination = await copyNativeFixture("preset-project", parent);
  assert.notEqual(destination, join(fixtureRoot, "preset-project"));
  assert.equal(
    await readFile(join(destination, "src", "main.cpp"), "utf8"),
    await readFile(source, "utf8"),
  );
  await writeFile(join(destination, "src", "main.cpp"), "changed copy\n");
  assert.equal(await sha256(source), before);
});

test("copyNativeFixture supports a new workspace directory containing spaces and Unicode", async (t) => {
  const parent = await mkdtemp(join(tmpdir(), "native-fixture-unicode-"));
  t.after(() => rm(parent, { recursive: true, force: true }));

  const destination = await copyNativeFixture(
    "fallback-project",
    parent,
    "native 空格 Ω",
  );
  assert.equal(destination, join(parent, "native 空格 Ω"));
  assert.equal(
    await readFile(join(destination, "src", "main.cpp"), "utf8"),
    await readFile(
      join(fixtureRoot, "fallback-project", "src", "main.cpp"),
      "utf8",
    ),
  );
  await assert.rejects(
    copyNativeFixture("fallback-project", parent, "native 空格 Ω"),
    /destination must be a new directory/,
  );
});

test("safe fixture copy rejects symlink or junction content without following it", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "native-fixture-link-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const source = join(root, "source");
  const destination = join(root, "destination");
  const outside = join(root, "outside");
  await mkdir(source);
  await mkdir(outside);
  await writeFile(join(outside, "secret.txt"), "outside");
  try {
    await symlink(
      outside,
      join(source, "escape"),
      process.platform === "win32" ? "junction" : "dir",
    );
  } catch (error: unknown) {
    if ((error as NodeJS.ErrnoException).code === "EPERM") {
      return;
    }
    throw error;
  }

  await assert.rejects(
    __testing.copyFixtureTree(source, destination),
    /unsafe fixture entry/,
  );
  await assert.rejects(lstat(destination), { code: "ENOENT" });
});

test("copyNativeFixture rejects names and fixture keys that could escape the destination", async (t) => {
  const parent = await mkdtemp(join(tmpdir(), "native-fixture-escape-"));
  t.after(() => rm(parent, { recursive: true, force: true }));
  for (const directoryName of ["../escape", "nested/name", "C:\\escape", ".", ".."]) {
    await assert.rejects(
      copyNativeFixture("preset-project", parent, directoryName),
      /invalid fixture destination name/,
    );
  }
  await assert.rejects(
    copyNativeFixture("../preset-project" as never, parent),
    /unknown native fixture/,
  );
});

test("fixture entry ordering is deterministic by Unicode code point", () => {
  assert.deepEqual(
    ["😀", "Ω", "z", "a"].sort(__testing.compareByCodePoint),
    ["a", "z", "Ω", "😀"],
  );
});

test("normalizeNativeDiagnostic handles Windows paths before normalizing separators", () => {
  const diagnostic: Diagnostic = {
    severity: errorSeverity,
    code: "C2065",
    message: String.raw`C:\Work Space\src\main.cpp(3,10): unknown identifier`,
    sourceUri: String.raw`c:\work space\src\main.cpp`,
    line: 3,
    column: 10,
  };
  assert.deepEqual(
    normalizeNativeDiagnostic(diagnostic, {
      workspace: String.raw`C:\Work Space`,
      build: String.raw`C:\Work Space\.native-e2e\build`,
    }),
    {
      ...diagnostic,
      sourceUri: "<workspace>/src/main.cpp",
      message: "<workspace>/src/main.cpp(3,10): unknown identifier",
    },
  );
});

test("normalizeNativeDiagnostic handles POSIX paths and prioritizes the build root", () => {
  const diagnostic: Diagnostic = {
    severity: errorSeverity,
    code: "",
    message: "/home/runner/work/native/build/CMakeFiles/link.txt: link failed",
    sourceUri: "file:///home/runner/work/native/build/CMakeFiles/link.txt",
    line: 1,
  };
  assert.deepEqual(
    normalizeNativeDiagnostic(diagnostic, {
      workspace: "/home/runner/work/native",
      build: "/home/runner/work/native/build",
    }),
    {
      ...diagnostic,
      sourceUri: "<build>/CMakeFiles/link.txt",
      message: "<build>/CMakeFiles/link.txt: link failed",
    },
  );
});

test("normalizeNativeDiagnostic maps strict workspace URIs to the workspace root", () => {
  const roots = {
    workspace: String.raw`C:\workspace`,
    build: String.raw`C:\workspace\build`,
  };
  assert.equal(
    normalizeNativeDiagnostic(
      { severity: errorSeverity, code: "", sourceUri: "workspace:///src/main.cpp", message: "" },
      roots,
    ).sourceUri,
    "<workspace>/src/main.cpp",
  );
  assert.equal(
    normalizeNativeDiagnostic(
      { severity: errorSeverity, code: "", sourceUri: "workspace:///src/%E6%B5%8B.cpp", message: "" },
      roots,
    ).sourceUri,
    "<workspace>/src/测.cpp",
  );
});

test("normalizeNativeDiagnostic leaves invalid workspace URIs unchanged", () => {
  const roots = {
    workspace: String.raw`C:\workspace`,
    build: String.raw`C:\workspace\build`,
  };
  for (const value of [
    "workspace://host/src/main.cpp",
    "workspace:///../secret.cpp",
    "workspace:///C:/secret.cpp",
  ]) {
    assert.equal(
      normalizeNativeDiagnostic(
        { severity: errorSeverity, code: "", sourceUri: value, message: "" },
        roots,
      ).sourceUri,
      value,
    );
  }
});

test("normalizeNativeDiagnostic maps explicitly trusted external roots without exposing host paths", () => {
  const diagnostic: Diagnostic = {
    severity: errorSeverity,
    code: "CMAKE_ERROR",
    message: String.raw`C:\Tools\CMake\share\Modules\Compiler.cmake: failed`,
    sourceUri: "file:///C:/Tools/CMake/share/Modules/Compiler.cmake",
  };
  assert.deepEqual(
    normalizeNativeDiagnostic(diagnostic, {
      workspace: String.raw`C:\workspace`,
      build: String.raw`C:\service\build`,
      external: [String.raw`C:\Tools\CMake`],
    }),
    {
      ...diagnostic,
      sourceUri: "<external>/share/Modules/Compiler.cmake",
      message: "<external>/share/Modules/Compiler.cmake: failed",
    },
  );
});

test("normalization preserves diagnostic identity and unrelated source text", () => {
  const diagnostic: Diagnostic = {
    severity: errorSeverity,
    code: "-Wfixture",
    message: "workspace planning text mentions C:\\workbench but no source path",
    sourceUri: "https://example.invalid/source.cpp",
    line: 7,
    column: 3,
  };
  const normalized = normalizeNativeDiagnostic(diagnostic, {
    workspace: String.raw`C:\work`,
    build: String.raw`C:\work\build`,
  });
  assert.deepEqual(normalized, diagnostic);
  assert.notEqual(normalized, diagnostic);
});

test("normalization does not treat a sibling path as a child root", () => {
  const diagnostic: Diagnostic = {
    severity: errorSeverity,
    code: "E",
    message: String.raw`C:\workspace-other\main.cpp failed`,
    sourceUri: String.raw`C:\workspace-other\main.cpp`,
  };
  assert.deepEqual(
    normalizeNativeDiagnostic(diagnostic, {
      workspace: String.raw`C:\workspace`,
      build: String.raw`C:\workspace\build`,
    }),
    diagnostic,
  );
});

test("normalization keeps a foreign file URI authority outside local roots", () => {
  const diagnostic: Diagnostic = {
    severity: errorSeverity,
    code: "E",
    message: "remote source",
    sourceUri: "file://build-host/C:/workspace/src/main.cpp",
  };
  assert.deepEqual(
    normalizeNativeDiagnostic(diagnostic, {
      workspace: String.raw`C:\workspace`,
      build: String.raw`C:\workspace\build`,
    }),
    diagnostic,
  );
});

test("tracked golden diagnostics keep the cross-compiler minimum contract", async () => {
  const names = [
    "compiler-gcc-clang.json",
    "compiler-msvc-clang-cl.json",
    "linker-gcc-clang.json",
    "linker-msvc-clang-cl.json",
    "configure.json",
  ];
  for (const name of names) {
    const document = JSON.parse(
      await readFile(join(fixtureRoot, "golden", name), "utf8"),
    ) as { minimum: GoldenDiagnosticExpectation[] };
    assert.ok(Array.isArray(document.minimum) && document.minimum.length > 0, name);
    for (const expectation of document.minimum) {
      assert.match(expectation.kind, /^(configure|compiler|linker)$/);
      assert.match(expectation.severity, /^(warning|error)$/);
      assert.ok(expectation.messageContains.length > 0);
      if (expectation.file !== undefined) {
        assert.match(expectation.file, /^<(workspace|build)>\//);
      }
      if (expectation.codePattern !== undefined) {
        const codePattern = expectation.codePattern;
        const pattern = new RegExp(codePattern);
        assert.equal(pattern.test(""), false, `${name} accepts an empty diagnostic code`);
      }
    }
  }
});

test("MSVC linker fixture uses a supported unresolved entrypoint option", async () => {
  const cmake = await readFile(
    join(fixtureRoot, "failures", "linker", "CMakeLists.txt"),
    "utf8",
  );
  assert.match(
    cmake,
    /target_link_options\s*\(\s*linker_failure\s+PRIVATE\s+"\/entry:native_missing_symbol"\s*\)/,
  );
});
