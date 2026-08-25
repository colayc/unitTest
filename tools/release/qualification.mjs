import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const platforms = Object.freeze(["linux", "windows"]);
const digestPattern = /^[0-9a-f]{64}$/u;
const commitPattern = /^[0-9a-f]{40}$/u;
const semverPattern = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/u;
const artifactIdPattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/u;
const artifactKindPattern = /^[a-z][a-z0-9-]*$/u;
const expectedOutcomes = Object.freeze({
  install: "pass",
  launchHandshake: "pass",
  upgrade: "pass",
  upgradeLaunch: "failed-as-expected",
  rollback: "pass",
  rollbackLaunch: "pass",
  repeatedRollback: "pass",
  uninstall: "pass",
  userDataPreserved: "pass",
  packageResidueAbsent: "pass",
});
const evidenceKeys = Object.freeze([
  "schemaVersion",
  "product",
  "platform",
  "sourceCommit",
  "packageFilename",
  "version",
  "packageSha256",
  "manifestSha256",
  "rollbackVersion",
  "outcomes",
]);
const manifestKeys = Object.freeze([
  "schemaVersion",
  "product",
  "version",
  "platform",
  "architecture",
  "sourceCommit",
  "artifacts",
  "licenses",
  "generatedAt",
]);
const auditKeys = Object.freeze([
  "schemaVersion",
  "product",
  "version",
  "platform",
  "sourceCommit",
  "licenses",
  "passed",
]);

function plainRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value) && Object.getPrototypeOf(value) === Object.prototype;
}

function hasExactKeys(value, expected) {
  return plainRecord(value)
    && Object.keys(value).sort().join("\0") === [...expected].sort().join("\0");
}

function portableRelativePath(value) {
  return typeof value === "string"
    && /^[A-Za-z0-9][A-Za-z0-9._+/-]*$/u.test(value)
    && !value.includes("\\")
    && !value.startsWith("/")
    && !/^[A-Za-z]:/u.test(value)
    && value.split("/").every((segment) => segment.length > 0 && segment !== "." && segment !== "..");
}

function validLicense(value) {
  return hasExactKeys(value, ["path", "size", "sha256"])
    && portableRelativePath(value.path)
    && Number.isSafeInteger(value.size)
    && value.size >= 0
    && digestPattern.test(value.sha256);
}

function closedLicenseList(value) {
  return Array.isArray(value)
    && value.length > 0
    && value.every(validLicense)
    && new Set(value.map(({ path }) => path)).size === value.length;
}

function validArtifact(value) {
  return hasExactKeys(value, ["id", "kind", "relativePath", "size", "sha256", "executable"])
    && artifactIdPattern.test(value.id ?? "")
    && artifactKindPattern.test(value.kind ?? "")
    && portableRelativePath(value.relativePath)
    && Number.isSafeInteger(value.size)
    && value.size >= 0
    && digestPattern.test(value.sha256 ?? "")
    && typeof value.executable === "boolean";
}

function closedArtifactList(value) {
  return Array.isArray(value)
    && value.length > 0
    && value.every(validArtifact)
    && new Set(value.map(({ id }) => id)).size === value.length
    && new Set(value.map(({ relativePath }) => relativePath)).size === value.length;
}

function sameLicenses(left, right) {
  if (!closedLicenseList(left) || !closedLicenseList(right) || left.length !== right.length) return false;
  const canonical = (records) => [...records]
    .sort((a, b) => a.path.localeCompare(b.path, "en"))
    .map(({ path, sha256, size }) => `${path}\0${size}\0${sha256}`);
  return canonical(left).every((value, index) => value === canonical(right)[index]);
}

function outputOutcomes(evidence) {
  return Object.fromEntries(Object.keys(expectedOutcomes).map((name) => [
    name,
    typeof evidence?.outcomes?.[name] === "string" ? evidence.outcomes[name] : null,
  ]));
}

function outputDigests(record) {
  return {
    packageSha256: digestPattern.test(record?.packageSha256 ?? "") ? record.packageSha256 : null,
    manifestSha256: digestPattern.test(record?.manifestSha256 ?? "") ? record.manifestSha256 : null,
  };
}

