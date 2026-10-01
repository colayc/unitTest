package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"

	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/protocol"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

// ManagedGenerationBackend is an additive contract. Implementations must not
// report ready until they can resolve current coverage IDs and validate the
// exact selected output with real compile, test, and coverage execution.
type ManagedGenerationBackend interface {
	GenerationBackend
	ManagedTestsReady() bool
	ListManagedTests(context.Context, managedtest.Query) (managedtest.Page, error)
	GetManagedReview(context.Context, string, string) (managedtest.Review, error)
	ApplyManagedReview(context.Context, managedtest.ApplyRequest) (testgendomain.Run, error)
}

// ManagedReviewPageBackend is read-only. It does not authorize review apply or
// affect protocol negotiation on its own.
type ManagedReviewPageBackend interface {
	GetManagedReviewPage(context.Context, string, string, string, int) (generationv16.ManagedReviewV16, error)
}

type ManagedStartResolverBackend interface {
	ResolveManagedStart(context.Context, string, generationv16.TestGenerationStartRequestV16) (testgendomain.ManagedTarget, error)
}

// These opt-in interfaces keep the legacy read-only v1.6 facade from
// accidentally acquiring a write route merely by implementing its older
// ManagedGenerationBackend method set.
type ManagedStartBackend interface {
	StartManaged(context.Context, string, generationv16.TestGenerationStartRequestV16) (generationv16.TestGenerationRunV16, error)
}

// ManagedRunReadBackend keeps v1.6 reads on the same authenticated, durable
// run as StartManaged. The legacy v1.5 producer is not a substitute.
type ManagedRunReadBackend interface {
	ListManagedTargets(context.Context, string, generationv16.TestGenerationTargetListRequestV16) (generationv16.TestGenerationTargetListV16, error)
	GetManagedRun(context.Context, string, string) (generationv16.TestGenerationRunV16, error)
	CancelManagedRun(context.Context, string, string) (generationv16.TestGenerationRunV16, error)
	ReplayManagedEvents(context.Context, string, generationv16.TestGenerationEventReplayRequestV16) (generationv16.TestGenerationEventPageV16, error)
	ListManagedCandidates(context.Context, string, generationv16.TestGenerationCandidateListRequestV16) (generationv16.TestGenerationCandidatePageV16, error)
}

type ManagedApplyBackend interface {
	ManagedApplyReady() bool
}

type ManagedRecordsPageBackend interface {
	ListManagedTestRecords(context.Context, string, generationv16.ManagedRecordsRequestV16) (generationv16.ManagedTestRecordPageV16, error)
}

func validManagedStart(input generationv16.TestGenerationStartRequestV16) bool {
	if !validID(input.IdempotencyKey) || !validHash(input.WorkspaceGeneration) || !validProjectID(input.ProjectID) ||
		input.Budgets.WallTimeMS < 1 || input.Budgets.WallTimeMS > 86400000 ||
		input.Budgets.CandidateCount < 1 || input.Budgets.CandidateCount > 1000 ||
		input.Budgets.MemoryMiB < 64 || input.Budgets.MemoryMiB > 1048576 ||
		input.Budgets.Concurrency < 1 || input.Budgets.Concurrency > 256 || input.Budgets.Concurrency > input.Budgets.CandidateCount {
		return false
	}
	for _, goal := range []float64{input.Goals.FunctionPercent, input.Goals.LinePercent, input.Goals.BranchPercent} {
		if math.IsNaN(goal) || math.IsInf(goal, 0) || goal < 0 || goal > 100 {
			return false
		}
	}
	switch input.Framework {
	case generationv16.Auto, generationv16.TestGenerationFrameworkV16Cpputest, generationv16.TestGenerationFrameworkV16Unity:
	default:
		return false
	}
	ids := func(values ...*string) bool {
		for _, value := range values {
			if value != nil {
				return false
			}
		}
		return true
	}
	switch input.Scope {
	case generationv16.Symbol:
		return input.FunctionID != nil && validID(*input.FunctionID) && ids(input.FileID, input.CoverageGapID, input.TargetID, input.CoverageReportID)
	case generationv16.File:
		return input.FileID != nil && validID(*input.FileID) && ids(input.FunctionID, input.CoverageGapID, input.TargetID, input.CoverageReportID)
	case generationv16.TestGenerationScopeV16CoverageGap:
		return input.CoverageGapID != nil && validID(*input.CoverageGapID) && input.CoverageReportID != nil && validID(*input.CoverageReportID) && ids(input.FunctionID, input.FileID, input.TargetID)
	case generationv16.Target:
		return input.TargetID != nil && validHash(*input.TargetID) && ids(input.FunctionID, input.FileID, input.CoverageGapID, input.CoverageReportID)
	case generationv16.Workspace:
		return ids(input.FunctionID, input.FileID, input.CoverageGapID, input.CoverageReportID, input.TargetID)
	}
	return false
}

