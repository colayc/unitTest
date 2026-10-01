import type { TrustState } from "./contracts.js";

export interface WorkspaceSnapshot {
  folderCount: number;
  isTrusted: boolean;
}

export function evaluateWorkspace(snapshot: WorkspaceSnapshot): TrustState {
  if (snapshot.folderCount === 0) return "no-workspace";
  if (snapshot.folderCount !== 1) return "blocked-multi-root";
  return snapshot.isTrusted ? "trusted" : "blocked-untrusted";
}

export function canStartService(state: TrustState): state is "trusted" {
  return state === "trusted";
}

/** Generation has an additional service capability gate; trust alone is not enough. */
export function canUseTestGeneration(state: TrustState, capabilities: { testGeneration?: boolean } | undefined): boolean {
  return state === "trusted" && capabilities?.testGeneration === true;
}

export class TrustGate {
  private state: TrustState | undefined;
  private readonly listeners = new Set<(state: TrustState) => void>();

  update(snapshot: WorkspaceSnapshot): TrustState {
    const nextState = evaluateWorkspace(snapshot);
    if (nextState === this.state) return nextState;

    this.state = nextState;
    for (const listener of this.listeners) listener(nextState);
    return nextState;
  }

  onTransition(listener: (state: TrustState) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
}
