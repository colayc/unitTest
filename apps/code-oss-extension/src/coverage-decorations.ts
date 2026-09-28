import type { CoverageLineDetailV16 } from "@unit-test-ide/test-client";
import type { ExtensionProtocolClient } from "./protocol-client.js";

export interface LineDecoration {
  readonly line: number;
  readonly style: "covered" | "uncovered" | "stale" | "incomplete";
  readonly label: string;
}

const MAX_DECORATIONS = 200;
const PAGE_LIMIT = 200;

export function lineDecorations(items: readonly CoverageLineDetailV16[], status: "current" | "stale" | "incomplete", limit = MAX_DECORATIONS): LineDecoration[] {
  return items.slice(0, Math.max(0, Math.min(limit, MAX_DECORATIONS))).map((item) => ({
    line: item.line,
    style: status === "current" ? item.count > 0 ? "covered" : "uncovered" : status,
    label: item.branchesTotal > 0 ? `${item.branchesCovered}/${item.branchesTotal} branches` : `${item.count} hits`
  }));
}

export interface DecorationContext {
  readonly client: Pick<ExtensionProtocolClient, "listCoverageLines"> | undefined;
  readonly workspaceGeneration: string;
  readonly reportId: string;
  readonly limit?: number;
}

export class CoverageDecorations {
  #version = 0;
  constructor(private readonly readContext: () => DecorationContext, private readonly apply: (items: readonly LineDecoration[]) => void) {}

  clear(): void { this.#version++; this.apply([]); }

  async load(fileId: string, status: "current" | "stale" | "incomplete"): Promise<void> {
    const binding = this.readContext();
    if (!binding.client?.listCoverageLines || !binding.reportId || !binding.workspaceGeneration) { this.clear(); return; }
    const version = ++this.#version;
    try {
      const limit = Math.max(1, Math.min(PAGE_LIMIT, binding.limit ?? PAGE_LIMIT));
      const result = await binding.client.listCoverageLines({ workspaceGeneration: binding.workspaceGeneration, coverageReportId: binding.reportId, fileId, limit });
      const current = this.readContext();
      if (version !== this.#version || current.client !== binding.client || current.reportId !== binding.reportId || current.workspaceGeneration !== binding.workspaceGeneration) return;
      if (result.workspaceGeneration !== binding.workspaceGeneration || result.coverageReportId !== binding.reportId || result.items.length > limit) throw new Error("Coverage line page identity or limit changed.");
      this.apply(lineDecorations(result.items, status));
    } catch {
      if (version === this.#version) this.clear();
    }
  }
}
