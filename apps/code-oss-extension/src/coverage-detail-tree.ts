import type { CoverageFileV16, CoverageFunctionV16, CoverageMetricV16, CoverageProjectV16 } from "@unit-test-ide/test-client";
import type { ExtensionProtocolClient } from "./protocol-client.js";

export type CoverageFilter = "all" | "uncovered" | "regressed" | "incomplete";
export interface MetricLabel { readonly covered: number; readonly total: number; readonly percent: number; readonly coveredDelta: number }
export interface CoverageTreeNode {
  readonly kind: "project" | "file" | "function" | "load-more";
  readonly id: string;
  readonly label: string;
  readonly status: "current" | "stale" | "incomplete";
  readonly metrics: { readonly functions: MetricLabel; readonly lines: MetricLabel; readonly branches: MetricLabel };
  readonly relativePath?: string;
  readonly sourceSha256?: string;
  readonly startLine?: number;
  readonly nextCursor?: string;
}

export interface CoverageTreeContext {
  readonly client: ExtensionProtocolClient | undefined;
  readonly workspaceGeneration: string;
  readonly projectId: string;
  readonly reportId: string;
}

const PAGE_LIMIT = 100;
const MAX_LABEL_LENGTH = 256;

function metric(value: CoverageMetricV16): MetricLabel {
  return { covered: value.covered, total: value.total, percent: value.total === 0 ? 100 : Math.round(100 * value.covered / value.total), coveredDelta: value.coveredDelta };
}

function metrics(value: CoverageProjectV16["summary"]): CoverageTreeNode["metrics"] {
  return { functions: metric(value.functions), lines: metric(value.lines), branches: metric(value.branches) };
}

function label(name: string, value: CoverageProjectV16["summary"], status: CoverageTreeNode["status"]): string {
  const parts = (["functions", "lines", "branches"] as const).map((key) => {
    const item = metric(value[key]);
    return `${key} ${item.covered}/${item.total} (${item.percent}%, ${item.coveredDelta >= 0 ? "+" : ""}${item.coveredDelta})`;
  });
  const suffix = ` [${status}] · ${parts.join(" · ")}`;
  return `${name.slice(0, Math.max(0, MAX_LABEL_LENGTH - suffix.length))}${suffix}`.slice(0, MAX_LABEL_LENGTH);
}

function matches(node: CoverageTreeNode, filter: CoverageFilter): boolean {
  if (filter === "all" || node.kind === "load-more") return true;
  if (filter === "incomplete") return node.status !== "current";
  const items = Object.values(node.metrics);
  return filter === "uncovered" ? items.some((item) => item.covered < item.total) : items.some((item) => item.coveredDelta < 0);
}

type PageState = { items: CoverageTreeNode[]; nextCursor?: string; seen: Set<string>; loaded: boolean };

export class CoverageDetailTree {
  #version = 0;
  #binding: CoverageTreeContext | undefined;
  #project: CoverageTreeNode | undefined;
  #files: PageState = { items: [], seen: new Set(), loaded: false };
  #functions = new Map<string, PageState>();
  #pendingFiles: Promise<void> | undefined;
  #pendingFunctions = new Map<string, Promise<void>>();
  #filter: CoverageFilter = "all";
  #pageLimit = PAGE_LIMIT;
  #linePageLimit = 200;

  constructor(private readonly readContext: () => CoverageTreeContext) {}

