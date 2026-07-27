import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFile, readdir } from "node:fs/promises";
import test from "node:test";

const serviceCommandPackage = "./apps/test-service/cmd/unit-test-service";
const servicePackagePattern = "./apps/test-service/...";
const currentServicePlatform = process.platform === "win32" ? "windows" : "linux";
const serverImportPath = "unit-test-ide.local/test-service/internal/server";
const transportImportPath = "unit-test-ide.local/test-service/internal/transport";
const winioImportPath = "github.com/Microsoft/go-winio";

function goList(arguments_) {
  return execFileSync("go", arguments_, {
    encoding: "utf8",
    windowsHide: true
  }).trim().split(/\r?\n/).filter(Boolean);
}

function isNetworkCapableImport(importPath) {
  return importPath === "net"
    || importPath.startsWith("net/")
    || importPath === "crypto/tls"
    || importPath.startsWith("github.com/google/go-github")
    || importPath.startsWith("golang.org/x/oauth2")
    || importPath === "golang.org/x/net"
    || importPath.startsWith("golang.org/x/net/")
    || importPath === winioImportPath;
}

function packageImports(line) {
  const separator = line.indexOf("|");
  assert.notEqual(separator, -1, `unexpected go list output: ${line}`);
  return {
    importPath: line.slice(0, separator),
    imports: line.slice(separator + 1).split(",").filter(Boolean)
  };
}

function calledSelectors(source, packageName) {
  return [...source.matchAll(new RegExp(`\\b${packageName}\\.([A-Za-z_]\\w*)\\s*\\(`, "g"))]
    .map((match) => match[1]);
}

function sourceImportSpecs(source) {
  const imports = [...source.matchAll(/\bimport\s+(?:([A-Za-z_.]\w*)\s+)?"([^"]+)"/g)]
    .map((match) => ({ alias: match[1], path: match[2] }));
  for (const block of source.matchAll(/\bimport\s*\(([\s\S]*?)\)/g)) {
    imports.push(...[...block[1].matchAll(/^\s*(?:([A-Za-z_.]\w*)\s+)?"([^"]+)"\s*$/gm)]
      .map((match) => ({ alias: match[1], path: match[2] })));
  }
  return imports;
}

function serverNetworkImportEscapes(sources) {
  return sources.flatMap(({ filename, source }) =>
    sourceImportSpecs(source)
      .filter((entry) => isNetworkCapableImport(entry.path) && (entry.alias || entry.path !== "net"))
      .map((entry) => ({ filename, ...entry }))
  );
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
  assert.equal((await readFile(".go-version", "utf8")).trim(), "1.26.5");
  const manifest = JSON.parse(await readFile("package.json", "utf8"));
  assert.equal(manifest.packageManager, "pnpm@11.4.0");
  assert.equal(manifest.engines.node, ">=24.18.0 <25");
});

test("README contains the complete local verification gate", async () => {
  const readme = await readFile("README.md", "utf8");
  for (const command of ["pnpm check:protocol-generated", "pnpm test", "pnpm test:e2e"]) {
    assert.match(readme, new RegExp(command.replaceAll(" ", "\\s+")));
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

test("production packages constrain network-capable code to local IPC boundaries", async () => {
  const allowedImports = new Map([
    [serverImportPath, new Set(["net"])],
    [transportImportPath, new Set(["net", winioImportPath])]
  ]);
  const packages = goList([
    "list",
    "-f",
    "{{.ImportPath}}|{{join .Imports \",\"}}",
    servicePackagePattern
  ]).map(packageImports);
  for (const package_ of packages) {
    const allowed = allowedImports.get(package_.importPath) ?? new Set();
    const escaped = package_.imports.filter((importPath) =>
      isNetworkCapableImport(importPath) && !allowed.has(importPath)
    );
    assert.deepEqual(
      escaped,
      [],
      `${currentServicePlatform} ${package_.importPath} imports network-capable code outside its local IPC boundary`
    );
  }

  const serverSources = await productionGoSources("apps/test-service/internal/server");
  const aliasMutants = [
    {
      filename: "named_alias.go",
      source: `package server
import netx "net"
func outbound() { _, _ = netx.Dial("tcp", "example.invalid:443") }
`
    },
    {
      filename: "dot_import.go",
      source: `package server
import . "net"
func outbound() { _, _ = Dial("tcp", "example.invalid:443") }
`
    }
  ];
  assert.deepEqual(
    serverNetworkImportEscapes(aliasMutants),
    [
      { filename: "named_alias.go", alias: "netx", path: "net" },
      { filename: "dot_import.go", alias: ".", path: "net" }
    ],
    "server import audit must catch named and dot aliases before selector checks"
  );
  const escapedServerImports = serverNetworkImportEscapes(serverSources);
  assert.deepEqual(
    escapedServerImports,
    [],
    "internal/server must import unaliased net only for its local IPC connection surface"
  );
  const allowedServerNetSelectors = new Set(["Addr", "Conn", "ErrClosed", "Error", "Listener"]);
  const escapedServerSelectors = serverSources.flatMap(({ source }) =>
    [...source.matchAll(/\bnet\.([A-Za-z_]\w*)/g)]
      .map((match) => match[1])
      .filter((selector) => !allowedServerNetSelectors.has(selector))
  );
  assert.deepEqual(
    [...new Set(escapedServerSelectors)].sort(),
    [],
    "internal/server must use net only for local IPC connection, interface, type, and error handling"
  );
});

test("cross-platform transports constrain network-capable calls to local IPC", async () => {
  const transportSources = await productionGoSources("apps/test-service/internal/transport");
  const allowedImports = new Map([
    ["listener.go", ["net"]],
    ["listener_unix.go", ["net"]],
    ["listener_windows.go", ["net", winioImportPath]]
  ]);
  const allowedSelectors = new Map([
    ["listener.go", { net: ["Listener"], winio: [] }],
    ["listener_unix.go", { net: ["Listener", "DialTimeout", "Listen"], winio: [] }],
    ["listener_windows.go", { net: ["Listener"], winio: ["ListenPipe", "PipeConfig"] }]
  ]);
  for (const { filename, source } of transportSources) {
    const imports = sourceImportSpecs(source);
    assert.deepEqual(
      imports.filter((entry) => entry.alias && isNetworkCapableImport(entry.path)),
      [],
      `${filename} must not alias network-capable imports`
    );
    assert.deepEqual(
      imports.map((entry) => entry.path).filter(isNetworkCapableImport),
      allowedImports.get(filename) ?? [],
      `${filename} imports network-capable code outside its local IPC boundary`
    );
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
