package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

type productionValidationPlannerFixture struct {
	candidateID string
	coverage    []byte
	fail        testgenvalidate.Stage
	stages      []testgenvalidate.Stage
}

func (planner *productionValidationPlannerFixture) Execute(_ context.Context, stage testgenvalidate.Stage, _ testgenvalidate.Roots) (testgenvalidate.StageEvidence, error) {
	planner.stages = append(planner.stages, stage)
	if stage == planner.fail {
		return testgenvalidate.StageEvidence{ExitCode: 1}, nil
	}
	if stage == testgenvalidate.StageDiscover {
		return testgenvalidate.StageEvidence{DiscoveredCaseIDs: []string{planner.candidateID}}, nil
	}
	if stage == testgenvalidate.StageCoverage {
		return testgenvalidate.StageEvidence{CoverageJSON: planner.coverage}, nil
	}
	return testgenvalidate.StageEvidence{}, nil
}

type productionValidationAuthorityFixture struct {
	ready       bool
	source      string
	fingerprint string
	baseline    []byte
	planner     *productionValidationPlannerFixture
	binding     productionValidationBinding
	verify      int
}

func (authority *productionValidationAuthorityFixture) Ready() bool { return authority.ready }

func (authority *productionValidationAuthorityFixture) Prepare(_ context.Context, binding productionValidationBinding) (productionValidationInput, error) {
	authority.binding = binding
	authority.planner.candidateID = binding.ValidationID
	return productionValidationInput{BaselineCoverage: append([]byte(nil), authority.baseline...), Metrics: testgenvalidate.Metrics{Lines: true}}, nil
}

func (authority *productionValidationAuthorityFixture) Verify(_ context.Context, binding productionValidationBinding, receipts []testgenvalidate.StageReceipt) error {
	authority.verify++
	if !reflect.DeepEqual(binding, authority.binding) || len(receipts) != 6 {
		return errors.New("validation binding changed")
	}
	return nil
}

func (authority *productionValidationAuthorityFixture) resolve(_ context.Context, candidateID string) (testgenvalidate.ResolvedCandidate, error) {
	if candidateID != authority.binding.ValidationID {
		return testgenvalidate.ResolvedCandidate{}, errors.New("unknown candidate")
	}
	return testgenvalidate.ResolvedCandidate{
		ID: candidateID, Kind: authority.binding.Kind, TargetSymbol: authority.binding.TargetSymbol,
		TargetFileURI: "source.c", TargetLines: []int64{1},
		BaselineSHA256:       digestRuntimeValidation(authority.baseline),
		SourceSnapshotDigest: authority.fingerprint,
		AssertionDigest:      authority.binding.AssertionDigest,
	}, nil
}

func (authority *productionValidationAuthorityFixture) evidence(_ context.Context, candidateID string, evidence testgenvalidate.AssertionEvidence) bool {
	return candidateID == authority.binding.ValidationID && evidence.Kind == authority.binding.Kind &&
		evidence.IndependentProofDigest == authority.binding.AssertionDigest && evidence.ObservedOutputReceipt == ""
}

type productionGenerationArtifactFixture struct {
	body    []byte
	value   task.Artifact
	corrupt bool
}

func (store *productionGenerationArtifactFixture) CommitGenerationSource(_ context.Context, taskID, artifactID string, at time.Time, source []byte) (task.Artifact, error) {
	store.body = append([]byte(nil), source...)
	store.value = task.Artifact{ID: artifactID, TaskID: taskID, Kind: "test-generation-source", RelativePath: "artifacts/" + artifactID + ".source", MIMEType: "application/octet-stream", SHA256: digestRuntimeValidation(source), Size: int64(len(source)), CreatedAt: at}
	return store.value, nil
}

func (store *productionGenerationArtifactFixture) VerifyGenerationSource(_ context.Context, artifact task.Artifact) error {
	if store.corrupt || artifact != store.value || digestRuntimeValidation(store.body) != artifact.SHA256 {
		return errors.New("artifact changed")
	}
	return nil
}

func digestRuntimeValidation(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fingerprintRuntimeValidation(t *testing.T, root string) string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || path == root || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[strings.ToLower(filepath.ToSlash(rel))] = digestRuntimeValidation(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(files)
	return digestRuntimeValidation(encoded)
}

func productionValidationCoverage(covered int64) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":"1.0","provenance":{"platform":"windows","architecture":"x64","compiler":{"family":"clang-cl","version":"22.1.8"},"driver":{"name":"llvm-cov","version":"22.1.8"},"collector":{"name":"llvm-cov","version":"22.1.8"},"normalizerVersion":"1","instrumentationFingerprint":"%s"},"completeness":{"outcome":"available","reasons":[]},"summary":{"functions":{"covered":0,"total":0},"lines":{"covered":%d,"total":1},"branches":{"covered":0,"total":0}},"files":[{"uri":"source.c","sha256":"%s","summary":{"functions":{"covered":0,"total":0},"lines":{"covered":%d,"total":1},"branches":{"covered":0,"total":0}},"lines":[{"line":1,"count":%d,"branches":{"covered":0,"total":0}}]}]}`, strings.Repeat("a", 64), covered, strings.Repeat("b", 64), covered, covered))
}

