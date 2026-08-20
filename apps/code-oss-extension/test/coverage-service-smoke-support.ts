import { randomBytes } from "node:crypto";
import {
  lstat,
  mkdir,
  open,
  readFile,
  rename,
  rm
} from "node:fs/promises";
import { dirname, isAbsolute, join } from "node:path";

const XML_DECLARATION = '<?xml version="1.0" encoding="UTF-8"?>';
const MAX_JUNIT_BYTES = 64 * 1024 * 1024;
const TEST_ID = /^utid-v1-[0-9a-f]{64}$/u;
const ITERATED_TEST_ID = /^utid-v1-[0-9a-f]{64}#([2-9][0-9]*)$/u;

export interface ParsedJUnit {
  readonly tests: number;
  readonly failures: number;
  readonly errors: number;
  readonly skipped: number;
}

export interface EvidencePublishOptions {
  readonly readBack?: (path: string) => Promise<Uint8Array>;
}

interface XMLStartToken {
  readonly kind: "start";
  readonly name: string;
  readonly attributes: ReadonlyMap<string, string>;
}

interface XMLEndToken {
  readonly kind: "end";
  readonly name: string;
}

interface XMLTextToken {
  readonly kind: "text";
  readonly raw: string;
  readonly value: string;
}

type XMLToken = XMLStartToken | XMLEndToken | XMLTextToken;

interface OpenElement {
  readonly name: string;
  outcome?: boolean;
}

