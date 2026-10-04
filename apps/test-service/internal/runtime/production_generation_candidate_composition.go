package runtime

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgenanalysis"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

const productionGenerationAnalyzerVersion = "clang-ast-ir-v1"

// productionGenerationArtifactContent is the immutable content boundary owned
// by artifactstore. Artifact metadata remains authoritative in taskstore, so
// the composed adapter deliberately cannot invent or replace metadata rows.
type productionGenerationArtifactContent interface {
	CommitGenerationSource(context.Context, string, string, time.Time, []byte) (task.Artifact, error)
	VerifyGenerationSource(context.Context, task.Artifact) error
	ReadGenerationSource(context.Context, task.Artifact) ([]byte, error)
	CommitGenerationEvidence(context.Context, string, string, time.Time, []byte) (task.Artifact, error)
	VerifyGenerationEvidence(context.Context, task.Artifact) error
	ReadGenerationEvidence(context.Context, task.Artifact) ([]byte, error)
}

type runtimeProductionGenerationArtifacts struct {
	metadata *taskstore.Store
	content  productionGenerationArtifactContent
}

func (artifacts runtimeProductionGenerationArtifacts) CommitGenerationSource(ctx context.Context, taskID, artifactID string, at time.Time, source []byte) (task.Artifact, error) {
	return artifacts.content.CommitGenerationSource(ctx, taskID, artifactID, at, source)
}

func (artifacts runtimeProductionGenerationArtifacts) VerifyGenerationSource(ctx context.Context, artifact task.Artifact) error {
	return artifacts.content.VerifyGenerationSource(ctx, artifact)
}

func (artifacts runtimeProductionGenerationArtifacts) ReadGenerationSource(ctx context.Context, artifact task.Artifact) ([]byte, error) {
	return artifacts.content.ReadGenerationSource(ctx, artifact)
}

func (artifacts runtimeProductionGenerationArtifacts) CommitGenerationEvidence(ctx context.Context, taskID, artifactID string, at time.Time, evidence []byte) (task.Artifact, error) {
	return artifacts.content.CommitGenerationEvidence(ctx, taskID, artifactID, at, evidence)
}

func (artifacts runtimeProductionGenerationArtifacts) VerifyGenerationEvidence(ctx context.Context, artifact task.Artifact) error {
	return artifacts.content.VerifyGenerationEvidence(ctx, artifact)
}

func (artifacts runtimeProductionGenerationArtifacts) ReadGenerationEvidence(ctx context.Context, artifact task.Artifact) ([]byte, error) {
	return artifacts.content.ReadGenerationEvidence(ctx, artifact)
}

func (artifacts runtimeProductionGenerationArtifacts) GetArtifact(ctx context.Context, artifactID string) (task.Artifact, error) {
	return artifacts.metadata.GetArtifact(ctx, artifactID)
}

// productionGenerationCandidateComposition is the closed candidate-production
// half of the managed generation capability. It intentionally is not itself a
// runtime provider: selected-output revalidation and publication must be bound
// before the product capability gate may expose the driver.
type productionGenerationCandidateComposition struct {
	driver     *productionGenerationDriver
	resolver   *productionGenerationTargetResolver
	validation *productionGenerationValidation
	snapshots  *productionGenerationSnapshotAuthority
	artifacts  *productionGenerationArtifactAuthority
	processes  *productionGenerationProcessAuthority
}

