import type { ServiceStatus, TrustState } from "./contracts.js";
import type { ExtensionProtocolClient } from "./protocol-client.js";
import { redactServiceError } from "./service-resources.js";

export interface DisposableLike {
  dispose(): unknown;
}

export interface CommandContext {
  subscriptions: DisposableLike[];
}

export interface OutputChannelLike extends DisposableLike {
  appendLine(value: string): void;
}

export interface StatusBarLike extends DisposableLike {
  text: string;
  show(): void;
}

export interface LifecycleSession {
  readonly client: ExtensionProtocolClient;
}

export interface CommandManager {
  readonly status: ServiceStatus;
  readonly session: LifecycleSession | undefined;
  start(): Promise<LifecycleSession>;
  stop(): Promise<void>;
}

export interface CommandStatus {
  readonly trustState: TrustState;
  isActive(): boolean;
  refreshTrust(): TrustState;
  projectService(status: ServiceStatus): void;
}

export interface CommandHost {
  registerCommand(command: string, handler: () => void | Promise<void>): DisposableLike;
  showErrorMessage(message: string): void | PromiseLike<unknown>;
}

export type ClientProvider = () => ExtensionProtocolClient | undefined;

const BLOCKED_MESSAGES: Record<Exclude<TrustState, "trusted">, string> = {
  "no-workspace": "Unit Test: Open a workspace to use the service.",
  "blocked-multi-root": "Unit Test: Multi-root workspaces are not supported.",
  "blocked-untrusted": "Unit Test: Trust this workspace to use the service."
};

export async function presentManagerError(
  host: Pick<CommandHost, "showErrorMessage">,
  manager: Pick<CommandManager, "status">,
  fallback: string
): Promise<void> {
  const message = manager.status.state === "failed" && manager.status.detail
    ? manager.status.detail
    : fallback;
  await host.showErrorMessage(redactServiceError(message, []).message);
}

export function registerCommands(
  context: CommandContext,
  manager: CommandManager,
  clientProvider: ClientProvider,
  output: OutputChannelLike,
  status: CommandStatus,
  host: CommandHost,
  onServiceChanged?: (state: "started" | "stopped") => void | Promise<void>
): void {
  const currentAuthorization = (): { allowed: boolean; message?: string } => {
    if (!status.isActive()) return { allowed: false };
    const trustState = status.refreshTrust();
    return trustState === "trusted"
      ? { allowed: true }
      : { allowed: false, message: BLOCKED_MESSAGES[trustState] };
  };

  const requireTrustedWorkspace = async (): Promise<boolean> => {
    const authorization = currentAuthorization();
    if (authorization.allowed) return true;
    if (authorization.message) await host.showErrorMessage(authorization.message);
    return false;
  };

  const startService = async () => {
    if (!await requireTrustedWorkspace()) return;
    const authorization = currentAuthorization();
    if (!authorization.allowed) {
      if (authorization.message) await host.showErrorMessage(authorization.message);
      return;
    }
    status.projectService({ state: "starting" });
    try {
      await manager.start();
      await onServiceChanged?.("started");
    } catch {
      await presentManagerError(host, manager, "Unit Test: Service start failed.");
    } finally {
      status.projectService(manager.status);
    }
  };

  const stopService = async () => {
    if (!await requireTrustedWorkspace()) return;
    const authorization = currentAuthorization();
    if (!authorization.allowed) {
      if (authorization.message) await host.showErrorMessage(authorization.message);
      return;
    }
    status.projectService({ state: "stopping" });
    try {
      await manager.stop();
      await onServiceChanged?.("stopped");
    } catch {
      await presentManagerError(host, manager, "Unit Test: Service stop failed.");
    } finally {
      status.projectService(manager.status);
    }
  };

  const inspectWorkspace = async () => {
    if (!await requireTrustedWorkspace()) return;
    const authorization = currentAuthorization();
    if (!authorization.allowed) {
      if (authorization.message) await host.showErrorMessage(authorization.message);
      return;
    }
    const client = clientProvider();
    if (!client) {
      await host.showErrorMessage("Unit Test: Service is not running.");
      return;
    }
    try {
      output.appendLine(JSON.stringify(await client.inspectWorkspace(), null, 2));
    } catch {
      await presentManagerError(host, manager, "Unit Test: Workspace inspection failed.");
      status.projectService(manager.status);
    }
  };

  context.subscriptions.push(
    host.registerCommand("unitTestIde.startService", startService),
    host.registerCommand("unitTestIde.stopService", stopService),
    host.registerCommand("unitTestIde.inspectWorkspace", inspectWorkspace)
  );
}
