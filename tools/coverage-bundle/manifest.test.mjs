import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import Ajv2020 from "ajv/dist/2020.js";

const root = new URL("./", import.meta.url);
const expectedProjects = [
  "colorama",
  "colorlog",
  "gcovr",
  "jinja2",
  "lxml",
  "markupsafe",
  "pygments",
];
const sha256 = /^[0-9a-f]{64}$/u;
const exactVersion = /^\d+(?:\.\d+)*$/u;
const sourceHosts = new Set(["www.python.org", "files.pythonhosted.org", "quay.io"]);

async function readJson(name) {
  return JSON.parse(await readFile(new URL(name, root), "utf8"));
}

function validate(schema, value) {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  const check = ajv.compile(schema);
  assert.equal(check(value), true, ajv.errorsText(check.errors));
}

function clone(value) {
  return structuredClone(value);
}

function hasUniqueProjects(manifest) {
  const projects = manifest.gcovr.wheels.map(({ project }) => project);
  return new Set(projects).size === projects.length;
}

test("tracked manifest fixes the Python 3.14.6 and gcovr 8.6 source lock", async () => {
  const [schema, manifest] = await Promise.all([
    readJson("manifest.schema.json"),
    readJson("manifest.json"),
  ]);
  validate(schema, manifest);

  assert.equal(manifest.python.version, "3.14.6");
  assert.equal(manifest.gcovr.version, "8.6");
  assert.deepEqual(Object.keys(manifest.python.artifacts).sort(), ["linux-x64", "windows-x64"]);
  assert.equal(manifest.python.artifacts["windows-x64"].kind, "embedded-archive");
  assert.equal(manifest.python.artifacts["linux-x64"].kind, "source-archive");
  assert.equal(manifest.linux.builder.image, "quay.io/pypa/manylinux_2_28_x86_64@sha256:c7123a4aebb153c1e45b8152f07a64bd950d65e630cfb633a029cc45ee21897c");
  assert.equal(manifest.linux.glibcBaseline, "2.28");
  assert.equal(manifest.linux.muslPolicy, "unsupported");
  assert.deepEqual(
    {
      windowsPython: manifest.python.artifacts["windows-x64"].sha256,
      linuxPython: manifest.python.artifacts["linux-x64"].sha256,
      wheels: Object.fromEntries(manifest.gcovr.wheels.map(({ project, files }) => [
        project,
        files.map(({ filename, sha256: digest }) => `${filename}:${digest}`),
      ])),
    },
    {
      windowsPython: "df901e84a896ff1ee720ad03377e0c8d8c2244fda79808aeeaff6316df1cb75c",
      linuxPython: "74d0d71d0600e477651a077101d6e62d1e2e69b8e992ba18c993dd643b7ba222",
      wheels: {
        gcovr: ["gcovr-8.6-py3-none-any.whl:dbf9d87c38042752ad6f530aa8210427e22b526611bb7b7bfed0e81977d1f1ef"],
        colorlog: ["colorlog-6.10.1-py3-none-any.whl:2d7e8348291948af66122cff006c9f8da6255d224e7cf8e37d8de2df3bad8c9c"],
        colorama: ["colorama-0.4.6-py2.py3-none-any.whl:4f1d9991f5acc0ca119f9d443620b77f9d6b33703e51011c16baf57afb285fc6"],
        jinja2: ["jinja2-3.1.6-py3-none-any.whl:85ece4451f492d0c13c5dd7c13a64681a86afae63a5f347908daf103ce6d2f67"],
        markupsafe: [
          "markupsafe-3.0.3-cp314-cp314-win_amd64.whl:bdc919ead48f234740ad807933cdf545180bfbe9342c2bb451556db2ed958581",
          "markupsafe-3.0.3-cp314-cp314-manylinux2014_x86_64.manylinux_2_17_x86_64.manylinux_2_28_x86_64.whl:457a69a9577064c05a97c41f4e65148652db078a3a509039e64d3467b9e7ef97",
        ],
        lxml: [
          "lxml-6.0.2-cp314-cp314-win_amd64.whl:fa25afbadead523f7001caf0c2382afd272c315a033a7b06336da2637d92d6ed",
          "lxml-6.0.2-cp314-cp314-manylinux_2_26_x86_64.manylinux_2_28_x86_64.whl:98a5e1660dc7de2200b00d53fa00bcd3c35a3608c305d45a7bbcaf29fa16e83d",
        ],
        pygments: ["pygments-2.19.2-py3-none-any.whl:86540386c03d588bb81d44bc3928634ff26449851e99741617ecb9037ee5ec0b"],
      },
    },
  );
});

