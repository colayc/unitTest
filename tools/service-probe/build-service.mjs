import { spawnSync } from "node:child_process";
import { mkdir } from "node:fs/promises";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");
const build = join(root, "build");
await mkdir(build, { recursive: true });
const programs = [
  ["unit-test-service", "./apps/test-service/cmd/unit-test-service"],
  ["cmake-fixture", "./apps/test-service/cmd/cmake-fixture"],
  ["ctest", "./apps/test-service/cmd/cmake-fixture"]
];
for (const [name, pkg] of programs) {
  const output = join(build, process.platform === "win32" ? `${name}.exe` : name);
  const result = spawnSync("go", ["build", "-o", output, pkg], {
    cwd: root,
    stdio: "inherit"
  });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}
