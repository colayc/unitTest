import { checkBundle } from "./prepare.mjs";

const args = process.argv.slice(2);
const rootIndex = args.indexOf("--root");
const root = rootIndex >= 0 ? args[rootIndex + 1] : undefined;
checkBundle({ root })
  .then((result) => process.stdout.write(`${result.root}\n`))
  .catch((error) => { process.stderr.write(`${error.message}\n`); process.exitCode = 1; });
