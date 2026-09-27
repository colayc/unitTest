package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/protocol"
	capabilitiesv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/capabilities"
	generationv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/testgeneration"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
)

// GenerationBackend owns all source, target, candidate and preview resolution.
// A route passes only closed protocol inputs and a stable authentication realm;
// it never passes caller-provided source bytes or a candidate set to publishing.
type GenerationBackend interface {
	TestGenerationReady() bool
	ListTestGenerationTargets(context.Context, string, generationv15.TestGenerationTargetListRequestV15) (generationv15.TestGenerationTargetListV15, error)
	StartTestGeneration(context.Context, string, generationv15.TestGenerationStartRequestV15) (generationv15.TestGenerationRunV15, error)
	GetTestGenerationRun(context.Context, string, string) (generationv15.TestGenerationRunV15, error)
	ListTestGenerationCandidates(context.Context, string, generationv15.TestGenerationCandidateListRequestV15) (generationv15.TestGenerationCandidatePageV15, error)
	AcceptTestGeneration(context.Context, string, generationv15.TestGenerationAcceptRequestV15) (generationv15.TestGenerationRunV15, error)
}

var ErrCharacterizationConfirmationRequired = errors.New("characterization confirmation required")

func generationMethod(method string) bool {
	switch method {
	case "testGeneration/targets/list", "testGeneration/start", "testGeneration/runs/get", "testGeneration/candidates/list", "testGeneration/accept":
		return true
	}
	return false
}

func negotiateForGeneration(envelope string, supported []string, coverage CoverageBackend, generation GenerationBackend) (string, bool) {
	if envelope != protocol.Version15 {
		return negotiateForBackend(envelope, supported, coverage)
	}
	if coverage != nil && generation != nil && generation.TestGenerationReady() {
		return negotiateCandidates(envelope, supported, []string{protocol.Version15, protocol.Version14, protocol.Version13, protocol.Version12, protocol.Version11, protocol.Version10})
	}
	if coverage != nil {
		return negotiateCandidates(protocol.Version14, supported, []string{protocol.Version14, protocol.Version13, protocol.Version12, protocol.Version11, protocol.Version10})
	}
	return negotiateCandidates(protocol.Version13, supported, []string{protocol.Version13, protocol.Version12, protocol.Version11, protocol.Version10})
}

func capabilitiesV15() capabilitiesv15.CapabilitiesV15 {
	old := capabilitiesV14()
	adapters := make([]capabilitiesv15.FrameworkAdapterCapabilityV15, len(old.FrameworkAdapters))
	for i, adapter := range old.FrameworkAdapters {
		adapters[i] = capabilitiesv15.FrameworkAdapterCapabilityV15{
			ID: capabilitiesv15.FrameworkAdapterIDV15(adapter.ID), ContractVersion: adapter.ContractVersion,
			DisplayName: adapter.DisplayName, CanDiscoverCases: adapter.CanDiscoverCases,
			CanRunCase: adapter.CanRunCase, CanReportSkipped: adapter.CanReportSkipped,
			CanReportSourceLocation: adapter.CanReportSourceLocation, CanReportMockDetails: adapter.CanReportMockDetails,
		}
	}
	return capabilitiesv15.CapabilitiesV15{
		WorkspaceInspect: old.WorkspaceInspect, TargetList: old.TargetList, CmakeBuild: old.CmakeBuild,
		TestDiscovery: old.TestDiscovery, TestRun: old.TestRun, CoverageRun: old.CoverageRun,
		CoverageReport: old.CoverageReport, CtestJSON: old.CtestJSON, OpaqueCTestFallback: old.OpaqueCTestFallback,
		MaxRepeatCount: float64(old.MaxRepeatCount), MaxSelectionSize: float64(old.MaxSelectionSize),
		MaxCatalogPageSize: float64(old.MaxCatalogPageSize), MaxCoveragePageSize: float64(old.MaxCoveragePageSize),
		MaxCoverageTimeoutMS: float64(old.MaxCoverageTimeoutMS), UnityHelperContractVersion: old.UnityHelperContractVersion,
		UnityRunnerContractVersion: old.UnityRunnerContractVersion, FrameworkAdapters: adapters,
		TestGeneration: true, MaxTestGenerationCandidates: 1000,
	}
}

func (s *Session) generationOwner() string {
	digest := sha256.Sum256([]byte(s.token))
	return hex.EncodeToString(digest[:])
}