test("source artifacts and every locked wheel use reviewed HTTPS URLs and lowercase SHA-256", async () => {
  const manifest = await readJson("manifest.json");
  const artifacts = [
    ...Object.values(manifest.python.artifacts),
    ...manifest.gcovr.wheels.flatMap(({ files }) => files),
  ];
  assert.ok(artifacts.length > 0);
  for (const artifact of artifacts) {
    const url = new URL(artifact.url);
    assert.equal(url.protocol, "https:");
    assert.ok(sourceHosts.has(url.hostname), artifact.url);
    assert.equal(url.username, "");
    assert.equal(url.password, "");
    assert.equal(url.search, "");
    assert.equal(url.hash, "");
    assert.match(artifact.sha256, sha256);
    assert.doesNotMatch(artifact.url, /latest|(?:^|[/@])(?:main|master|develop)(?:$|[/@])|\.git(?:$|[?#])/iu);
  }
});

test("wheel lock has exact versions, unique normalized projects, no markers, editable packages, or sdists", async () => {
  const manifest = await readJson("manifest.json");
  const projects = new Set();
  for (const wheel of manifest.gcovr.wheels) {
    assert.match(wheel.project, /^[a-z0-9]+(?:-[a-z0-9]+)*$/u);
    assert.ok(!projects.has(wheel.project), `duplicate project: ${wheel.project}`);
    projects.add(wheel.project);
    assert.match(wheel.version, exactVersion);
    assert.equal(wheel.kind, "wheel");
    assert.equal(wheel.marker, undefined);
    for (const file of wheel.files) {
      assert.doesNotMatch(file.filename, /(?:\.tar\.gz|\.zip|\.git)$/iu);
      assert.doesNotMatch(file.url, /(?:^|[?#])(?:editable|egg)=|git\+/iu);
      assert.ok(Array.isArray(file.platforms) && file.platforms.length > 0);
    }
  }
  assert.deepEqual([...projects].sort(), expectedProjects);
});

test("license contract is complete for each locked package and the two bundled licenses are present", async () => {
  const [manifest, dependencies, pythonLicense, gcovrLicense] = await Promise.all([
    readJson("manifest.json"),
    readJson("licenses/dependencies.json"),
    readFile(new URL("licenses/Python-3.14.6.txt", root), "utf8"),
    readFile(new URL("licenses/gcovr-8.6.txt", root), "utf8"),
  ]);
  assert.match(pythonLicense, /PYTHON SOFTWARE FOUNDATION LICENSE VERSION 2/u);
  assert.match(gcovrLicense, /BSD 3-Clause License/u);
  assert.equal(dependencies.schemaVersion, 1);
  assert.equal(dependencies.python.licenseFile, "Python-3.14.6.txt");
  assert.equal(dependencies.gcovr.licenseFile, "gcovr-8.6.txt");
  assert.deepEqual(
    dependencies.packages.map(({ project }) => project).sort(),
    manifest.gcovr.wheels.map(({ project }) => project).sort(),
  );
  for (const dependency of dependencies.packages) {
    assert.match(dependency.version, exactVersion);
    assert.match(dependency.license, /\S/u);
    assert.match(dependency.licenseTextId, /\S/u);
    assert.match(dependencies.licenseTexts[dependency.licenseTextId], /\S[\s\S]{80,}/u);
    assert.match(dependency.notice, /\S[\s\S]{20,}/u);
    assert.equal(new URL(dependency.licenseSource).protocol, "https:");
  }
});

test("schema is closed and rejects version drift, unsafe sources, lock ambiguity, and unpinned transitive dependencies", async () => {
  const [schema, manifest] = await Promise.all([
    readJson("manifest.schema.json"),
    readJson("manifest.json"),
  ]);
  const cases = [
    ["top-level additional property", (value) => { value.latest = true; }],
    ["wrong Python version", (value) => { value.python.version = "3.14.7"; }],
    ["wrong gcovr version", (value) => { value.gcovr.version = "8.6.1"; }],
    ["foreign URL", (value) => { value.python.artifacts["linux-x64"].url = "https://example.com/Python.tgz"; }],
    ["HTTP URL", (value) => { value.gcovr.wheels[0].files[0].url = "http://files.pythonhosted.org/wheel.whl"; }],
    ["uppercase SHA-256", (value) => { value.gcovr.wheels[0].files[0].sha256 = value.gcovr.wheels[0].files[0].sha256.toUpperCase(); }],
    ["placeholder SHA-256", (value) => { value.gcovr.wheels[0].files[0].sha256 = "0".repeat(64); }],
    ["open platform", (value) => { value.gcovr.wheels[0].files[0].platforms = ["darwin-x64"]; }],
    ["range version", (value) => { value.gcovr.wheels[0].version = ">=8.6"; }],
    ["marker ambiguity", (value) => { value.gcovr.wheels[0].marker = 'python_version >= "3.10"'; }],
    ["sdist fallback", (value) => { value.gcovr.wheels[0].kind = "sdist"; }],
    ["duplicate project", (value) => { value.gcovr.wheels.push(clone(value.gcovr.wheels[0])); }],
  ];
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  const check = ajv.compile(schema);
  for (const [name, mutate] of cases) {
    const value = clone(manifest);
    mutate(value);
    assert.equal(check(value) && hasUniqueProjects(value), false, name);
  }
});

test("schema predefines a closed resolved output record without pre-creating Task 2 digests", async () => {
  const schema = await readJson("manifest.schema.json");
  const valid = {
    path: "runtime/python/python.exe",
    sha256: "a".repeat(64),
    kind: "regular-file",
  };
  const outputSchema = { $defs: schema.$defs, $ref: "#/$defs/resolvedOutput" };
  validate(outputSchema, valid);
  for (const mutate of [
    (value) => { delete value.sha256; },
    (value) => { value.sha256 = "A".repeat(64); },
    (value) => { value.sha256 = "0".repeat(64); },
    (value) => { value.path = "../escape"; },
    (value) => { value.extra = true; },
  ]) {
    const value = clone(valid);
    mutate(value);
    const ajv = new Ajv2020({ allErrors: true, strict: true });
    const check = ajv.compile(outputSchema);
    assert.equal(check(value), false);
  }
});
