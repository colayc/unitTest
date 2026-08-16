import type { ServiceStatus, TrustState } from "./contracts.js";
import type { ExtensionProtocolClient } from "./protocol-client.js";

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

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function managerError(manager: CommandManager, error: unknown): string {
  return manager.status.state === "failed" && manager.status.detail
    ? manager.status.detail
    : errorMessage(error);
}

export function registerCommands(
  context: CommandContext,
  manager: CommandManager,
  clientProvider: ClientProvider,
  output: OutputChannelLike,
  status: CommandStatus,
  host: CommandHost
): void {
  const requireTrustedWorkspace = async (): Promise<boolean> => {
    if (status.trustState === "trusted") return true;
    await host.showErrorMessage(BLOCKED_MESSAGES[status.trustState]);
    return false;
  };

  const startService = async () => {
    if (!await requireTrustedWorkspace()) return;
    status.projectService({ state: "starting" });
    try {
      await manager.start();
    } catch (error) {
      await host.showErrorMessage(managerError(manager, error));
    } finally {
      status.projectService(manager.status);
    }
  };

  const stopService = async () => {
    if (!await requireTrustedWorkspace()) return;
    status.projectService({ state: "stopping" });
    try {
      await manager.stop();
    } catch (error) {
      await host.showErrorMessage(managerError(manager, error));
    } finally {
      status.projectService(manager.status);
    }
  };

  const inspectWorkspace = async () => {
    if (!await requireTrustedWorkspace()) return;
    const client = clientProvider();
    if (!client) {
      await host.showErrorMessage("Unit Test: Service is not running.");
      return;
    }
    try {
      output.appendLine(JSON.stringify(await client.inspectWorkspace(), null, 2));
    } catch (error) {
      await host.showErrorMessage(managerError(manager, error));
      status.projectService(manager.status);
    }
  };

  context.subscriptions.push(
    host.registerCommand("unitTestIde.startService", startService),
    host.registerCommand("unitTestIde.stopService", stopService),
    host.registerCommand("unitTestIde.inspectWorkspace", inspectWorkspace)
  );
}