func (s *Session) handleGeneration(ctx context.Context, version string, request protocol.Request, backend GenerationBackend) HandleResult {
	owner := s.generationOwner()
	switch request.Method {
	case "testGeneration/targets/list":
		input, err := decodeStrict[generationv15.TestGenerationTargetListRequestV15](request.Payload)
		if err != nil || !validHash(input.WorkspaceGeneration) || !validProjectID(input.ProjectID) || !validGenerationPage(input.Cursor, input.Limit) {
			return invalidPayload(version, request)
		}
		value, err := backend.ListTestGenerationTargets(ctx, owner, input)
		if err != nil {
			return generationFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, value))
	case "testGeneration/start":
		input, err := decodeStrict[generationv15.TestGenerationStartRequestV15](request.Payload)
		if err != nil || !validGenerationStart(input) {
			return invalidPayload(version, request)
		}
		value, err := backend.StartTestGeneration(ctx, owner, input)
		if err != nil {
			return generationFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, value))
	case "testGeneration/runs/get":
		input, err := decodeStrict[generationv15.TestGenerationRunIDRequestV15](request.Payload)
		if err != nil || !validID(input.RunID) {
			return invalidPayload(version, request)
		}
		value, err := backend.GetTestGenerationRun(ctx, owner, input.RunID)
		if err != nil {
			return generationFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, value))
	case "testGeneration/candidates/list":
		input, err := decodeStrict[generationv15.TestGenerationCandidateListRequestV15](request.Payload)
		if err != nil || !validID(input.RunID) || !validGenerationPage(input.Cursor, input.Limit) {
			return invalidPayload(version, request)
		}
		value, err := backend.ListTestGenerationCandidates(ctx, owner, input)
		if err != nil {
			return generationFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, value))
	case "testGeneration/accept":
		input, err := decodeStrict[generationv15.TestGenerationAcceptRequestV15](request.Payload)
		if err != nil || !validID(input.RunID) || !validID(input.CandidateID) || !validHash(input.ConfirmationDigest) {
			return invalidPayload(version, request)
		}
		value, err := backend.AcceptTestGeneration(ctx, owner, input)
		if err != nil {
			return generationFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, value))
	}
	return handled(protocol.Failure(version, request, "METHOD_NOT_FOUND", "method is not supported", false))
}

func validGenerationPage(cursor *string, limit *int64) bool {
	return (cursor == nil || len(*cursor) > 0 && len(*cursor) <= 4096) &&
		(limit == nil || *limit >= 1 && *limit <= 200)
}

func validGenerationStart(input generationv15.TestGenerationStartRequestV15) bool {
	placeholder := "0000000000000000000000000000000000000000000000000000000000000000"
	request := testgendomain.Request{
		IdempotencyKey: input.IdempotencyKey, WorkspaceGeneration: input.WorkspaceGeneration,
		ProjectID: input.ProjectID, Scope: testgendomain.Scope(input.Scope), Framework: testgendomain.Framework(input.Framework),
		Goals:                 testgendomain.Goals{FunctionPercent: input.Goals.FunctionPercent, LinePercent: input.Goals.LinePercent, BranchPercent: input.Goals.BranchPercent},
		Budgets:               testgendomain.Budgets{WallTimeMS: input.Budgets.WallTimeMS, CandidateCount: input.Budgets.CandidateCount, MemoryMiB: input.Budgets.MemoryMiB, Concurrency: input.Budgets.Concurrency},
		CompileSnapshotDigest: placeholder, CoverageSnapshotDigest: placeholder, SourceDigest: placeholder,
		CMakeTargetDigest: placeholder, FrameworkBundleDigest: placeholder, AnalyzerBundleDigest: placeholder,
		BaselineReportDigest: placeholder, ProcessOwnerDigest: placeholder,
	}
	if input.SymbolID != nil {
		request.SymbolID = *input.SymbolID
	}
	if input.File != nil {
		request.File = *input.File
	}
	if input.TargetID != nil {
		request.TargetID = *input.TargetID
	}
	if input.CoverageReportID != nil {
		request.CoverageReportID = *input.CoverageReportID
	}
	return testgendomain.ValidateRequest(request) == nil &&
		!math.IsNaN(input.Goals.FunctionPercent) && !math.IsInf(input.Goals.FunctionPercent, 0)
}

func generationFailure(version string, request protocol.Request, err error) HandleResult {
	code, message, retryable := "SERVICE_UNHEALTHY", "test generation service is unavailable", true
	switch {
	case errors.Is(err, build.ErrWorkspaceTrustRequired):
		code, message, retryable = "WORKSPACE_TRUST_REQUIRED", "workspace trust is required", false
	case errors.Is(err, task.ErrNotFound):
		code, message, retryable = "TASK_NOT_FOUND", "generation run was not found", false
	case errors.Is(err, testgendomain.ErrStaleSnapshot), errors.Is(err, testgenpublish.ErrConflict):
		code, message, retryable = "WORKSPACE_CHANGED", "generation preview is stale", false
	case errors.Is(err, ErrCharacterizationConfirmationRequired):
		code, message, retryable = "INVALID_TASK_SPEC", "characterization confirmation is required", false
	case errors.Is(err, task.ErrInvalidArgument):
		code, message, retryable = "INVALID_MESSAGE", "request argument is invalid", false
	}
	return handled(protocol.Failure(version, request, code, message, retryable))
}
