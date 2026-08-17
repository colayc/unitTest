import assert from "node:assert/strict";
import { posix, win32 } from "node:path";
import test from "node:test";
import { resolveNativeWorkDirectory } from "./native-work-root.js";

test("Windows native work root stays short across normal clones and managed worktrees", () => {
  assert.equal(
    resolveNativeWorkDirectory(
      "C:\\source\\unitTest",
      "win32",
      "C:\\Users\\runner\\AppData\\Local\\Temp",
    ),
    "C:\\source\\unitTest\\.native-e2e\\work",
  );
  assert.equal(
    resolveNativeWorkDirectory(
      "C:\\source\\unitTest\\.worktrees\\phase3",
      "win32",
      "C:\\Users\\runner\\AppData\\Local\\Temp",
    ),
    "C:\\source\\unitTest\\.native-e2e\\work",
  );
  assert.equal(
    win32.isAbsolute(resolveNativeWorkDirectory("C:\\source\\unitTest", "win32", "C:\\Temp")),
    true,
  );
});

test("Linux native work root remains under the system temporary directory", () => {
  const result = resolveNativeWorkDirectory("/source/unitTest", "linux", "/tmp");
  assert.equal(result, "/tmp/uti-native");
  assert.equal(posix.isAbsolute(result), true);
});

test("native work root rejects relative and malformed roots", () => {
  assert.throws(
    () => resolveNativeWorkDirectory("relative", "win32", "C:\\Temp"),
    /repository root/,
  );
  assert.throws(
    () => resolveNativeWorkDirectory("/source/unitTest", "linux", "relative"),
    /temporary root/,
  );
  assert.throws(
    () => resolveNativeWorkDirectory("C:\\source\\unitTest\0bad", "win32", "C:\\Temp"),
    /repository root/,
  );
});
