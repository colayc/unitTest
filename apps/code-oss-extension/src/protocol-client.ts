import { ProtocolClient, type WorkspaceSnapshot } from "@unit-test-ide/test-client";

export interface ExtensionProtocolClient {
  inspectWorkspace(): Promise<WorkspaceSnapshot>;
  close(): void;
}

export function createProtocolClient(endpoint: string): Promise<ProtocolClient> {
  return ProtocolClient.connect(endpoint);
}