func (s *Session) handleManagedGeneration(ctx context.Context, version string, request protocol.Request) HandleResult {
	var start generationv16.TestGenerationStartRequestV16
	if request.Method == "testGeneration/start" {
		var err error
		start, err = decodeStrict[generationv16.TestGenerationStartRequestV16](request.Payload)
		if err != nil || !validManagedStart(start) {
			return invalidPayload(version, request)
		}
	}
	provider, ok := s.generationBackend.(ManagedGenerationBackend)
	if !ok || !provider.ManagedTestsReady() || s.managedTests == nil || !s.managedTests.ManagedTestsReady() {
		return handled(protocol.Failure(version, request, "PROTOCOL_FEATURE_UNAVAILABLE", "managed generation is not available", false))
	}
	if request.Method == "testGeneration/start" {
		resolver, ok := s.generationBackend.(ManagedStartResolverBackend)
		if !ok {
			return handled(protocol.Failure(version, request, "PROTOCOL_FEATURE_UNAVAILABLE", "managed generation ID resolution is not available", false))
		}
		if _, err := resolver.ResolveManagedStart(ctx, s.generationOwner(), start); err != nil {
			return generationFailure(version, request, err)
		}
		starter, ok := s.generationBackend.(ManagedStartBackend)
		if !ok {
			return handled(protocol.Failure(version, request, "PROTOCOL_FEATURE_UNAVAILABLE", "managed generation is not available", false))
		}
		run, err := starter.StartManaged(ctx, s.generationOwner(), start)
		if err != nil {
			return generationFailure(version, request, err)
		}
		if run.ProjectID != start.ProjectID || run.WorkspaceGeneration != start.WorkspaceGeneration || !validID(run.RunID) || !validManagedRunResponse(run, run.RunID) {
			return handled(protocol.Failure(version, request, "SERVICE_UNHEALTHY", "managed generation run is invalid", true))
		}
		return handled(protocol.Success(version, request, run))
	}
	reader, ok := s.generationBackend.(ManagedRunReadBackend)
	if !ok {
		return handled(protocol.Failure(version, request, "PROTOCOL_FEATURE_UNAVAILABLE", "managed generation read is not available", false))
	}
	owner := s.generationOwner()
	switch request.Method {
	case "testGeneration/targets/list":
		input, err := decodeStrict[generationv16.TestGenerationTargetListRequestV16](request.Payload)
		if err != nil || !validProjectID(input.ProjectID) || !validHash(input.WorkspaceGeneration) || !validGenerationPage(input.Cursor, input.Limit) {
			return invalidPayload(version, request)
		}
		page, err := reader.ListManagedTargets(ctx, owner, input)
		if err != nil {
			return generationFailure(version, request, err)
		}
		limit := 200
		if input.Limit != nil {
			limit = int(*input.Limit)
		}
		if len(page.Items) > limit || page.NextCursor != nil && (len(page.Items) == 0 || len(*page.NextCursor) == 0 || len(*page.NextCursor) > 4096 || input.Cursor != nil && *input.Cursor == *page.NextCursor) {
			return invalidManagedResponse(version, request)
		}
		for _, item := range page.Items {
			if item.Kind != generationv16.BuildTarget || !validHash(item.TargetID) || len(item.Frameworks) == 0 || len(item.Frameworks) > 2 {
				return invalidManagedResponse(version, request)
			}
			for _, framework := range item.Frameworks {
				if framework != generationv16.FrameworkCpputest && framework != generationv16.FrameworkUnity {
					return invalidManagedResponse(version, request)
				}
			}
		}
		return handled(protocol.Success(version, request, page))
	case "testGeneration/runs/get", "testGeneration/runs/cancel":
		input, err := decodeStrict[generationv16.TestGenerationRunIDRequestV16](request.Payload)
		if err != nil || !validID(input.RunID) {
			return invalidPayload(version, request)
		}
		var run generationv16.TestGenerationRunV16
		if request.Method == "testGeneration/runs/get" {
			run, err = reader.GetManagedRun(ctx, owner, input.RunID)
		} else {
			run, err = reader.CancelManagedRun(ctx, owner, input.RunID)
		}
		if err != nil {
			return generationFailure(version, request, err)
		}
		if !validManagedRunResponse(run, input.RunID) {
			return invalidManagedResponse(version, request)
		}
		return handled(protocol.Success(version, request, run))
	case "testGeneration/events/replay":
		input, err := decodeStrict[generationv16.TestGenerationEventReplayRequestV16](request.Payload)
		if err != nil || !validID(input.RunID) || input.AfterSequence < 0 || input.AfterSequence > 9007199254740991 || input.Limit != nil && (*input.Limit < 1 || *input.Limit > 200) {
			return invalidPayload(version, request)
		}
		page, err := reader.ReplayManagedEvents(ctx, owner, input)
		if err != nil {
			return generationFailure(version, request, err)
		}
		limit := 100
		if input.Limit != nil {
			limit = int(*input.Limit)
		}
		if len(page.Items) > limit || page.NextAfterSequence < input.AfterSequence || page.NextAfterSequence > 9007199254740991 {
			return invalidManagedResponse(version, request)
		}
		previous := input.AfterSequence
		for _, event := range page.Items {
			if event.Sequence <= previous || event.Sequence > page.NextAfterSequence || !validManagedState(event.State) || event.OccurredAt.IsZero() {
				return invalidManagedResponse(version, request)
			}
			previous = event.Sequence
		}
		if len(page.Items) > 0 && previous != page.NextAfterSequence || len(page.Items) == 0 && page.NextAfterSequence != input.AfterSequence {
			return invalidManagedResponse(version, request)
		}
		return handled(protocol.Success(version, request, page))
	case "testGeneration/candidates/list":
		input, err := decodeStrict[generationv16.TestGenerationCandidateListRequestV16](request.Payload)
		if err != nil || !validID(input.RunID) || !validGenerationPage(input.Cursor, input.Limit) {
			return invalidPayload(version, request)
		}
		page, err := reader.ListManagedCandidates(ctx, owner, input)
		if err != nil {
			return generationFailure(version, request, err)
		}
		limit := 100
		if input.Limit != nil {
			limit = int(*input.Limit)
		}
		if len(page.Items) > limit || page.NextCursor != nil && (len(page.Items) == 0 || len(*page.NextCursor) == 0 || len(*page.NextCursor) > 4096 || input.Cursor != nil && *page.NextCursor == *input.Cursor) {
			return invalidManagedResponse(version, request)
		}
		for _, item := range page.Items {
			if !validManagedCandidate(item) {
				return invalidManagedResponse(version, request)
			}
		}
		encoded, encodeErr := json.Marshal(page)
		if encodeErr != nil || len(encoded) > 2<<20 {
			return invalidManagedResponse(version, request)
		}
		return handled(protocol.Success(version, request, page))
	case "testGeneration/accept":
		// Managed publication is exclusively reviews/apply through the journal.
		return handled(protocol.Failure(version, request, "PROTOCOL_FEATURE_UNAVAILABLE", "managed reviews must be applied through the review publisher", false))
	}
	return handled(protocol.Failure(version, request, "METHOD_NOT_FOUND", "method is not supported", false))
}