function addReason(reasons, message) {
  if (!reasons.includes(message)) reasons.push(message);
}

function validatePlatform(platform, input, reasons) {
  const evidence = input[`${platform}Evidence`];
  const manifestRecord = input.manifests?.[platform];
  const manifest = manifestRecord?.releaseManifest;
  const audit = input.licenseAudit?.[platform];

  if (evidence === undefined || evidence === null) {
    addReason(reasons, `${platform} evidence is missing`);
  } else if (!hasExactKeys(evidence, evidenceKeys) || !hasExactKeys(evidence.outcomes, Object.keys(expectedOutcomes))) {
    addReason(reasons, `${platform} evidence record is not closed`);
  }

  if (evidence !== undefined && evidence !== null) {
    if (evidence.schemaVersion !== 1 || evidence.product !== "unit-test-ide" || evidence.platform !== platform) {
      addReason(reasons, `${platform} evidence identity is invalid`);
    }
    if (!commitPattern.test(evidence.sourceCommit ?? "")) addReason(reasons, `${platform} evidence source commit is invalid`);
    if (!digestPattern.test(evidence.packageSha256 ?? "")) addReason(reasons, `${platform} evidence package digest is invalid`);
    if (!digestPattern.test(evidence.manifestSha256 ?? "")) addReason(reasons, `${platform} evidence manifest digest is invalid`);
    for (const [name, expected] of Object.entries(expectedOutcomes)) {
      if (evidence.outcomes?.[name] !== expected) {
        addReason(reasons, `${platform} ${name} outcome must be ${expected}`);
      }
    }
  }

  if (manifestRecord === undefined || manifestRecord === null) {
    addReason(reasons, `${platform} manifest evidence is missing`);
  } else if (!hasExactKeys(manifestRecord, ["releaseManifest", "packageSha256", "manifestSha256"])) {
    addReason(reasons, `${platform} manifest evidence record is not closed`);
  }
  if (manifest === undefined || manifest === null) {
    addReason(reasons, `${platform} release manifest is missing`);
  } else {
    if (!hasExactKeys(manifest, manifestKeys)) addReason(reasons, `${platform} release manifest is not closed`);
    if (
      manifest.schemaVersion !== 1
      || manifest.product !== "unit-test-ide"
      || manifest.platform !== platform
      || !commitPattern.test(manifest.sourceCommit ?? "")
      || !semverPattern.test(manifest.version ?? "")
      || !closedLicenseList(manifest.licenses)
    ) {
      addReason(reasons, `${platform} release manifest identity is invalid`);
    }
    if (!closedArtifactList(manifest.artifacts)) {
      addReason(reasons, `${platform} release manifest artifacts are invalid`);
    }
    if (!Array.isArray(manifest.licenses) || manifest.licenses.length === 0) {
      addReason(reasons, `${platform} release manifest must contain at least one license notice`);
    }
  }
  if (!digestPattern.test(manifestRecord?.packageSha256 ?? "")) {
    addReason(reasons, `${platform} package digest evidence is invalid`);
  } else if (evidence && manifestRecord.packageSha256 !== evidence.packageSha256) {
    addReason(reasons, `${platform} packageSha256 does not match install evidence`);
  }
  if (!digestPattern.test(manifestRecord?.manifestSha256 ?? "")) {
    addReason(reasons, `${platform} manifest digest evidence is invalid`);
  } else if (evidence && manifestRecord.manifestSha256 !== evidence.manifestSha256) {
    addReason(reasons, `${platform} manifestSha256 does not match install evidence`);
  }
  if (manifest && evidence && manifest.version !== evidence.version) {
    addReason(reasons, `${platform} evidence version does not match the release manifest`);
  }

  if (audit === undefined || audit === null) {
    addReason(reasons, `${platform} license audit is missing`);
  } else {
    if (!hasExactKeys(audit, auditKeys)) addReason(reasons, `${platform} license audit is not closed`);
    if (
      audit.schemaVersion !== 1
      || audit.product !== "unit-test-ide"
      || audit.platform !== platform
      || audit.passed !== true
      || !commitPattern.test(audit.sourceCommit ?? "")
    ) {
      addReason(reasons, `${platform} license audit did not pass`);
    }
    if (manifest && (audit.version !== manifest.version || !sameLicenses(audit.licenses, manifest.licenses))) {
      addReason(reasons, `${platform} license audit does not match the release manifest`);
    }
  }
}

