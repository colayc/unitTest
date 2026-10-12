package runtime

import (
	"context"
	"reflect"
	"sync"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

type productionGenerationSnapshotResolver interface {
	ResolveRequest(context.Context, testgendomain.Request) (generationTarget, error)
}

// productionGenerationSnapshotAuthority shares one exact request registry
// between the coordinator and publisher. The coordinator registers only a
// request that the live production resolver reconstructs byte-for-byte; the
// publisher can then re-resolve the same request from its closed digest.
type productionGenerationSnapshotAuthority struct {
	resolver productionGenerationSnapshotResolver
	mu       sync.Mutex
	requests map[string]testgendomain.Request
}

func newProductionGenerationSnapshotAuthority(resolver productionGenerationSnapshotResolver) (*productionGenerationSnapshotAuthority, error) {
	if resolver == nil {
		return nil, task.ErrStorageUnavailable
	}
	return &productionGenerationSnapshotAuthority{resolver: resolver, requests: make(map[string]testgendomain.Request)}, nil
}

func (authority *productionGenerationSnapshotAuthority) VerifyRequest(ctx context.Context, request testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
	if authority == nil || authority.resolver == nil || ctx == nil || ctx.Err() != nil || testgendomain.ValidateRequest(request) != nil {
		return testgendomain.SnapshotIdentity{}, testgendomain.ErrStaleSnapshot
	}
	target, err := authority.resolver.ResolveRequest(ctx, request)
	if err != nil || !reflect.DeepEqual(target.request, request) {
		return testgendomain.SnapshotIdentity{}, testgendomain.ErrStaleSnapshot
	}
	digest := testgendomain.NewGenerationRecord(request).SnapshotDigest
	if !validProductionDigest(digest) {
		return testgendomain.SnapshotIdentity{}, testgendomain.ErrStaleSnapshot
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if current, exists := authority.requests[digest]; exists && !reflect.DeepEqual(current, request) {
		return testgendomain.SnapshotIdentity{}, testgendomain.ErrStaleSnapshot
	}
	authority.requests[digest] = request
	return request.SnapshotIdentity(), nil
}

func (authority *productionGenerationSnapshotAuthority) VerifyDigest(ctx context.Context, digest string) error {
	if authority == nil || authority.resolver == nil || ctx == nil || ctx.Err() != nil || !validProductionDigest(digest) {
		return testgendomain.ErrStaleSnapshot
	}
	authority.mu.Lock()
	request, exists := authority.requests[digest]
	authority.mu.Unlock()
	if !exists {
		return testgendomain.ErrStaleSnapshot
	}
	target, err := authority.resolver.ResolveRequest(ctx, request)
	if err != nil || !reflect.DeepEqual(target.request, request) || testgendomain.NewGenerationRecord(target.request).SnapshotDigest != digest {
		return testgendomain.ErrStaleSnapshot
	}
	return nil
}

type productionGenerationArtifactVerifier interface {
	VerifyGenerationSource(context.Context, task.Artifact) error
	VerifyGenerationEvidence(context.Context, task.Artifact) error
}

type productionGenerationArtifactAuthority struct {
	verifier productionGenerationArtifactVerifier
}

func newProductionGenerationArtifactAuthority(verifier productionGenerationArtifactVerifier) (*productionGenerationArtifactAuthority, error) {
	if verifier == nil {
		return nil, task.ErrStorageUnavailable
	}
	return &productionGenerationArtifactAuthority{verifier: verifier}, nil
}

func (authority *productionGenerationArtifactAuthority) Verify(ctx context.Context, artifact task.Artifact) error {
	if authority == nil || authority.verifier == nil || ctx == nil || ctx.Err() != nil {
		return task.ErrStorageUnavailable
	}
	switch artifact.Kind {
	case "test-generation-source":
		return authority.verifier.VerifyGenerationSource(ctx, artifact)
	case "test-generation-evidence":
		return authority.verifier.VerifyGenerationEvidence(ctx, artifact)
	default:
		return task.ErrConflict
	}
}

type productionGenerationLeaseReader interface {
	ActiveLeases(context.Context) ([]task.ProcessLease, error)
}

type productionGenerationProcessAuthority struct {
	leases      productionGenerationLeaseReader
	ownerDigest string
}

func newProductionGenerationProcessAuthority(leases productionGenerationLeaseReader, serviceInstanceID string) (*productionGenerationProcessAuthority, error) {
	if leases == nil || !validProductionObjectID(serviceInstanceID) {
		return nil, task.ErrStorageUnavailable
	}
	return &productionGenerationProcessAuthority{leases: leases, ownerDigest: productionBytesDigest([]byte(serviceInstanceID))}, nil
}

func (authority *productionGenerationProcessAuthority) OwnerDigest() string {
	if authority == nil {
		return ""
	}
	return authority.ownerDigest
}

func (authority *productionGenerationProcessAuthority) Verify(ctx context.Context, taskID, ownerDigest string) error {
	if authority == nil || authority.leases == nil || ctx == nil || ctx.Err() != nil || !validProductionObjectID(taskID) ||
		!validProductionDigest(ownerDigest) || ownerDigest != authority.ownerDigest {
		return task.ErrConflict
	}
	leases, err := authority.leases.ActiveLeases(ctx)
	if err != nil {
		return err
	}
	for _, lease := range leases {
		if lease.TaskID == taskID {
			return task.ErrConflict
		}
	}
	return nil
}
