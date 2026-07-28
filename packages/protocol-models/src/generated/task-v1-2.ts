export interface TaskSnapshotBaseV12 { taskId: string; status: TaskStatusV12; createdAt: Date; lastSequence: number; outcome?: TaskOutcomeV12; startedAt?: Date; finishedAt?: Date; errorCode?: string; errorMessage?: string; }
export interface CmakeBuildTaskSnapshotV12 extends TaskSnapshotBaseV12 { kind: TaskKindV12.CmakeBuild; workspaceGeneration: string; projectId: string; buildProfileId: string; targetIds: string[]; jobs: number; timeoutMs: number; scenario?: never; }
export interface SimulationTaskSnapshotV12 extends TaskSnapshotBaseV12 { kind: TaskKindV12.Simulation; scenario: SimulationScenarioV12; timeoutMs?: number; workspaceGeneration?: never; projectId?: never; buildProfileId?: never; targetIds?: never; jobs?: never; }
export type TaskSnapshotV12 = CmakeBuildTaskSnapshotV12 | SimulationTaskSnapshotV12;
export enum TaskKindV12 { CmakeBuild = "cmakeBuild", Simulation = "simulation" }
export enum TaskStatusV12 { Queued = "queued", Running = "running", Cancelling = "cancelling", Finished = "finished" }
export enum TaskOutcomeV12 { Succeeded = "succeeded", CommandFailed = "command_failed", Cancelled = "cancelled", TimedOut = "timed_out", Interrupted = "interrupted", InfrastructureFailed = "infrastructure_failed" }
export enum SimulationScenarioV12 { Success = "success", ExitNonzero = "exit-nonzero", Hang = "hang", SpawnChild = "spawn-child", EmitOutput = "emit-output" }