func invalidManagedResponse(version string, request protocol.Request) HandleResult {
	return handled(protocol.Failure(version, request, "SERVICE_UNHEALTHY", "managed generation response is invalid", true))
}

func validManagedState(state generationv16.TestGenerationStateV16) bool {
	switch state {
	case generationv16.Accepted, generationv16.Analyzing, generationv16.AwaitingConfirmation, generationv16.Baseline,
		generationv16.Cancelled, generationv16.Failed, generationv16.Minimizing, generationv16.Queued,
		generationv16.Rejected, generationv16.Rendering, generationv16.Solving, generationv16.Validating:
		return true
	}
	return false
}

func validManagedRunResponse(run generationv16.TestGenerationRunV16, runID string) bool {
	if run.Preview != nil {
		preview := run.Preview
		if !validHash(preview.CandidateSetDigest) || !validHash(preview.DiffDigest) || !validHash(preview.ConfirmationDigest) ||
			preview.CharacterizationDigest != nil && !validHash(*preview.CharacterizationDigest) {
			return false
		}
		if preview.Diff != nil {
			if len(*preview.Diff) == 0 || len(*preview.Diff) > 262144 {
				return false
			}
			sum := sha256.Sum256([]byte(*preview.Diff))
			if hex.EncodeToString(sum[:]) != preview.DiffDigest {
				return false
			}
		}
	}
	terminal := run.State == generationv16.Accepted || run.State == generationv16.Rejected || run.State == generationv16.Cancelled || run.State == generationv16.Failed
	return run.RunID == runID && validID(run.TaskID) && validProjectID(run.ProjectID) && validHash(run.WorkspaceGeneration) && !run.CreatedAt.IsZero() &&
		(terminal == (run.FinishedAt != nil)) &&
		validManagedState(run.State) && run.LastSequence >= 0 && run.LastSequence <= 9007199254740991 &&
		(run.CandidateCount == nil || *run.CandidateCount >= 0 && *run.CandidateCount <= 1000)
}

