import assert from "node:assert/strict";
import test from "node:test";

import {
  isPortableReleasePath,
  isPortableReleasePathComponent,
} from "./portable-path.mjs";

const realRuntimePaths = [
  "app/code-oss-runtime/resources/app/node_modules.asar.unpacked/@vscode/ripgrep/bin/rg.exe",
  "app/code-oss-runtime/resources/app/extensions/javascript/syntaxes/Regular Expressions (JavaScript).tmLanguage",
];

const unsafePaths = [
  "",
  "/app/x",
  "C:/app/x",
  "app//x",
  "app/x/",
  "app/./x",
  "app/../x",
  "app\\x",
  "app/x:y",
  "app/less<than.txt",
  "app/greater>than.txt",
  "app/quote\"name.txt",
  "app/pipe|name.txt",
  "app/question?.txt",
  "app/star*.txt",
  "app/control\u0001.txt",
  "app/hash#name.txt",
  "app/caf\u00e9.txt",
  "app/ leading.txt",
  "app/trailing ",
  "app/trailing.",
  "app/CON",
  "app/con.txt",
  "app/LPT9.log",
];

test("portable release paths accept literal real Code-OSS runtime names", () => {
  for (const value of realRuntimePaths) {
    assert.equal(isPortableReleasePath(value), true, value);
  }
  assert.equal(isPortableReleasePathComponent("@vscode"), true);
  assert.equal(isPortableReleasePathComponent("Regular Expressions (JavaScript).tmLanguage"), true);
});

test("portable release paths reject unsafe components and separators", () => {
  for (const value of unsafePaths) {
    assert.equal(isPortableReleasePath(value), false, value);
  }
  for (const value of [
    "",
    ".",
    "..",
    " leading",
    "trailing ",
    "trailing.",
    "CON.txt",
    "com1.exe",
    "hash#name",
    "caf\u00e9",
  ]) {
    assert.equal(isPortableReleasePathComponent(value), false, value);
  }
});
