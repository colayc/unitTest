const componentPattern = /^[A-Za-z0-9._+@() -]+$/u;
const deviceBasenamePattern = /^(?:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$/u;

export function isPortableReleasePathComponent(value) {
  if (
    typeof value !== "string"
    || value.length === 0
    || !componentPattern.test(value)
    || value === "."
    || value === ".."
    || value.startsWith(" ")
    || value.endsWith(" ")
    || value.endsWith(".")
  ) {
    return false;
  }
  return !deviceBasenamePattern.test(value.split(".", 1)[0].toUpperCase());
}

export function isPortableReleasePath(value) {
  return typeof value === "string"
    && value.length > 0
    && value.split("/").every(isPortableReleasePathComponent);
}
