import { build } from "esbuild";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const extensionRoot = dirname(fileURLToPath(import.meta.url));
const output = resolve(extensionRoot, "dist", "src", "extension.js");
const result = await build({
  absWorkingDir: extensionRoot,
  bundle: true,
  charset: "utf8",
  entryPoints: ["src/extension.ts"],
  external: ["vscode"],
  format: "esm",
  legalComments: "external",
  logLevel: "info",
  metafile: true,
  minify: false,
  outfile: output,
  platform: "node",
  sourcemap: false,
  target: "node20",
  tsconfig: "tsconfig.json",
});

const unexpected = Object.values(result.metafile.outputs)
  .flatMap(({ imports }) => imports)
  .filter(({ external, path }) => external && path !== "vscode" && !path.startsWith("node:"));
if (unexpected.length > 0) {
  throw new Error(`extension bundle has unexpected external imports: ${unexpected.map(({ path }) => path).join(", ")}`);
}
