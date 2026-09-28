package session

import (
	"context"
	"encoding/json"
	"math"

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
		if run.ProjectID != start.ProjectID || run.WorkspaceGeneration != start.WorkspaceGeneration || !validID(run.RunID) || !validID(run.TaskID) {
			return handled(protocol.Failure(version, request, "SERVICE_UNHEALTHY", "managed generation run is invalid", true))
		}
		return handled(protocol.Success(version, request, run))
	}
	// Candidate generation is deliberately not wired to this read-only route.
	return handled(protocol.Failure(version, request, "PROTOCOL_FEATURE_UNAVAILABLE", "managed generation ID resolution is not available", false))
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