  get available(): boolean { return this.#project !== undefined; }
  get binding(): CoverageTreeContext | undefined { return this.#binding; }
  get linePageLimit(): number { return this.#linePageLimit; }
  owns(node: CoverageTreeNode): boolean {
    if (!this.#binding || !this.#current(this.#version, this.#binding)) return false;
    if (node.kind === "file") return this.#files.items.includes(node);
    if (node.kind === "function") return [...this.#functions.values()].some((page) => page.items.includes(node));
    return node === this.#project;
  }
  fileIdFor(node: CoverageTreeNode): string | undefined {
    if (!this.owns(node)) return undefined;
    if (node.kind === "file") return node.id;
    for (const [fileId, page] of this.#functions) if (page.items.includes(node)) return fileId;
    return undefined;
  }

  setFilter(filter: CoverageFilter): void { this.#filter = filter; }

  invalidate(): void {
    this.#version++;
    this.#binding = undefined;
    this.#project = undefined;
    this.#files = { items: [], seen: new Set(), loaded: false };
    this.#functions.clear();
    this.#pendingFiles = undefined;
    this.#pendingFunctions.clear();
  }

  #current(version: number, binding: CoverageTreeContext): boolean {
    const current = this.readContext();
    return version === this.#version && current.client === binding.client &&
      current.workspaceGeneration === binding.workspaceGeneration && current.projectId === binding.projectId && current.reportId === binding.reportId;
  }

  async refresh(): Promise<boolean> {
    this.invalidate();
    const version = this.#version;
    const binding = this.readContext();
    const client = binding.client;
    if (!client?.getCapabilities || !client.getCoverageProject || !client.listCoverageFiles || !client.listCoverageFunctions ||
      !client.listCoverageLines || !binding.workspaceGeneration || !binding.projectId || !binding.reportId) return false;
    try {
      const capabilities = await client.getCapabilities();
      if (!this.#current(version, binding) || !("coverageDetails" in capabilities) || capabilities.coverageDetails !== true) return false;
      if (!("maxCoverageDetailPageSize" in capabilities) || !Number.isSafeInteger(capabilities.maxCoverageDetailPageSize) || capabilities.maxCoverageDetailPageSize < 1 ||
        !("maxCoverageLinePageSize" in capabilities) || !Number.isSafeInteger(capabilities.maxCoverageLinePageSize) || capabilities.maxCoverageLinePageSize < 1) return false;
      this.#pageLimit = Math.min(PAGE_LIMIT, capabilities.maxCoverageDetailPageSize);
      this.#linePageLimit = Math.min(200, capabilities.maxCoverageLinePageSize);
      const project = await client.getCoverageProject({ workspaceGeneration: binding.workspaceGeneration, projectId: binding.projectId, coverageReportId: binding.reportId });
      if (!this.#current(version, binding)) return false;
      if (project.workspaceGeneration !== binding.workspaceGeneration || project.projectId !== binding.projectId || project.coverageReportId !== binding.reportId) throw new Error("Coverage detail project identity changed.");
      this.#binding = binding;
      this.#project = { kind: "project", id: project.projectId, label: label(project.projectId, project.summary, project.status), status: project.status, metrics: metrics(project.summary) };
      return true;
    } catch {
      if (this.#current(version, binding)) this.invalidate();
      return false;
    }
  }

  async children(parent?: CoverageTreeNode): Promise<CoverageTreeNode[]> {
    const binding = this.#binding;
    if (!binding || !this.#project || !this.#current(this.#version, binding)) { this.invalidate(); return []; }
    if (!parent) return [this.#project];
    if (parent.kind === "load-more") {
      if (!parent.nextCursor) return [];
      if (parent.id === this.#project.id) await this.#loadFiles(parent.nextCursor);
      else await this.#loadFunctions(parent.id, parent.nextCursor);
      return this.children(parent.id === this.#project.id ? this.#project : this.#files.items.find((item) => item.id === parent.id));
    }
    if (parent.kind === "project") {
      if (parent !== this.#project) return [];
      if (!this.#files.loaded) await this.#loadFiles();
      if (!this.#binding || !this.#current(this.#version, binding)) return [];
      return this.#pageChildren(this.#files, parent.id, parent.status, parent.metrics);
    }
    if (parent.kind === "file") {
      if (!this.#files.items.includes(parent)) return [];
      if (!this.#functions.has(parent.id)) await this.#loadFunctions(parent.id);
      if (!this.#binding || !this.#current(this.#version, binding)) return [];
      const page = this.#functions.get(parent.id);
      return page ? this.#pageChildren(page, parent.id, parent.status, parent.metrics) : [];
    }
    return [];
  }

  #pageChildren(page: PageState, id: string, status: CoverageTreeNode["status"], values: CoverageTreeNode["metrics"]): CoverageTreeNode[] {
    const filtered = page.items.filter((item) => matches(item, this.#filter));
    return page.nextCursor ? [...filtered, { kind: "load-more", id, label: "Load more…", status, metrics: values, nextCursor: page.nextCursor }] : filtered;
  }

  async #loadFiles(cursor?: string): Promise<void> {
    if (this.#pendingFiles) return this.#pendingFiles;
    const pending = this.#fetchFiles(cursor);
    this.#pendingFiles = pending;
    try { await pending; } finally { if (this.#pendingFiles === pending) this.#pendingFiles = undefined; }
  }

  async #fetchFiles(cursor?: string): Promise<void> {
    const binding = this.#binding!;
    const version = this.#version;
    const result = await binding.client!.listCoverageFiles!({ workspaceGeneration: binding.workspaceGeneration, projectId: binding.projectId, coverageReportId: binding.reportId, cursor, limit: this.#pageLimit });
    if (!this.#current(version, binding)) return;
    if (result.workspaceGeneration !== binding.workspaceGeneration || result.coverageReportId !== binding.reportId || result.items.length > this.#pageLimit) throw new Error("Coverage file page identity or limit changed.");
    for (const file of result.items) this.#insertFile(file);
    this.#files.nextCursor = result.nextCursor;
    this.#files.loaded = true;
  }

  #insertFile(file: CoverageFileV16): void {
    if (this.#files.seen.has(file.fileId)) throw new Error("Duplicate coverage file.");
    this.#files.seen.add(file.fileId);
    this.#files.items.push({ kind: "file", id: file.fileId, label: label(file.relativePath, file.summary, file.status), status: file.status, metrics: metrics(file.summary), relativePath: file.relativePath, sourceSha256: file.sourceSha256 });
  }

  async #loadFunctions(fileId: string, cursor?: string): Promise<void> {
    const existing = this.#pendingFunctions.get(fileId);
    if (existing) return existing;
    const pending = this.#fetchFunctions(fileId, cursor);
    this.#pendingFunctions.set(fileId, pending);
    try { await pending; } finally { if (this.#pendingFunctions.get(fileId) === pending) this.#pendingFunctions.delete(fileId); }
  }

  async #fetchFunctions(fileId: string, cursor?: string): Promise<void> {
    const binding = this.#binding!;
    const version = this.#version;
    const result = await binding.client!.listCoverageFunctions!({ workspaceGeneration: binding.workspaceGeneration, coverageReportId: binding.reportId, fileId, cursor, limit: this.#pageLimit });
    if (!this.#current(version, binding)) return;
    if (result.workspaceGeneration !== binding.workspaceGeneration || result.coverageReportId !== binding.reportId || result.items.length > this.#pageLimit) throw new Error("Coverage function page identity or limit changed.");
    const page = this.#functions.get(fileId) ?? { items: [], seen: new Set<string>(), loaded: false };
    for (const item of result.items) this.#insertFunction(page, item, fileId);
    page.nextCursor = result.nextCursor;
    page.loaded = true;
    this.#functions.set(fileId, page);
  }

  #insertFunction(page: PageState, fn: CoverageFunctionV16, fileId: string): void {
    if (fn.fileId !== fileId || page.seen.has(fn.functionId)) throw new Error("Coverage function identity changed.");
    page.seen.add(fn.functionId);
    const file = this.#files.items.find((item) => item.id === fileId)!;
    page.items.push({ kind: "function", id: fn.functionId, label: label(fn.qualifiedName, fn.summary, fn.status), status: fn.status, metrics: metrics(fn.summary), relativePath: file.relativePath, sourceSha256: file.sourceSha256, startLine: fn.startLine });
  }
}