function validateReleaseVersion(input, reasons) {
  const canonical = input.manifests?.linux?.releaseManifest?.version
    ?? input.manifests?.windows?.releaseManifest?.version
    ?? input.linuxEvidence?.version
    ?? input.windowsEvidence?.version
    ?? null;
  if (!semverPattern.test(canonical ?? "")) {
    addReason(reasons, "canonical release version is missing or invalid");
    return null;
  }
  for (const platform of platforms) {
    const records = [
      ["release version", input.manifests?.[platform]?.releaseManifest?.version],
      ["evidence version", input[`${platform}Evidence`]?.version],
      ["license audit version", input.licenseAudit?.[platform]?.version],
    ];
    for (const [label, version] of records) {
      if (version !== undefined && version !== null && version !== canonical) {
        addReason(reasons, `${platform} ${label} does not match canonical version ${canonical}`);
      }
    }
  }
  return canonical;
}

function validateSourceCommit(input, reasons) {
  const canonical = input.manifests?.linux?.releaseManifest?.sourceCommit
    ?? input.manifests?.windows?.releaseManifest?.sourceCommit
    ?? input.linuxEvidence?.sourceCommit
    ?? input.windowsEvidence?.sourceCommit
    ?? null;
  for (const platform of platforms) {
    const sources = [
      ["evidence", input[`${platform}Evidence`]?.sourceCommit],
      ["release manifest", input.manifests?.[platform]?.releaseManifest?.sourceCommit],
      ["license audit", input.licenseAudit?.[platform]?.sourceCommit],
    ];
    for (const [label, commit] of sources) {
      if (commit !== undefined && commit !== null && canonical !== null && commit !== canonical) {
        addReason(reasons, `${platform} ${label} source commit does not match the release`);
      }
    }
  }
  return commitPattern.test(canonical ?? "") ? canonical : null;
}

function validateSignatures(signatures, reasons) {
  if (!hasExactKeys(signatures, ["windows"]) || !hasExactKeys(signatures?.windows, ["required", "outcome"])) {
    addReason(reasons, "Windows signature evidence is missing or not closed");
    return "missing";
  }
  const { outcome, required } = signatures.windows;
  if (typeof required !== "boolean" || !["verified", "not-required"].includes(outcome)) {
    addReason(reasons, "Windows signature evidence is invalid");
  }
  if (required === true && outcome !== "verified") {
    addReason(reasons, "required Windows package signature is not verified");
  }
  if (required === false && !["verified", "not-required"].includes(outcome)) {
    addReason(reasons, "optional Windows package signature outcome is invalid");
  }
  return typeof outcome === "string" ? outcome : "missing";
}

function licensePassed(platform, input) {
  const audit = input.licenseAudit?.[platform];
  const manifest = input.manifests?.[platform]?.releaseManifest;
  return hasExactKeys(audit, auditKeys)
    && audit.schemaVersion === 1
    && audit.product === "unit-test-ide"
    && audit.platform === platform
    && audit.passed === true
    && commitPattern.test(audit.sourceCommit ?? "")
    && audit.sourceCommit === manifest?.sourceCommit
    && audit.version === manifest?.version
    && sameLicenses(audit.licenses, manifest?.licenses);
}