func newProductionGenerationCandidateComposition(store *taskstore.Store, runtimeValue *Runtime) (*productionGenerationCandidateComposition, error) {
	if store == nil || runtimeValue == nil || runtimeValue.productBundles == nil || runtimeValue.productBundles.Testgen() == nil ||
		runtimeValue.productionBuilds == nil || runtimeValue.runner == nil || runtimeValue.probeRunner == nil || runtimeValue.artifacts == nil ||
		runtimeValue.workspaceRoot.NativePath == "" || runtimeValue.workspaceRoot.ID == "" || runtimeValue.controlDataRoot == "" ||
		!validProductionObjectID(runtimeValue.serviceInstanceID) || runtimeValue.platform != "windows" && runtimeValue.platform != "linux" {
		return nil, task.ErrStorageUnavailable
	}
	bundle := runtimeValue.productBundles.Testgen()
	if err := bundle.Verify(); err != nil || !validProductionDigest(bundle.ManifestSHA256()) {
		return nil, task.ErrStorageUnavailable
	}
	content, ok := runtimeValue.artifacts.(productionGenerationArtifactContent)
	if !ok || content == nil {
		return nil, task.ErrStorageUnavailable
	}
	validationRoot := filepath.Join(runtimeValue.controlDataRoot, "test-generation-validation")
	if !filepath.IsAbs(validationRoot) || filepath.Clean(validationRoot) != validationRoot || os.MkdirAll(validationRoot, 0o700) != nil ||
		os.Chmod(validationRoot, 0o700) != nil || validateDirectDirectory(validationRoot) != nil {
		return nil, task.ErrStorageUnavailable
	}

	analyzer, err := testgenanalysis.NewAnalyzerWithVerifiedBundle(bundle)
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	ctestRunner, err := ctest.NewRunner(runtimeValue.installation)
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	unityDigest, err := productionCanonicalDigest("generation-framework-adapter-v1", string(testgendomain.FrameworkUnity))
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	cppUTestDigest, err := productionCanonicalDigest("generation-framework-adapter-v1", string(testgendomain.FrameworkCppUTest))
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	layout, err := newCTestProductionTestLayoutAuthority(productionTestLayoutConfig{
		root: runtimeValue.workspaceRoot,
		ctest: productionCTestReader{
			runner:   ctestRunner,
			executor: ctestProbeExecutor{runner: runtimeValue.probeRunner},
		},
		frameworkDigests: map[testgendomain.Framework]string{
			testgendomain.FrameworkUnity:    unityDigest,
			testgendomain.FrameworkCppUTest: cppUTestDigest,
		},
	})
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}

	resolverAuthority := runtimeProductionResolverAuthority{reader: runtimeValue}
	processes, err := newProductionGenerationProcessAuthority(store, runtimeValue.serviceInstanceID)
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	resolver, err := newProductionGenerationTargetResolver(productionTargetResolverConfig{
		root:                 runtimeValue.workspaceRoot,
		authority:            resolverAuthority,
		layout:               layout,
		analyzerBundleDigest: bundle.ManifestSHA256(),
		analyzerVersion:      productionGenerationAnalyzerVersion,
		processOwnerDigest:   processes.OwnerDigest(),
	})
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	snapshots, err := newProductionGenerationSnapshotAuthority(resolver)
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}

	generationArtifacts := runtimeProductionGenerationArtifacts{metadata: store, content: content}
	artifactAuthority, err := newProductionGenerationArtifactAuthority(generationArtifacts)
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	baseline, err := newRuntimeCoverageBaselineReader(store, runtimeValue.artifacts)
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	validationAuthority, err := newRuntimeProductionValidationAuthority(productionValidationAuthorityConfig{
		root: runtimeValue.workspaceRoot, resolver: resolverAuthority, baseline: baseline,
	})
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	builds, err := newProductionBuildAuthority(runtimeValue.productionBuilds)
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	compiler, err := newProductionValidationNativeCompiler(productionValidationNativeCompilerConfig{
		installation: runtimeValue.installation, platform: runtimeValue.platform,
		serviceInstanceID: runtimeValue.serviceInstanceID, leases: store,
	})
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	plans, err := newProductionValidationPlanRegistry(compiler)
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	executor, err := newProductionValidationStageExecutor(runtimeValue.runner, plans)
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	validation := newProductionGenerationValidation(testgenvalidate.Validator{Config: testgenvalidate.Config{
		SourceRoot: runtimeValue.workspaceRoot.NativePath, TempRoot: validationRoot, Planner: executor,
		VerifyEvidence: validationAuthority.VerifyEvidence, ResolveCandidate: validationAuthority.ResolveCandidate,
	}}, validationAuthority, generationArtifacts)
	if err := validation.bindProductionPlans(builds, plans); err != nil || !validation.Ready() {
		return nil, task.ErrStorageUnavailable
	}

	pipeline := newProductionGenerationPipeline(analyzer, productionStaticOracle{})
	driver := newProductionGenerationDriver(resolver, pipeline, validation)
	return &productionGenerationCandidateComposition{
		driver: driver, resolver: resolver, validation: validation,
		snapshots: snapshots, artifacts: artifactAuthority, processes: processes,
	}, nil
}

var _ productionGenerationArtifactStore = runtimeProductionGenerationArtifacts{}
var _ productionGenerationArtifactVerifier = runtimeProductionGenerationArtifacts{}