func validManagedCandidate(item generationv16.TestGenerationCandidateV16) bool {
	if !validID(item.CandidateID) || !validHash(item.CodeDigest) || !validHash(item.ArtifactDigest) || !validHash(item.AssertionProvenance.EvidenceDigest) ||
		len(item.PlannedEdits) < 1 || len(item.PlannedEdits) > 128 || len(item.Diagnostics) > 1000 ||
		!validManagedCoverage(item.BaselineCoverage) || !validManagedCoverage(item.DeltaCoverage) {
		return false
	}
	switch item.Kind {
	case generationv16.Verified:
		if item.AssertionProvenance.Kind != generationv16.IndependentOracle || item.CharacterizationConfirmed {
			return false
		}
	case generationv16.Characterization:
		if item.AssertionProvenance.Kind != generationv16.ObservedOutput {
			return false
		}
	default:
		return false
	}
	for _, edit := range item.PlannedEdits {
		if !validManagedRelativePath(edit.Path) || edit.Operation != generationv16.Create && edit.Operation != generationv16.Modify || !validHash(edit.AfterDigest) || edit.BeforeDigest != nil && !validHash(*edit.BeforeDigest) {
			return false
		}
	}
	for _, diagnostic := range item.Diagnostics {
		switch diagnostic.Code {
		case generationv16.CoverageGap, generationv16.TargetUnsupported, generationv16.OracleUnavailable, generationv16.BudgetExceeded, generationv16.ValidationFailed, generationv16.NoCandidate:
		default:
			return false
		}
		if diagnostic.Severity != generationv16.Info && diagnostic.Severity != generationv16.Warning && diagnostic.Severity != generationv16.Error {
			return false
		}
		if diagnostic.Reason != nil {
			switch *diagnostic.Reason {
			case generationv16.UncoveredBranch, generationv16.UncoveredLine, generationv16.UncoveredFunction, generationv16.BudgetLimit,
				generationv16.UnsupportedTarget, generationv16.TestGenerationDiagnosticReasonV16OracleUnavailable, generationv16.TestGenerationDiagnosticReasonV16ValidationFailed:
			default:
				return false
			}
		}
	}
	return true
}

func validManagedRelativePath(value string) bool {
	if len(value) == 0 || len(value) > 1024 {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, char := range part {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '.' || char == '-') {
				return false
			}
		}
	}
	return true
}

func validManagedCoverage(value generationv16.TestGenerationCoverageV16) bool {
	for _, percent := range []float64{value.FunctionPercent, value.LinePercent, value.BranchPercent} {
		if math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 || percent > 100 {
			return false
		}
	}
	return true
}

