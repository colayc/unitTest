import { copyFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.cwd();
const archive = resolve(root, ".release/evidence/cli-handshake-local/cmake-4.3.4-windows-x86_64.zip");
const module = await import(pathToFileURL(resolve(root, "tools/cmake-bundle/prepare.mjs")).href);
const result = await module.prepareBundle({
  download: async (destination) => copyFile(archive, destination, 0),
});
process.stdout.write(`${JSON.stringify(result)}\n`);
