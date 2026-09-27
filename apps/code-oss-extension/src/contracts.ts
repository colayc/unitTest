export const TRUST_STATES = ["no-workspace", "blocked-untrusted", "blocked-multi-root", "trusted"] as const;
export type TrustState = typeof TRUST_STATES[number];

export const SERVICE_STATES = ["stopped", "starting", "running", "stopping", "failed"] as const;
export type ServiceState = typeof SERVICE_STATES[number];

export interface ServiceStatus {
  state: ServiceState;
  detail?: string;
}

export interface ExtensionState {
  trust: TrustState;
  service: ServiceStatus;
}

export const TEST_GENERATION_COMMANDS = [
  "unitTestIde.generateTests",
  "unitTestIde.generateTestsForSymbol",
  "unitTestIde.generateTestsForFile",
  "unitTestIde.generateTestsForTarget",
  "unitTestIde.generateTestsForCoverageGap",
  "unitTestIde.reviewGeneratedTests",
  "unitTestIde.acceptGeneratedTests",
  "unitTestIde.cancelTestGeneration"
] as const;
export type TestGenerationCommand = typeof TEST_GENERATION_COMMANDS[number];

export type TestGenerationAvailability = "available" | "unavailable" | "blocked-trust" | "stale-workspace";