func productionValidationFixture(t *testing.T) (*productionGenerationValidation, testgendomain.Run, generationTarget, productionPipelineResult, *productionValidationAuthorityFixture, *productionGenerationArtifactFixture) {
	t.Helper()
	pipeline, target, _, _ := productionPipelineFixture(t)
	result, err := pipeline.Generate(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "tests"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "source.c"), []byte("int classify(int x) { return x < 3; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "tests", "CMakeLists.txt"), []byte(target.renderTarget.ExistingCMake), 0600); err != nil {
		t.Fatal(err)
	}
	planner := &productionValidationPlannerFixture{coverage: productionValidationCoverage(1)}
	authority := &productionValidationAuthorityFixture{ready: true, source: source, fingerprint: fingerprintRuntimeValidation(t, source), baseline: productionValidationCoverage(0), planner: planner}
	validator := testgenvalidate.Validator{Config: testgenvalidate.Config{
		SourceRoot: source, TempRoot: t.TempDir(), Planner: planner,
		VerifyEvidence: authority.evidence, ResolveCandidate: authority.resolve,
	}}
	artifacts := &productionGenerationArtifactFixture{}
	adapter := newProductionGenerationValidation(validator, authority, artifacts)
	run := testgendomain.Run{ID: strings.Repeat("1", 32), TaskID: strings.Repeat("2", 32), Request: target.request, CreatedAt: time.Unix(100, 0).UTC()}
	return adapter, run, target, result, authority, artifacts
}

func TestProductionGenerationValidationRetainsOnlyExecutedCoverageGain(t *testing.T) {
	adapter, run, target, pipeline, authority, artifacts := productionValidationFixture(t)
	result, err := adapter.Validate(context.Background(), run, target, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	if result.Next != testgendomain.StateMinimizing || len(result.Candidates) != 1 || len(result.Artifacts) != 1 {
		t.Fatalf("validation result=%+v", result)
	}
	candidate := result.Candidates[0]
	if err := testgendomain.ValidateCandidate(candidate); err != nil {
		t.Fatalf("invalid candidate: %+v: %v", candidate, err)
	}
	if candidate.CoverageDelta.Lines != 1 || candidate.BaselineCoverage.LinePercent != 0 || candidate.DeltaCoveragePercent.LinePercent != 100 {
		t.Fatalf("coverage not projected: %+v", candidate)
	}
	if len(authority.planner.stages) != 6 || authority.planner.stages[0] != testgenvalidate.StageConfigure || authority.planner.stages[5] != testgenvalidate.StageCoverage {
		t.Fatalf("stages=%v", authority.planner.stages)
	}
	if _, err := os.Stat(filepath.Join(authority.source, "tests", "generated", "classify_test.cpp")); !os.IsNotExist(err) {
		t.Fatalf("validator wrote workspace: %v", err)
	}
	cmake, _ := os.ReadFile(filepath.Join(authority.source, "tests", "CMakeLists.txt"))
	if string(cmake) != target.renderTarget.ExistingCMake {
		t.Fatalf("workspace cmake changed: %q", cmake)
	}
	if len(artifacts.body) == 0 || artifacts.value.SHA256 != candidate.StagedSourceArtifact.Digest {
		t.Fatalf("artifact not bound: %+v", artifacts.value)
	}
	set, err := adapter.CandidateSet(context.Background(), run, result.Candidates)
	if err != nil || !reflect.DeepEqual(set.Files, pipeline.editSet.Files) || !reflect.DeepEqual(set.CaseIDs, []string{candidate.CaseID}) {
		t.Fatalf("candidate set mismatch: %+v %v", set, err)
	}
	set.Files[0].Content[0] ^= 0xff
	set.CaseIDs[0] = strings.Repeat("f", 32)
	stable, err := adapter.CandidateSet(context.Background(), run, result.Candidates)
	if err != nil || !reflect.DeepEqual(stable.Files, pipeline.editSet.Files) || !reflect.DeepEqual(stable.CaseIDs, []string{candidate.CaseID}) {
		t.Fatalf("caller mutated authoritative set: %+v %v", stable, err)
	}
	minimized, err := adapter.Minimize(context.Background(), run, target, pipeline)
	if err != nil || minimized.Next != testgendomain.StateAwaitingConfirmation || !reflect.DeepEqual(minimized.MinimizedCaseIDs, []string{candidate.CaseID}) || minimized.PreviewSet == nil {
		t.Fatalf("minimize result=%+v err=%v", minimized, err)
	}
	if err := adapter.ValidateCandidate(context.Background(), run, candidate); err != nil || authority.verify != 4 {
		t.Fatalf("candidate receipt not reverified: %v count=%d", err, authority.verify)
	}
	artifacts.corrupt = true
	if err := adapter.ValidateCandidate(context.Background(), run, candidate); err == nil {
		t.Fatal("changed source artifact accepted")
	}
}

func TestProductionGenerationValidationFailureRetainsNothing(t *testing.T) {
	adapter, run, target, pipeline, authority, artifacts := productionValidationFixture(t)
	authority.planner.fail = testgenvalidate.StageCompile
	result, err := adapter.Validate(context.Background(), run, target, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	if result.Next != testgendomain.StateRejected || len(result.Candidates) != 0 || len(result.Artifacts) != 0 || len(artifacts.body) != 0 {
		t.Fatalf("failed validation retained data: %+v", result)
	}
}