export function qualifyRelease(input) {
  const normalized = plainRecord(input) ? input : {};
  const reasons = [];
  if (!plainRecord(input)) addReason(reasons, "release qualification input is missing");
  else if (!hasExactKeys(input, ["linuxEvidence", "windowsEvidence", "manifests", "licenseAudit", "signatures"])) {
    addReason(reasons, "release qualification input is not closed");
  }
  if (!hasExactKeys(normalized.manifests, platforms)) addReason(reasons, "platform manifest evidence is missing or not closed");
  if (!hasExactKeys(normalized.licenseAudit, platforms)) addReason(reasons, "platform license audit evidence is missing or not closed");
  for (const platform of platforms) validatePlatform(platform, normalized, reasons);
  validateReleaseVersion(normalized, reasons);
  const sourceCommit = validateSourceCommit(normalized, reasons);
  const windowsSignature = validateSignatures(normalized.signatures, reasons);
  const qualified = reasons.length === 0;
  const report = {
    schemaVersion: 1,
    sourceCommit,
    packageDigests: {
      linux: outputDigests(normalized.manifests?.linux),
      windows: outputDigests(normalized.manifests?.windows),
    },
    signatureOutcomes: { windows: windowsSignature },
    lifecycleOutcomes: {
      linux: outputOutcomes(normalized.linuxEvidence),
      windows: outputOutcomes(normalized.windowsEvidence),
    },
    licenseOutcome: {
      linux: licensePassed("linux", normalized) ? "pass" : "fail",
      windows: licensePassed("windows", normalized) ? "pass" : "fail",
    },
    qualificationOutcome: { qualified, reasons },
  };
  return { qualified, report };
}

async function sha256File(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest("hex");
}

async function readJson(path, label) {
  try {
    return JSON.parse(await readFile(path, "utf8"));
  } catch (error) {
    const wrapped = new Error(`${label} is missing or invalid JSON`);
    wrapped.cause = error;
    throw wrapped;
  }
}

function parseCli(argv) {
  const allowed = new Set([
    "--linux-evidence",
    "--windows-evidence",
    "--linux-manifest",
    "--windows-manifest",
    "--linux-package",
    "--windows-package",
    "--linux-license-audit",
    "--windows-license-audit",
    "--windows-signature-required",
    "--windows-signature-outcome",
    "--out",
  ]);
  const values = {};
  for (let index = 0; index < argv.length; index += 2) {
    const flag = argv[index];
    const value = argv[index + 1];
    if (!allowed.has(flag)) throw new Error(`unknown qualification flag: ${flag}`);
    if (typeof value !== "string" || value.length === 0 || value.startsWith("--")) {
      throw new Error(`missing value for qualification flag: ${flag}`);
    }
    if (Object.hasOwn(values, flag)) throw new Error(`duplicate qualification flag: ${flag}`);
    values[flag] = value;
  }
  for (const flag of allowed) {
    if (!Object.hasOwn(values, flag)) throw new Error(`required qualification flag is missing: ${flag}`);
  }
  if (!["0", "1"].includes(values["--windows-signature-required"])) {
    throw new Error("--windows-signature-required must be 0 or 1");
  }
  return values;
}

async function main(argv) {
  const values = parseCli(argv);
  const manifests = {};
  const licenseAudit = {};
  const evidence = {};
  for (const platform of platforms) {
    const manifestPath = resolve(values[`--${platform}-manifest`]);
    const packagePath = resolve(values[`--${platform}-package`]);
    manifests[platform] = {
      releaseManifest: await readJson(manifestPath, `${platform} release manifest`),
      packageSha256: await sha256File(packagePath),
      manifestSha256: await sha256File(manifestPath),
    };
    licenseAudit[platform] = await readJson(
      resolve(values[`--${platform}-license-audit`]),
      `${platform} license audit`,
    );
    evidence[platform] = await readJson(
      resolve(values[`--${platform}-evidence`]),
      `${platform} install evidence`,
    );
  }
  const result = qualifyRelease({
    linuxEvidence: evidence.linux,
    windowsEvidence: evidence.windows,
    manifests,
    licenseAudit,
    signatures: {
      windows: {
        required: values["--windows-signature-required"] === "1",
        outcome: values["--windows-signature-outcome"],
      },
    },
  });
  const output = resolve(values["--out"]);
  await mkdir(dirname(output), { recursive: true });
  await writeFile(output, `${JSON.stringify(result.report, null, 2)}\n`);
  process.stdout.write(`${output}\n`);
  if (!result.qualified) process.exitCode = 1;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  });
}
