package session

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/protocol"
	coveragev14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/coverage"
	capabilitiesv14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/capabilities"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

type CoverageBackend interface {
	StartCoverageRun(context.Context, CoverageRunStart) (task.Task, coveragedomain.Run, testdomain.TestRun, error)
	GetCoverageRun(context.Context, string) (coveragedomain.Run, error)
	ListCoverageRuns(context.Context, coveragedomain.RunPageRequest) (coveragedomain.RunPage, error)
	GetCoverageReport(context.Context, string) (coveragedomain.Report, error)
}

type CoverageRunStart struct {
	IdempotencyKey      string
	WorkspaceGeneration string
	ProjectID           string
	CoverageProfileID   string
	CatalogRevision     string
	Selection           testdomain.Selection
	RepeatCount         int64
	Timeout             time.Duration
}

type coverageRunStartPayloadV14 struct {
	IdempotencyKey      string          `json:"idempotencyKey"`
	WorkspaceGeneration string          `json:"workspaceGeneration"`
	ProjectID           string          `json:"projectId"`
	CoverageProfileID   string          `json:"coverageProfileId"`
	CatalogRevision     string          `json:"catalogRevision"`
	Selection           json.RawMessage `json:"selection"`
	RepeatCount         int64           `json:"repeatCount"`
	TimeoutMS           int64           `json:"timeoutMs"`
}

type coverageRunIDPayloadV14 struct {
	CoverageRunID string `json:"coverageRunId"`
}

type coverageReportIDPayloadV14 struct {
	ReportID string `json:"reportId"`
}

type coverageRunsListPayloadV14 struct {
	ProjectID         *string `json:"projectId"`
	CoverageProfileID *string `json:"coverageProfileId"`
	Cursor            *string `json:"cursor"`
	Limit             *int    `json:"limit"`
}

func coverageMethod(method string) bool {
	switch method {
	case "coverage/runs/start", "coverage/runs/get", "coverage/runs/list", "coverage/reports/get":
		return true
	default:
		return false
	}
}

func negotiateForBackend(envelopeVersion string, supported []string, backend any) (string, bool) {
	if _, ok := backend.(CoverageBackend); !ok {
		return negotiate(envelopeVersion, supported)
	}
	if envelopeVersion == protocolVersion10 && len(supported) == 0 {
		return protocolVersion10, true
	}
	candidates := []string{protocolVersion14, protocolVersion13, protocolVersion12, protocolVersion11, protocolVersion10}
	return negotiateCandidates(envelopeVersion, supported, candidates)
}

const (
	protocolVersion10 = "1.0"
	protocolVersion11 = "1.1"
	protocolVersion12 = "1.2"
	protocolVersion13 = "1.3"
	protocolVersion14 = "1.4"
)

func negotiateCandidates(envelopeVersion string, supported, candidates []string) (string, bool) {
	maximum := -1
	for index, candidate := range candidates {
		if candidate == envelopeVersion {
			maximum = index
			break
		}
	}
	if maximum < 0 {
		return "", false
	}
	for _, candidate := range candidates[maximum:] {
		for _, offered := range supported {
			if offered == candidate {
				return candidate, true
			}
		}
	}
	return "", false
}

func capabilitiesV14() capabilitiesv14.CapabilitiesV14 {
	return capabilitiesv14.CapabilitiesV14{
		WorkspaceInspect: true, TargetList: true, CmakeBuild: true,
		TestDiscovery: true, TestRun: true, CoverageRun: true, CoverageReport: true,
		CtestJSON: true, OpaqueCTestFallback: true, MaxRepeatCount: 100,
		MaxSelectionSize: 100_000, MaxCatalogPageSize: 1_000, MaxCoveragePageSize: 200,
		MaxCoverageTimeoutMS: int64((24 * time.Hour) / time.Millisecond),
		UnityHelperContractVersion: "1", UnityRunnerContractVersion: "utide.runner.v1",
		FrameworkAdapters: []capabilitiesv14.FrameworkAdapterCapabilityV14{
			{ID: capabilitiesv14.Cpputest, ContractVersion: "cpputest.v1", DisplayName: "CppUTest / CppUMock", CanDiscoverCases: true, CanRunCase: true, CanReportSkipped: true, CanReportSourceLocation: true, CanReportMockDetails: true},
			{ID: capabilitiesv14.Unity, ContractVersion: "utide.runner.v1", DisplayName: "Unity / CMock", CanDiscoverCases: true, CanRunCase: true, CanReportSkipped: true, CanReportSourceLocation: true, CanReportMockDetails: true},
		},
	}
}

