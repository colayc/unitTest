import { spawnSync } from "node:child_process";
import { mkdir } from "node:fs/promises";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");
const build = join(root, "build");
await mkdir(build, { recursive: true });
const output = join(build, process.platform === "win32" ? "unit-test-service.exe" : "unit-test-service");
const result = spawnSync("go", ["build", "-o", output, "./apps/test-service/cmd/unit-test-service"], {
  cwd: root,
  stdio: "inherit"
});
if (result.error) throw result.error;
if (result.status !== 0) process.exit(result.status ?? 1);
