import assert from "node:assert/strict";
import { mkdtemp, mkdir, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, posix, win32 } from "node:path";
import test from "node:test";
import {
  resolveProductLayout,
  validateProductLayout,
  type ProductLayout
} from "../src/service-layout.js";

test("release-shaped extension resolves the sibling service and generation bundles", () => {
  const windows = resolveProductLayout(
    "C:\\Program Files\\Unit Test IDE\\app\\extensions\\unit-test-ide",
    "win32",
    false
  );
  assert.deepEqual(windows, {
    productRoot: "C:\\Program Files\\Unit Test IDE",
    serviceExecutable: "C:\\Program Files\\Unit Test IDE\\service\\unit-test-service.exe",
    cmakeBundleRoot: "C:\\Program Files\\Unit Test IDE\\bundles\\cmake",
    coverageBundleRoot: "C:\\Program Files\\Unit Test IDE\\bundles\\coverage",
    testgenBundleRoot: "C:\\Program Files\\Unit Test IDE\\bundles\\testgen"
  });

  const linux = resolveProductLayout(
    "/opt/unit test ide/app/extensions/unit-test-ide",
    "linux",
    false
  );
  assert.deepEqual(linux, {
    productRoot: "/opt/unit test ide",
    serviceExecutable: "/opt/unit test ide/service/unit-test-service",
    cmakeBundleRoot: "/opt/unit test ide/bundles/cmake",
    coverageBundleRoot: "/opt/unit test ide/bundles/coverage",
    testgenBundleRoot: "/opt/unit test ide/bundles/testgen"
  });
});

test("production layout rejects unexpected depth and Windows network or device roots", () => {
  for (const value of [
    "C:\\product\\extensions\\unit-test-ide",
    "C:\\product\\app\\extensions\\other",
    "\\\\server\\share\\app\\extensions\\unit-test-ide",
    "\\\\?\\C:\\product\\app\\extensions\\unit-test-ide"
  ]) {
    assert.throws(() => resolveProductLayout(value, "win32", false), /product layout is unavailable/);
  }
  assert.throws(
    () => resolveProductLayout("/opt/product/extensions/unit-test-ide", "linux", false),
    /product layout is unavailable/
  );
});

async function createReleaseLayout(): Promise<{ root: string; layout: ProductLayout }> {
  const root = await mkdtemp(join(tmpdir(), "utide product layout "));
  const extensionPath = join(root, "app", "extensions", "unit-test-ide");
  await Promise.all([
    mkdir(extensionPath, { recursive: true }),
    mkdir(join(root, "service"), { recursive: true }),
    mkdir(join(root, "bundles", "cmake"), { recursive: true }),
    mkdir(join(root, "bundles", "coverage"), { recursive: true }),
    mkdir(join(root, "bundles", "testgen"), { recursive: true })
  ]);
  await writeFile(
    join(root, "service", process.platform === "win32" ? "unit-test-service.exe" : "unit-test-service"),
    "service"
  );
  return { root, layout: resolveProductLayout(extensionPath, process.platform, false) };
}

test("layout validation rejects missing components before service startup", async (t) => {
  const fixture = await createReleaseLayout();
  t.after(() => rm(fixture.root, { recursive: true, force: true }));
  await validateProductLayout(fixture.layout);

  await rm(fixture.layout.testgenBundleRoot, { recursive: true, force: true });
  await assert.rejects(validateProductLayout(fixture.layout), /product layout is unavailable/);
});

test("layout validation rejects a linked bundle component", async (t) => {
  const fixture = await createReleaseLayout();
  t.after(() => rm(fixture.root, { recursive: true, force: true }));
  const outside = await mkdtemp(join(tmpdir(), "utide outside "));
  t.after(() => rm(outside, { recursive: true, force: true }));
  await rm(fixture.layout.coverageBundleRoot, { recursive: true, force: true });
  try {
    await symlink(outside, fixture.layout.coverageBundleRoot, process.platform === "win32" ? "junction" : "dir");
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "EPERM") {
      t.skip("creating a Windows junction is unavailable");
      return;
    }
    throw error;
  }
  await assert.rejects(validateProductLayout(fixture.layout), /product layout is unavailable/);
});

test("resolved paths use only the selected platform path grammar", () => {
  const windows = resolveProductLayout("C:\\p\\app\\extensions\\unit-test-ide", "win32", false);
  assert.equal(win32.basename(windows.serviceExecutable), "unit-test-service.exe");
  const linux = resolveProductLayout("/p/app/extensions/unit-test-ide", "linux", false);
  assert.equal(posix.basename(linux.serviceExecutable), "unit-test-service");
});
