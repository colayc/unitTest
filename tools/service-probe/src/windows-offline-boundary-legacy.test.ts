import assert from "node:assert/strict";
import { execFile as execFileCallback } from "node:child_process";
import { lstat, mkdtemp, readdir, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import { promisify } from "node:util";

const execFile = promisify(execFileCallback);
const pwsh = process.env.PWSH?.trim() || "pwsh";
const boundaryScript = resolve(import.meta.dirname, "../scripts/windows-offline-boundary.ps1");
const fixtureScript = resolve(import.meta.dirname, "../testdata/windows-legacy-cleanup-fixture.ps1");
const ruleName = "UnitTestIDE-NativeOffline-0123456789abcdef";

async function runFixture(
  root: string,
  stateScenario: "Valid" | "WrongNonce" | "WrongOwnerPid" | "WrongRuleMarker" | "ExtraMarker" | "UnknownState",
  ruleScenario: "Valid" | "ExtraRule" | "WrongAction" = "Valid",
  mutationScenario: "None" | "LateUnknownMarker" | "LateExtraLeaf" | "LateReparseReplacement" = "None",
): Promise<{ readonly code: number; readonly stderr: string }> {
  const stateRoot = join(root, "state-root");
  try {
    await execFile(pwsh, [
      "-NoLogo",
      "-NoProfile",
      "-File",
      fixtureScript,
      "-BoundaryScript",
      boundaryScript,
      "-RuleName",
      ruleName,
      "-StateRoot",
      stateRoot,
      "-StateScenario",
      stateScenario,
      "-RuleScenario",
      ruleScenario,
      "-MutationScenario",
      mutationScenario,
    ], {
      encoding: "utf8",
      windowsHide: true,
      timeout: 30_000,
    });
    return { code: 0, stderr: "" };
  } catch (error) {
    const failure = error as NodeJS.ErrnoException & { stderr?: string; code?: string | number };
    return {
      code: typeof failure.code === "number" ? failure.code : 1,
      stderr: failure.stderr ?? String(error),
    };
  }
}

test("legacy cleanup removes only canonically audited historical residue", {
  skip: process.platform === "win32" ? false : "legacy cleanup fixture runs only on Windows",
}, async (t) => {
  const root = await mkdtemp(join(tmpdir(), "utide-legacy-cleanup-valid-"));
  const result = await runFixture(root, "Valid");
  t.after(async () => {
    const { rm } = await import("node:fs/promises");
    await rm(root, { recursive: true, force: true });
  });
  assert.equal(result.code, 0, result.stderr);
  assert.deepEqual(await readdir(join(root, "state-root")), []);
});

for (const scenario of [
  {
    mutation: "LateUnknownMarker",
    pattern: /unknown|marker/u,
    assertPreserved: async (root: string) => {
      assert.equal((await stat(join(root, "state-root", ruleName, "late.marker"))).isFile(), true);
    },
  },
  {
    mutation: "LateExtraLeaf",
    pattern: /unknown|file/u,
    assertPreserved: async (root: string) => {
      assert.equal((await stat(join(root, "state-root", "late-extra.txt"))).isFile(), true);
    },
  },
  {
    mutation: "LateReparseReplacement",
    pattern: /reparse|unsafe/u,
    assertPreserved: async (root: string) => {
      assert.equal((await stat(join(root, "late-reparse-target"))).isDirectory(), true);
      assert.equal((await lstat(join(root, "state-root", ruleName))).isSymbolicLink(), true);
    },
  },
] as const) {
  test(`legacy cleanup rejects late ${scenario.mutation} TOCTOU residue without deleting it`, {
    skip: process.platform === "win32" ? false : "legacy cleanup fixture runs only on Windows",
  }, async (t) => {
    const root = await mkdtemp(join(tmpdir(), `utide-legacy-cleanup-${scenario.mutation.toLowerCase()}-`));
    t.after(async () => {
      const { rm } = await import("node:fs/promises");
      await rm(root, { recursive: true, force: true });
    });
    const result = await runFixture(root, "Valid", "Valid", scenario.mutation);
    assert.notEqual(result.code, 0, "fixture unexpectedly succeeded");
    assert.match(result.stderr, scenario.pattern);
    await scenario.assertPreserved(root);
  });
}

for (const scenario of [
  { state: "WrongNonce", rule: "Valid", pattern: /nonce|invalid/u },
  { state: "WrongOwnerPid", rule: "Valid", pattern: /owner|pid|invalid/u },
  { state: "WrongRuleMarker", rule: "Valid", pattern: /rule|canonical|invalid/u },
  { state: "ExtraMarker", rule: "Valid", pattern: /marker|unknown/u },
  { state: "UnknownState", rule: "Valid", pattern: /unknown/u },
  { state: "Valid", rule: "ExtraRule", pattern: /extra|rule|unknown/u },
  { state: "Valid", rule: "WrongAction", pattern: /action|invalid/u },
] as const) {
  test(`legacy cleanup rejects ${scenario.state}/${scenario.rule} residue without deleting it`, {
    skip: process.platform === "win32" ? false : "legacy cleanup fixture runs only on Windows",
  }, async (t) => {
    const root = await mkdtemp(join(tmpdir(), `utide-legacy-cleanup-${scenario.state.toLowerCase()}-`));
    t.after(async () => {
      const { rm } = await import("node:fs/promises");
      await rm(root, { recursive: true, force: true });
    });
    const result = await runFixture(root, scenario.state, scenario.rule);
    assert.notEqual(result.code, 0, "fixture unexpectedly succeeded");
    assert.match(result.stderr, scenario.pattern);
    const stateDirectory = join(root, "state-root", ruleName);
    assert.equal((await stat(stateDirectory)).isDirectory(), true, "invalid residue must not be deleted");
  });
}
