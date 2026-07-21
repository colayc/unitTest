import { randomUUID } from "node:crypto";
import { join } from "node:path";

export function endpoint(tempDirectory: string): string {
  const id = randomUUID();
  return process.platform === "win32"
    ? `\\\\.\\pipe\\unit-test-ide-${id}`
    : join(tempDirectory, `unit-test-ide-${id}.sock`);
}
