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