export function parseStrictJUnit(bytes: Uint8Array): ParsedJUnit {
  if (bytes.byteLength === 0 || bytes.byteLength > MAX_JUNIT_BYTES) {
    throw junitError("size is outside the bounded contract");
  }
  let xml: string;
  try {
    xml = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch (error) {
    throw junitError("is not valid UTF-8", error);
  }
  if (!xml.startsWith(XML_DECLARATION)) {
    throw junitError("must begin with the exact XML declaration");
  }

  const tokenizer = new StrictXMLTokenizer(xml, XML_DECLARATION.length);
  const stack: OpenElement[] = [];
  let declared: ParsedJUnit | undefined;
  const actual = { tests: 0, failures: 0, errors: 0, skipped: 0 };
  let rootClosed = false;
  for (;;) {
    const token = tokenizer.next();
    if (token === undefined) break;
    if (token.kind === "text") {
      if (stack.length !== 3 && !/^[\t\n\r ]*$/u.test(token.raw)) {
        throw junitError("contains text outside an outcome detail");
      }
      continue;
    }
    if (token.kind === "end") {
      const current = stack.pop();
      if (current === undefined || current.name !== token.name) {
        throw junitError("contains mismatched element nesting");
      }
      if (stack.length === 0) rootClosed = true;
      continue;
    }
    if (rootClosed) throw junitError("contains an extra root element");
    if (stack.length === 0) {
      if (token.name !== "testsuite" || declared !== undefined) {
        throw junitError("root must be one testsuite");
      }
      declared = parseSuiteAttributes(token.attributes);
      stack.push({ name: token.name });
      continue;
    }
    if (stack.length === 1) {
      if (token.name !== "testcase") {
        throw junitError("testsuite contains an unknown child");
      }
      validateTestcaseAttributes(token.attributes);
      actual.tests++;
      stack.push({ name: token.name, outcome: false });
      continue;
    }
    if (stack.length === 2) {
      const testcase = stack[1]!;
      if (testcase.outcome) throw junitError("testcase contains multiple outcomes");
      validateOutcomeAttributes(token.name, token.attributes);
      testcase.outcome = true;
      if (token.name === "failure") actual.failures++;
      if (token.name === "error") actual.errors++;
      if (token.name === "skipped") actual.skipped++;
      stack.push({ name: token.name });
      continue;
    }
    throw junitError("outcome detail contains nested elements");
  }
  if (stack.length !== 0 || declared === undefined || !rootClosed) {
    throw junitError("document is incomplete");
  }
  if (
    declared.tests !== actual.tests ||
    declared.failures !== actual.failures ||
    declared.errors !== actual.errors ||
    declared.skipped !== actual.skipped
  ) {
    throw junitError("declared counts do not match structure");
  }
  return actual;
}

export async function publishEvidenceAtomically(
  target: string,
  bytes: Uint8Array,
  options: EvidencePublishOptions = {}
): Promise<void> {
  if (!isAbsolute(target) || target.includes("\0")) {
    throw new Error("coverage evidence target must be an absolute path");
  }
  const expected = Buffer.from(bytes);
  validateCanonicalJSON(expected);
  const directory = dirname(target);
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const directoryInfo = await lstat(directory);
  if (!directoryInfo.isDirectory() || directoryInfo.isSymbolicLink()) {
    throw new Error("coverage evidence directory is unsafe");
  }
  try {
    await lstat(target);
    throw new Error("coverage evidence target already exists");
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
  }

  const temporary = join(
    directory,
    `.coverage-execution-report-${process.pid}-${randomBytes(8).toString("hex")}.tmp`
  );
  let handle: Awaited<ReturnType<typeof open>> | undefined;
  try {
    handle = await open(temporary, "wx", 0o600);
    await handle.writeFile(expected);
    await handle.sync();
    await handle.close();
    handle = undefined;
    const readBack = Buffer.from(await (options.readBack ?? readFile)(temporary));
    if (!readBack.equals(expected)) {
      throw new Error("temporary evidence readback does not match the intended bytes");
    }
    validateCanonicalJSON(readBack);
    await rename(temporary, target);
  } catch (error) {
    await handle?.close().catch(() => undefined);
    await rm(temporary, { force: true }).catch(() => undefined);
    await rm(target, { force: true }).catch(() => undefined);
    throw error;
  }
}

export async function teardownThenPublish(
  teardown: readonly (() => Promise<void>)[],
  publish: () => Promise<void>
): Promise<void> {
  const errors: unknown[] = [];
  for (const step of teardown) {
    try {
      await step();
    } catch (error) {
      errors.push(error);
    }
  }
  if (errors.length > 0) {
    throw new AggregateError(errors, "coverage smoke teardown failed; evidence was not published");
  }
  await publish();
}

class StrictXMLTokenizer {
  readonly #pending: XMLToken[] = [];
  #index: number;

  constructor(
    private readonly xml: string,
    index: number
  ) {
    this.#index = index;
  }

  next(): XMLToken | undefined {
    const pending = this.#pending.shift();
    if (pending !== undefined) return pending;
    if (this.#index >= this.xml.length) return undefined;
    if (this.xml[this.#index] !== "<") return this.#text();
    if (this.xml.startsWith("</", this.#index)) return this.#end();
    if (this.xml.startsWith("<!", this.#index)) {
      throw junitError("DOCTYPE, ENTITY, comment and CDATA declarations are forbidden");
    }
    if (this.xml.startsWith("<?", this.#index)) {
      throw junitError("processing instructions are forbidden after the XML declaration");
    }
    return this.#start();
  }

  #text(): XMLTextToken {
    const end = this.xml.indexOf("<", this.#index);
    const next = end < 0 ? this.xml.length : end;
    const raw = this.xml.slice(this.#index, next);
    this.#index = next;
    if (raw.includes("]]>", 0)) throw junitError("text contains a forbidden CDATA terminator");
    return { kind: "text", raw, value: decodeXMLValue(raw) };
  }

  #end(): XMLEndToken {
    this.#index += 2;
    const name = this.#name();
    this.#whitespace();
    this.#expect(">");
    return { kind: "end", name };
  }

  #start(): XMLStartToken {
    this.#index++;
    const name = this.#name();
    const attributes = new Map<string, string>();
    for (;;) {
      const whitespace = this.#whitespace();
      if (this.xml.startsWith("/>", this.#index)) {
        this.#index += 2;
        this.#pending.push({ kind: "end", name });
        return { kind: "start", name, attributes };
      }
      if (this.xml[this.#index] === ">") {
        this.#index++;
        return { kind: "start", name, attributes };
      }
      if (!whitespace) throw junitError("attributes must be separated by XML whitespace");
      const attributeName = this.#name();
      if (attributes.has(attributeName)) throw junitError("contains a duplicate attribute");
      this.#whitespace();
      this.#expect("=");
      this.#whitespace();
      const quote = this.xml[this.#index];
      if (quote !== '"' && quote !== "'") {
        throw junitError("attribute values must be quoted");
      }
      this.#index++;
      const end = this.xml.indexOf(quote, this.#index);
      if (end < 0) throw junitError("contains an unterminated attribute");
      const raw = this.xml.slice(this.#index, end);
      if (raw.includes("<")) throw junitError("attribute contains a raw less-than character");
      attributes.set(attributeName, decodeXMLValue(raw));
      this.#index = end + 1;
    }
  }

  #name(): string {
    const start = this.#index;
    const first = this.xml[this.#index];
    if (first === undefined || !/[A-Za-z_]/u.test(first)) {
      throw junitError("contains an invalid XML name");
    }
    this.#index++;
    while (this.#index < this.xml.length && /[A-Za-z0-9_.-]/u.test(this.xml[this.#index]!)) {
      this.#index++;
    }
    return this.xml.slice(start, this.#index);
  }

  #whitespace(): boolean {
    const start = this.#index;
    while (this.#index < this.xml.length && /[\t\n\r ]/u.test(this.xml[this.#index]!)) {
      this.#index++;
    }
    return this.#index !== start;
  }

  #expect(value: string): void {
    if (!this.xml.startsWith(value, this.#index)) {
      throw junitError(`expected ${value}`);
    }
    this.#index += value.length;
  }
}

function decodeXMLValue(raw: string): string {
  validateXMLCharacters(raw);
  let output = "";
  let index = 0;
  while (index < raw.length) {
    const ampersand = raw.indexOf("&", index);
    if (ampersand < 0) {
      output += raw.slice(index);
      break;
    }
    output += raw.slice(index, ampersand);
    const semicolon = raw.indexOf(";", ampersand + 1);
    if (semicolon < 0) throw junitError("contains an unterminated entity");
    const entity = raw.slice(ampersand + 1, semicolon);
    const builtin: Readonly<Record<string, string>> = {
      amp: "&",
      apos: "'",
      gt: ">",
      lt: "<",
      quot: '"'
    };
    if (builtin[entity] !== undefined) {
      output += builtin[entity];
    } else {
      let digits: string;
      let radix: 10 | 16;
      if (/^#[0-9]+$/u.test(entity)) {
        digits = entity.slice(1);
        radix = 10;
      } else if (/^#x[0-9A-Fa-f]+$/u.test(entity)) {
        digits = entity.slice(2);
        radix = 16;
      } else {
        throw junitError("contains an unknown entity");
      }
      const codePoint = Number.parseInt(digits, radix);
      if (!isXMLCharacter(codePoint)) throw junitError("contains an invalid numeric entity");
      output += String.fromCodePoint(codePoint);
    }
    index = semicolon + 1;
  }
  validateXMLCharacters(output);
  return output;
}

function validateXMLCharacters(value: string): void {
  for (const character of value) {
    const codePoint = character.codePointAt(0)!;
    if (!isXMLCharacter(codePoint)) throw junitError("contains an invalid XML character");
  }
}

function isXMLCharacter(codePoint: number): boolean {
  return codePoint === 0x09 || codePoint === 0x0a || codePoint === 0x0d ||
    codePoint >= 0x20 && codePoint <= 0xd7ff ||
    codePoint >= 0xe000 && codePoint <= 0xfffd ||
    codePoint >= 0x10000 && codePoint <= 0x10ffff;
}

function parseSuiteAttributes(attributes: ReadonlyMap<string, string>): ParsedJUnit {
  const values = exactAttributes(attributes, ["name", "tests", "failures", "errors", "skipped"]);
  if (values.name !== "coverage-test-run") throw junitError("testsuite name is invalid");
  return {
    tests: parseCount(values.tests),
    failures: parseCount(values.failures),
    errors: parseCount(values.errors),
    skipped: parseCount(values.skipped)
  };
}

function validateTestcaseAttributes(attributes: ReadonlyMap<string, string>): void {
  const values = exactAttributes(attributes, ["name", "classname"]);
  if (!TEST_ID.test(values.classname)) throw junitError("testcase classname is invalid");
  if (!TEST_ID.test(values.name) && !ITERATED_TEST_ID.test(values.name)) {
    throw junitError("testcase name is invalid");
  }
}

function validateOutcomeAttributes(
  name: string,
  attributes: ReadonlyMap<string, string>
): void {
  if (name === "skipped") {
    exactAttributes(attributes, ["message"]);
    return;
  }
  if (name !== "failure" && name !== "error") {
    throw junitError("testcase contains an unknown outcome");
  }
  const values = exactAttributes(attributes, ["type", "message"]);
  if (values.type.length === 0) throw junitError("outcome type is empty");
}

function exactAttributes<const Name extends string>(
  attributes: ReadonlyMap<string, string>,
  names: readonly Name[]
): Record<Name, string> {
  if (attributes.size !== names.length) throw junitError("element has the wrong attribute count");
  const values = {} as Record<Name, string>;
  for (const name of names) {
    const value = attributes.get(name);
    if (value === undefined) throw junitError(`element is missing attribute ${name}`);
    values[name] = value;
  }
  return values;
}

function parseCount(value: string): number {
  if (!/^(?:0|[1-9][0-9]*)$/u.test(value)) throw junitError("suite count is invalid");
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed)) throw junitError("suite count exceeds the safe range");
  return parsed;
}

function validateCanonicalJSON(bytes: Buffer): void {
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch (error) {
    throw new Error("coverage evidence is not valid UTF-8", { cause: error });
  }
  if (!text.endsWith("\n") || text.slice(0, -1).includes("\n") || text.includes("\r")) {
    throw new Error("coverage evidence must be one newline-terminated JSON object");
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(text.slice(0, -1));
  } catch (error) {
    throw new Error("coverage evidence is not strict JSON", { cause: error });
  }
  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("coverage evidence root must be an object");
  }
  if (`${JSON.stringify(parsed)}\n` !== text) {
    throw new Error("coverage evidence must use canonical compact JSON encoding");
  }
}

function junitError(message: string, cause?: unknown): Error {
  return new Error(`JUnit XML ${message}`, cause === undefined ? undefined : { cause });
}
