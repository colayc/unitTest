package runtime

import (
	"bytes"
	"errors"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
)

var errProductionManagedUnavailable = errors.New("production managed test generation is unavailable")

// productionManagedReviewInput contains only data re-read from trusted service
// stores. Current is the exact rooted workspace preimage; Set is reconstructed
// from Task 5 evidence rather than accepted from the protocol client.
type productionManagedReviewInput struct {
	Run              testgendomain.Run
	Index            coveragedetail.Index
	Set              testgenpublish.CandidateSet
	SourceArtifactID string
	Current          []byte
	Accepted         []managedtest.Record
	AcceptedBlocks   map[string][]byte
}

func productionManagedSource(set testgenpublish.CandidateSet) (string, []byte, bool) {
	path := ""
	var content []byte
	for _, file := range set.Files {
		if !strings.HasSuffix(file.Path, "_test.c") && !strings.HasSuffix(file.Path, "_test.cpp") {
			continue
		}
		if path != "" || !managedtest.ValidTestPath(file.Path) || len(file.Content) == 0 || productionBytesDigest(file.Content) != file.AfterDigest {
			return "", nil, false
		}
		path = file.Path
		content = append([]byte(nil), file.Content...)
	}
	return path, content, path != ""
}

func cloneAcceptedBlocks(input map[string][]byte) map[string][]byte {
	if input == nil {
		return nil
	}
	result := make(map[string][]byte, len(input))
	for id, block := range input {
		result[id] = append([]byte(nil), block...)
	}
	return result
}

func cloneProductionBytes(input []byte) []byte {
	result := make([]byte, len(input))
	copy(result, input)
	return result
}

func managedReviewStatus(operation managedtest.Operation) (managedtest.Status, bool) {
	switch operation.Kind {
	case managedtest.OperationAdd, managedtest.OperationUpdate, managedtest.OperationUnchanged:
		return managedtest.StatusCurrent, !operation.Conflict
	case managedtest.OperationConflict:
		return managedtest.StatusConflicted, operation.Conflict
	case managedtest.OperationOrphan:
		return managedtest.StatusOrphaned, operation.Conflict
	default:
		return managedtest.StatusInvalid, false
	}
}

// buildProductionManagedReview turns the exact validated managed source into a
// durable review. It never writes the workspace. The same ReconcileInput is
// retained for the later selected-output publication plan.
func buildProductionManagedReview(input productionManagedReviewInput) (managedtest.ReviewDraft, []managedtest.ReconcileInput, error) {
	run := input.Run
	if testgendomain.ValidateRun(run) != nil || run.State != testgendomain.StateAwaitingConfirmation || run.Request.Scope != testgendomain.ScopeCoverageGap || run.Request.ManagedGapID == "" ||
		input.Set.RunID != run.ID || input.Set.SnapshotDigest != testgendomain.NewGenerationRecord(run.Request).SnapshotDigest || input.Set.Managed != nil || len(input.Set.CaseIDs) != 1 ||
		!validProductionObjectID(input.SourceArtifactID) || input.Index.ToolchainID == "" {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	target, err := testgendomain.ResolveManagedTarget(testgendomain.ManagedSelector{
		ProjectID: run.Request.ProjectID, WorkspaceGeneration: run.Request.WorkspaceGeneration,
		CoverageReportID: run.Request.CoverageReportID, Scope: run.Request.Scope, ID: run.Request.ManagedGapID,
	}, input.Index)
	if err != nil || target.SourceDigest != run.Request.SourceDigest || target.FunctionID == "" || target.GapID != run.Request.ManagedGapID {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	path, generated, ok := productionManagedSource(input.Set)
	if !ok || !strings.HasPrefix(path, "tests/generated/"+target.File+"_test.") {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	artifactBound := false
	for _, artifact := range run.ArtifactDigests {
		if artifact.ID == input.SourceArtifactID && artifact.Digest == productionBytesDigest(generated) {
			artifactBound = true
			break
		}
	}
	if !artifactBound {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	generatedDocument, err := managedtest.ParseDocument(generated, managedtest.MaxReviewBytes, 200)
	if err != nil || len(generatedDocument.Blocks) == 0 {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	for _, block := range generatedDocument.Blocks {
		if block.FunctionID != target.FunctionID {
			return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
		}
	}
	currentDocument, err := managedtest.ParseDocument(input.Current, managedtest.MaxReviewBytes, 200)
	if err != nil {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	accepted := append([]managedtest.Record(nil), input.Accepted...)
	sort.Slice(accepted, func(left, right int) bool { return accepted[left].CaseID < accepted[right].CaseID })
	for _, record := range accepted {
		if !managedtest.ValidRecord(record) || record.ProjectID != run.Request.ProjectID || record.SourceFileID != target.FileID ||
			record.FunctionID != target.FunctionID || record.SourceRelativePath != target.File || record.TestRelativePath != path ||
			record.ToolchainID != input.Index.ToolchainID || record.SourceDigest != run.Request.SourceDigest || record.Status == managedtest.StatusInvalid {
			return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
		}
	}
	reconcileInput := managedtest.ReconcileInput{
		Accepted: accepted, AcceptedBlocks: cloneAcceptedBlocks(input.AcceptedBlocks), Current: currentDocument,
		Generated: managedtest.ManagedFile{Path: path, Content: cloneProductionBytes(generated)},
	}
	review, err := managedtest.Reconcile(reconcileInput)
	if err != nil || review.Path != path || review.PreimageDigest != productionBytesDigest(input.Current) || review.GeneratedDigest != productionBytesDigest(generated) {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	candidates := make([]managedtest.ReviewCandidate, 0, len(review.Operations))
	currentDigest, generatedDigest := productionBytesDigest(input.Current), productionBytesDigest(generated)
	for _, operation := range review.Operations {
		status, valid := managedReviewStatus(operation)
		if !valid {
			return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
		}
		candidates = append(candidates, managedtest.ReviewCandidate{
			CandidateID: operation.CaseID, TestRelativePath: path, Status: status, AcceptedDigest: operation.AcceptedDigest,
			CurrentDigest: currentDigest, GeneratedDigest: generatedDigest,
			CurrentBytes: cloneProductionBytes(input.Current), GeneratedBytes: cloneProductionBytes(generated),
		})
	}
	if len(candidates) == 0 {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	manifest := managedtest.ReviewManifest{
		ReviewID: run.ID, OwnerDigest: run.Request.SessionOwnerDigest, RunID: run.ID, RunRevision: run.Revision,
		ProjectID: run.Request.ProjectID, WorkspaceGeneration: run.Request.WorkspaceGeneration,
		ReportID: run.Request.CoverageReportID, ToolchainID: input.Index.ToolchainID, SourceDigest: run.Request.SourceDigest,
		ArtifactRef: "artifact:" + input.SourceArtifactID, CreatedAt: run.CreatedAt,
	}
	manifest.CandidateSetDigest = managedtest.ReviewCandidateSetDigest(candidates)
	manifest.CurrentPreimageDigest, manifest.GeneratedPreimageDigest = managedtest.ReviewPreimageSetDigests(candidates)
	draft := managedtest.ReviewDraft{Manifest: manifest, Candidates: candidates}
	if !managedtest.ValidReviewDraft(draft) || !bytes.Equal(reconcileInput.Current.Bytes, input.Current) || !bytes.Equal(reconcileInput.Generated.Content, generated) {
		return managedtest.ReviewDraft{}, nil, errProductionManagedUnavailable
	}
	return draft, []managedtest.ReconcileInput{reconcileInput}, nil
}
