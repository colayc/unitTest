import { posix, win32 } from "node:path";

export function resolveNativeWorkDirectory(
  repositoryRoot: string,
  platform: "linux" | "win32",
  temporaryRoot: string,
): string {
  const path = platform === "win32" ? win32 : posix;
  requireAbsoluteRoot(repositoryRoot, path.isAbsolute(repositoryRoot), "repository root");
  if (platform === "linux") {
    requireAbsoluteRoot(temporaryRoot, path.isAbsolute(temporaryRoot), "temporary root");
    return path.join(path.normalize(temporaryRoot), "uti-native");
  }

  const normalizedRepository = path.normalize(repositoryRoot);
  const parent = path.dirname(normalizedRepository);
  const managedRoot = path.basename(parent).toLowerCase() === ".worktrees"
    ? path.dirname(parent)
    : normalizedRepository;
  return path.join(managedRoot, ".native-e2e", "work");
}

function requireAbsoluteRoot(value: string, absolute: boolean, label: string): void {
  if (!absolute || value.includes("\0")) {
    throw new Error(`native ${label} must be an absolute path`);
  }
}