func (s *Session) handleManagedTests(ctx context.Context, version string, request protocol.Request) HandleResult {
	provider, ok := s.generationBackend.(ManagedGenerationBackend)
	if !ok || !provider.ManagedTestsReady() || s.managedTests == nil || !s.managedTests.ManagedTestsReady() {
		return handled(protocol.Failure(version, request, "PROTOCOL_FEATURE_UNAVAILABLE", "managed test service is not available", false))
	}
	if request.Method == "managedTests/records/list" {
		reader, ok := s.generationBackend.(ManagedRecordsPageBackend)
		if !ok {
			return handled(protocol.Failure(version, request, "PROTOCOL_FEATURE_UNAVAILABLE", "managed record read is not available", false))
		}
		input, err := decodeStrict[generationv16.ManagedRecordsRequestV16](request.Payload)
		if err != nil || !validProjectID(input.ProjectID) || !validHash(input.WorkspaceGeneration) || !validID(input.CoverageReportID) ||
			input.FileID != nil && !validID(*input.FileID) || !validGenerationPage(input.Cursor, input.Limit) ||
			input.Status != nil && !managedtest.ValidStatus(managedtest.Status(*input.Status)) {
			return invalidPayload(version, request)
		}
		limit := 200
		if input.Limit != nil {
			limit = int(*input.Limit)
		}
		page, err := reader.ListManagedTestRecords(ctx, s.generationOwner(), input)
		if err != nil {
			return generationFailure(version, request, err)
		}
		encoded, encodeErr := json.Marshal(page)
		if encodeErr != nil || len(encoded) > 512<<10 || page.CoverageReportID != input.CoverageReportID ||
			page.WorkspaceGeneration != input.WorkspaceGeneration || len(page.Items) > limit ||
			page.NextCursor != nil && (len(*page.NextCursor) == 0 || len(*page.NextCursor) > 4096) {
			return handled(protocol.Failure(version, request, "SERVICE_UNHEALTHY", "managed record page is invalid", true))
		}
		for _, item := range page.Items {
			if len(item.CaseID) != 36 || item.CaseID[:4] != "utc_" || !validID(item.CaseID[4:]) ||
				!validID(item.FileID) || !validID(item.FunctionID) || !protocol.ValidManagedRecordDigestsV16(item) ||
				!managedtest.ValidStatus(managedtest.Status(item.Status)) {
				return handled(protocol.Failure(version, request, "SERVICE_UNHEALTHY", "managed record item is invalid", true))
			}
		}
		return handled(protocol.Success(version, request, page))
	}
	if request.Method == "managedTests/reviews/get" {
		reader, ok := s.generationBackend.(ManagedReviewPageBackend)
		if !ok {
			return handled(protocol.Failure(version, request, "PROTOCOL_FEATURE_UNAVAILABLE", "managed review read is not available", false))
		}
		input, err := decodeStrict[generationv16.ManagedReviewIDRequestV16](request.Payload)
		if err != nil || !validID(input.ReviewID) || !validGenerationPage(input.Cursor, input.Limit) || input.Limit != nil && *input.Limit > protocol.MaxManagedReviewPageItemsV16 {
			return invalidPayload(version, request)
		}
		cursor, limit := "", protocol.MaxManagedReviewPageItemsV16
		if input.Cursor != nil {
			cursor = *input.Cursor
		}
		if input.Limit != nil {
			limit = int(*input.Limit)
		}
		page, err := reader.GetManagedReviewPage(ctx, s.generationOwner(), input.ReviewID, cursor, limit)
		if err != nil {
			return generationFailure(version, request, err)
		}
		if page.ReviewID != input.ReviewID || len(page.Cases) > limit || !protocol.ValidManagedReviewPageV16(page) {
			return handled(protocol.Failure(version, request, "SERVICE_UNHEALTHY", "managed review page is invalid", true))
		}
		return handled(protocol.Success(version, request, page))
	}
	if request.Method == "managedTests/reviews/apply" {
		apply, ok := s.generationBackend.(ManagedApplyBackend)
		if !ok || !apply.ManagedApplyReady() {
			return handled(protocol.Failure(version, request, "PROTOCOL_FEATURE_UNAVAILABLE", "managed review apply is not available", false))
		}
		input, err := decodeStrict[generationv16.ManagedReviewApplyRequestV16](request.Payload)
		if err != nil || !protocol.ValidManagedReviewApplyV16(input, input.ReviewID, input.ReviewDigest) {
			return invalidPayload(version, request)
		}
		resolutions := make(map[string]managedtest.ConflictChoice, len(input.Resolutions))
		for _, item := range input.Resolutions {
			resolutions[item.CaseID] = managedtest.ConflictChoice(item.Choice)
		}
		run, err := provider.ApplyManagedReview(ctx, managedtest.ApplyRequest{Owner: s.generationOwner(), ReviewID: input.ReviewID, ReviewDigest: input.ReviewDigest, Resolutions: resolutions})
		if err != nil {
			return generationFailure(version, request, err)
		}
		if run.State != testgendomain.StateAccepted || run.Request.SessionOwnerDigest != s.generationOwner() {
			return handled(protocol.Failure(version, request, "SERVICE_UNHEALTHY", "managed review outcome is not confirmed", true))
		}
		return handled(protocol.Success(version, request, generationv16.ManagedReviewApplyResultV16{Applied: true, ReviewID: input.ReviewID, ReviewDigest: input.ReviewDigest}))
	}
	return handled(protocol.Failure(version, request, "PROTOCOL_FEATURE_UNAVAILABLE", "managed review lifecycle is not available", false))
}
