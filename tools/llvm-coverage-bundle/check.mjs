import { checkBundle } from "./prepare.mjs";

const args = process.argv.slice(2);
const rootIndex = args.indexOf("--root");
const platformIndex = args.indexOf("--platform");
const root = rootIndex >= 0 ? args[rootIndex + 1] : undefined;
const platform = platformIndex >= 0 ? args[platformIndex + 1] : undefined;
const invalidPlatform = platformIndex >= 0 && (!platform || platform.startsWith("--") || platform !== "linux-x64");
if (invalidPlatform) {
  process.stderr.write(`unsupported LLVM coverage platform: ${platform ?? ""}\n`);
  process.exitCode = 1;
} else {
  checkBundle({ root })
  .then((result) => process.stdout.write(`${result.root}\n`))
  .catch((error) => { process.stderr.write(`${error.message}\n`); process.exitCode = 1; });
}
