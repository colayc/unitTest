package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

type productionSnapshotResolverFixture struct {
	target generationTarget
	err    error
}

func (fixture *productionSnapshotResolverFixture) ResolveRequest(context.Context, testgendomain.Request) (generationTarget, error) {
	return fixture.target, fixture.err
}

type productionArtifactVerifierFixture struct {
	source, evidence int
	err              error
}

func (fixture *productionArtifactVerifierFixture) VerifyGenerationSource(context.Context, task.Artifact) error {
	fixture.source++
	return fixture.err
}

func (fixture *productionArtifactVerifierFixture) VerifyGenerationEvidence(context.Context, task.Artifact) error {
	fixture.evidence++
	return fixture.err
}

type productionLeaseReaderFixture struct {
	leases []task.ProcessLease
	err    error
}

func (fixture productionLeaseReaderFixture) ActiveLeases(context.Context) ([]task.ProcessLease, error) {
	return append([]task.ProcessLease(nil), fixture.leases...), fixture.err
}

func TestProductionGenerationSnapshotAuthorityReattestsExactRequestAndDigest(t *testing.T) {
	request := productionGenerationRequestFixture()
	resolver := &productionSnapshotResolverFixture{target: generationTarget{request: request}}
	authority, err := newProductionGenerationSnapshotAuthority(resolver)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := authority.VerifyRequest(context.Background(), request)
	if err != nil || identity != request.SnapshotIdentity() {
		t.Fatalf("VerifyRequest() identity=%+v error=%v", identity, err)
	}
	digest := testgendomain.NewGenerationRecord(request).SnapshotDigest
	if err := authority.VerifyDigest(context.Background(), digest); err != nil {
		t.Fatalf("VerifyDigest() error=%v", err)
	}

	resolver.target.request.SourceDigest = strings.Repeat("f", 64)
	if _, err := authority.VerifyRequest(context.Background(), request); err == nil {
		t.Fatal("VerifyRequest() accepted a resolver/request mismatch")
	}
	if err := authority.VerifyDigest(context.Background(), digest); err == nil {
		t.Fatal("VerifyDigest() accepted a changed current request")
	}
}

func TestProductionGenerationSnapshotAuthorityRejectsUnknownOrConflictingDigest(t *testing.T) {
	request := productionGenerationRequestFixture()
	resolver := &productionSnapshotResolverFixture{target: generationTarget{request: request}}
	authority, err := newProductionGenerationSnapshotAuthority(resolver)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.VerifyDigest(context.Background(), strings.Repeat("e", 64)); err == nil {
		t.Fatal("VerifyDigest() accepted an unregistered digest")
	}
	if _, err := authority.VerifyRequest(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	conflict := request
	conflict.IdempotencyKey = strings.Repeat("e", 32)
	// SnapshotDigest deliberately excludes the idempotency key. The authority
	// must still reject two distinct requests for the same snapshot identity.
	resolver.target.request = conflict
	if _, err := authority.VerifyRequest(context.Background(), conflict); err == nil {
		t.Fatal("VerifyRequest() accepted a conflicting request for one digest")
	}
}

func TestProductionGenerationArtifactAuthorityRoutesOnlyKnownImmutableKinds(t *testing.T) {
	verifier := &productionArtifactVerifierFixture{}
	authority, err := newProductionGenerationArtifactAuthority(verifier)
	if err != nil {
		t.Fatal(err)
	}
	source := task.Artifact{Kind: "test-generation-source"}
	evidence := task.Artifact{Kind: "test-generation-evidence"}
	if err := authority.Verify(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if err := authority.Verify(context.Background(), evidence); err != nil {
		t.Fatal(err)
	}
	if verifier.source != 1 || verifier.evidence != 1 {
		t.Fatalf("source=%d evidence=%d", verifier.source, verifier.evidence)
	}
	if err := authority.Verify(context.Background(), task.Artifact{Kind: "coverage-json"}); err == nil {
		t.Fatal("Verify() accepted a non-generation artifact")
	}
	verifier.err = errors.New("changed")
	if err := authority.Verify(context.Background(), source); err == nil {
		t.Fatal("Verify() hid artifact verification failure")
	}
}

func TestProductionGenerationProcessAuthorityRequiresExactOwnerAndNoLease(t *testing.T) {
	serviceID := strings.Repeat("1", 32)
	taskID := strings.Repeat("2", 32)
	authority, err := newProductionGenerationProcessAuthority(productionLeaseReaderFixture{}, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if authority.OwnerDigest() != productionBytesDigest([]byte(serviceID)) {
		t.Fatalf("OwnerDigest()=%q", authority.OwnerDigest())
	}
	if err := authority.Verify(context.Background(), taskID, authority.OwnerDigest()); err != nil {
		t.Fatalf("Verify() error=%v", err)
	}
	if err := authority.Verify(context.Background(), taskID, strings.Repeat("3", 64)); err == nil {
		t.Fatal("Verify() accepted a foreign owner")
	}
	active, err := newProductionGenerationProcessAuthority(productionLeaseReaderFixture{leases: []task.ProcessLease{{TaskID: taskID}}}, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := active.Verify(context.Background(), taskID, active.OwnerDigest()); err == nil {
		t.Fatal("Verify() accepted an active task lease")
	}
	unrelated, err := newProductionGenerationProcessAuthority(productionLeaseReaderFixture{leases: []task.ProcessLease{{TaskID: strings.Repeat("4", 32)}}}, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := unrelated.Verify(context.Background(), taskID, unrelated.OwnerDigest()); err != nil {
		t.Fatalf("Verify() rejected an unrelated lease: %v", err)
	}
}

func TestProductionGenerationAuthoritiesRejectIncompleteConstruction(t *testing.T) {
	if authority, err := newProductionGenerationSnapshotAuthority(nil); err == nil || authority != nil {
		t.Fatalf("snapshot authority=%#v error=%v", authority, err)
	}
	if authority, err := newProductionGenerationArtifactAuthority(nil); err == nil || authority != nil {
		t.Fatalf("artifact authority=%#v error=%v", authority, err)
	}
	if authority, err := newProductionGenerationProcessAuthority(nil, strings.Repeat("1", 32)); err == nil || authority != nil {
		t.Fatalf("process authority=%#v error=%v", authority, err)
	}
}

func productionGenerationRequestFixture() testgendomain.Request {
	digest := strings.Repeat("a", 64)
	return testgendomain.Request{
		IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: digest, ProjectID: "core",
		Scope: testgendomain.ScopeFile, CoverageReportID: strings.Repeat("2", 32), ManagedTargetID: strings.Repeat("3", 32),
		Framework: testgendomain.FrameworkUnity, Goals: testgendomain.Goals{FunctionPercent: 1, LinePercent: 1, BranchPercent: 1},
		Budgets:               testgendomain.Budgets{WallTimeMS: 60000, CandidateCount: 1, MemoryMiB: 64, Concurrency: 1},
		CompileSnapshotDigest: digest, CoverageSnapshotDigest: digest, SourceDigest: digest, CMakeTargetDigest: digest,
		FrameworkBundleDigest: digest, AnalyzerBundleDigest: digest, BaselineReportDigest: digest,
		ProcessOwnerDigest: digest, SessionOwnerDigest: strings.Repeat("b", 64),
	}
}