func (s *Session) handleCoverage(ctx context.Context, version string, request protocol.Request, backend CoverageBackend) HandleResult {
	switch request.Method {
	case "coverage/runs/start":
		payload, err := decodeStrict[coverageRunStartPayloadV14](request.Payload)
		if err != nil || !validID(payload.IdempotencyKey) || !validHash(payload.WorkspaceGeneration) || !validProjectID(payload.ProjectID) || !validProjectID(payload.CoverageProfileID) || !validHash(payload.CatalogRevision) || payload.RepeatCount < 1 || payload.RepeatCount > 100 || payload.TimeoutMS < 1 || payload.TimeoutMS > maxTimeoutMS || payload.TimeoutMS%int64(time.Millisecond) != 0 {
			return invalidPayload(version, request)
		}
		selection, err := decodeTestSelection(payload.Selection)
		if err != nil {
			return invalidPayload(version, request)
		}
		started, run, _, err := backend.StartCoverageRun(ctx, CoverageRunStart{
			IdempotencyKey: payload.IdempotencyKey, WorkspaceGeneration: payload.WorkspaceGeneration,
			ProjectID: payload.ProjectID, CoverageProfileID: payload.CoverageProfileID,
			CatalogRevision: payload.CatalogRevision, Selection: selection,
			RepeatCount: payload.RepeatCount, Timeout: time.Duration(payload.TimeoutMS) * time.Millisecond,
		})
		if err != nil {
			return backendFailure(version, request, err)
		}
		projected, err := toProtocolCoverageRun(run)
		if err != nil || started.ID != run.TaskID {
			return backendFailure(version, request, errors.New("invalid coverage start projection"))
		}
		return handled(protocol.Success(version, request, projected))
	case "coverage/runs/get":
		payload, err := decodeStrict[coverageRunIDPayloadV14](request.Payload)
		if err != nil || !validID(payload.CoverageRunID) {
			return invalidPayload(version, request)
		}
		run, err := backend.GetCoverageRun(ctx, payload.CoverageRunID)
		if err != nil {
			return backendFailure(version, request, err)
		}
		projected, err := toProtocolCoverageRun(run)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, projected))
	case "coverage/runs/list":
		payload, err := decodeStrict[coverageRunsListPayloadV14](request.Payload)
		cursor, limit, pageErr := normalizedPage(payload.Cursor, payload.Limit)
		if err != nil || pageErr != nil || payload.ProjectID != nil && !validProjectID(*payload.ProjectID) || payload.CoverageProfileID != nil && !validProjectID(*payload.CoverageProfileID) {
			return invalidPayload(version, request)
		}
		page, err := backend.ListCoverageRuns(ctx, coveragedomain.RunPageRequest{ProjectID: optionalString(payload.ProjectID), CoverageProfileID: optionalString(payload.CoverageProfileID), Cursor: cursor, Limit: limit})
		if err != nil {
			return backendFailure(version, request, err)
		}
		projected, err := toProtocolCoverageRunPage(page)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, projected))
	case "coverage/reports/get":
		payload, err := decodeStrict[coverageReportIDPayloadV14](request.Payload)
		if err != nil || !validID(payload.ReportID) {
			return invalidPayload(version, request)
		}
		report, err := backend.GetCoverageReport(ctx, payload.ReportID)
		if err != nil {
			return backendFailure(version, request, err)
		}
		projected, err := toProtocolCoverageReport(report)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, projected))
	default:
		return handled(protocol.Failure(version, request, "METHOD_NOT_FOUND", "method is not supported", false))
	}
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
