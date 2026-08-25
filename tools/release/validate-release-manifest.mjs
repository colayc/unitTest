import { readFile } from "node:fs/promises";

import { validateReleaseManifestRecord } from "./release-manifest-validation.mjs";

function parse(argv) {
  const values = {};
  const allowed = new Set(["--manifest", "--platform", "--architecture", "--version"]);
  for (let index = 0; index < argv.length; index += 2) {
    const flag = argv[index];
    const value = argv[index + 1];
    if (!allowed.has(flag)) throw new Error(`unknown manifest validation flag: ${flag}`);
    if (typeof value !== "string" || value.length === 0 || value.startsWith("--")) {
      throw new Error(`missing value for manifest validation flag: ${flag}`);
    }
    if (Object.hasOwn(values, flag)) throw new Error(`duplicate manifest validation flag: ${flag}`);
    values[flag] = value;
  }
  if (!values["--manifest"]) throw new Error("--manifest is required");
  return values;
}

async function main(argv) {
  const values = parse(argv);
  const manifest = JSON.parse(await readFile(values["--manifest"], "utf8"));
  validateReleaseManifestRecord(manifest, {
    platform: values["--platform"],
    architecture: values["--architecture"],
    version: values["--version"],
  });
}

main(process.argv.slice(2)).catch((error) => {
  process.stdout.write(`${error.message}\n`);
  process.exitCode = 1;
});
