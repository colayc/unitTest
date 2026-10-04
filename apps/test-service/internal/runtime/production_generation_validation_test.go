package runtime

import (
	"bytes"
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

	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

type productionValidationPlannerFixture struct {
	candidateID    string
	coverage       []byte
	fail           testgenvalidate.Stage
	stages         []testgenvalidate.Stage
	processTaskIDs []string
}

func (planner *productionValidationPlannerFixture) Execute(_ context.Context, stage testgenvalidate.Stage, roots testgenvalidate.Roots) (testgenvalidate.StageEvidence, error) {
	planner.stages = append(planner.stages, stage)
	planner.processTaskIDs = append(planner.processTaskIDs, roots.ProcessTaskID)
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
		TargetFileURI: "source.c", TargetLines: []int64{1}, TargetFunctionIDs: append([]string(nil), authority.binding.TargetFunctionIDs...),
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
	bodies  map[string][]byte
	values  map[string]task.Artifact
	corrupt map[string]bool
}

func (store *productionGenerationArtifactFixture) commit(taskID, artifactID, kind, suffix string, at time.Time, body []byte) (task.Artifact, error) {
	if store.bodies == nil {
		store.bodies, store.values, store.corrupt = map[string][]byte{}, map[string]task.Artifact{}, map[string]bool{}
	}
	store.bodies[artifactID] = append([]byte(nil), body...)
	value := task.Artifact{ID: artifactID, TaskID: taskID, Kind: kind, RelativePath: "artifacts/" + artifactID + suffix, MIMEType: "application/octet-stream", SHA256: digestRuntimeValidation(body), Size: int64(len(body)), CreatedAt: at}
	store.values[artifactID] = value
	return value, nil
}

func (store *productionGenerationArtifactFixture) CommitGenerationSource(_ context.Context, taskID, artifactID string, at time.Time, source []byte) (task.Artifact, error) {
	return store.commit(taskID, artifactID, "test-generation-source", ".source", at, source)
}

func (store *productionGenerationArtifactFixture) CommitGenerationEvidence(_ context.Context, taskID, artifactID string, at time.Time, evidence []byte) (task.Artifact, error) {
	return store.commit(taskID, artifactID, "test-generation-evidence", ".evidence", at, evidence)
}

func (store *productionGenerationArtifactFixture) verify(artifact task.Artifact, kind string) error {
	if store.corrupt[artifact.ID] || artifact != store.values[artifact.ID] || artifact.Kind != kind || digestRuntimeValidation(store.bodies[artifact.ID]) != artifact.SHA256 {
		return errors.New("artifact changed")
	}
	return nil
}

func (store *productionGenerationArtifactFixture) VerifyGenerationSource(_ context.Context, artifact task.Artifact) error {
	return store.verify(artifact, "test-generation-source")
}

func (store *productionGenerationArtifactFixture) VerifyGenerationEvidence(_ context.Context, artifact task.Artifact) error {
	return store.verify(artifact, "test-generation-evidence")
}

func (store *productionGenerationArtifactFixture) ReadGenerationSource(_ context.Context, artifact task.Artifact) ([]byte, error) {
	if err := store.verify(artifact, "test-generation-source"); err != nil {
		return nil, err
	}
	return append([]byte(nil), store.bodies[artifact.ID]...), nil
}

func (store *productionGenerationArtifactFixture) ReadGenerationEvidence(_ context.Context, artifact task.Artifact) ([]byte, error) {
	if err := store.verify(artifact, "test-generation-evidence"); err != nil {
		return nil, err
	}
	return append([]byte(nil), store.bodies[artifact.ID]...), nil
}

func (store *productionGenerationArtifactFixture) GetArtifact(_ context.Context, artifactID string) (task.Artifact, error) {
	value, ok := store.values[artifactID]
	if !ok {
		return task.Artifact{}, task.ErrNotFound
	}
	return value, nil
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
	if result.Next != testgendomain.StateMinimizing || len(result.Candidates) != 1 || len(result.Artifacts) != 2 {
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
	for _, taskID := range authority.planner.processTaskIDs {
		if taskID != run.TaskID {
			t.Fatalf("validation process task ID = %q, want %q", taskID, run.TaskID)
		}
	}
	if _, err := os.Stat(filepath.Join(authority.source, "tests", "generated", "classify_test.cpp")); !os.IsNotExist(err) {
		t.Fatalf("validator wrote workspace: %v", err)
	}
	cmake, _ := os.ReadFile(filepath.Join(authority.source, "tests", "CMakeLists.txt"))
	if string(cmake) != target.renderTarget.ExistingCMake {
		t.Fatalf("workspace cmake changed: %q", cmake)
	}
	if len(result.Artifacts) != 2 || len(artifacts.bodies[candidate.StagedSourceArtifact.ID]) == 0 || artifacts.values[candidate.StagedSourceArtifact.ID].SHA256 != candidate.StagedSourceArtifact.Digest {
		t.Fatalf("artifacts not bound: %+v", result.Artifacts)
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
	for _, artifact := range result.Artifacts {
		run.ArtifactDigests = append(run.ArtifactDigests, testgendomain.ArtifactRef{ID: artifact.ID, Digest: artifact.SHA256})
	}
	adapter.mu.Lock()
	delete(adapter.records, run.ID)
	adapter.mu.Unlock()
	restarted, err := adapter.CandidateSet(context.Background(), run, result.Candidates)
	if err != nil || !reflect.DeepEqual(restarted.Files, pipeline.editSet.Files) {
		t.Fatalf("durable validation did not survive restart: %+v %v", restarted, err)
	}
	artifacts.corrupt[candidate.CaseID] = true
	adapter.mu.Lock()
	delete(adapter.records, run.ID)
	adapter.mu.Unlock()
	if err := adapter.ValidateCandidate(context.Background(), run, candidate); err == nil {
		t.Fatal("changed evidence artifact accepted")
	}
}

func TestProductionValidationPersistsManagedCaseAndReceiptEvidence(t *testing.T) {
	adapter, run, _, _, _, artifacts := productionValidationFixture(t)
	pipeline, target, _, _ := productionPipelineFixture(t)
	target.managed = true
	target.sourceRelativePath = "src/classify.cpp"
	target.renderTarget.TestPath = "tests/generated/src/classify.cpp_test.cpp"
	result, err := pipeline.Generate(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	run.Request = target.request
	validated, err := adapter.Validate(context.Background(), run, target, result)
	if err != nil || len(validated.Candidates) != 1 {
		t.Fatalf("managed validation=%+v err=%v", validated, err)
	}
	for _, artifact := range validated.Artifacts {
		run.ArtifactDigests = append(run.ArtifactDigests, testgendomain.ArtifactRef{ID: artifact.ID, Digest: artifact.SHA256})
	}
	set, cases, receipt, digest, err := adapter.ManagedEvidence(context.Background(), run, validated.Candidates)
	if err != nil || len(cases) != len(result.vectors) || productionBytesDigest(receipt) != digest || len(receipt) == 0 {
		t.Fatalf("managed evidence cases=%+v receipt=%d digest=%s err=%v", cases, len(receipt), digest, err)
	}
	source, ok := exactGeneratedSource(set.Files)
	if !ok {
		t.Fatal("managed source missing from durable set")
	}
	document, err := managedtest.ParseDocument(source, int64(len(source)), 200)
	if err != nil {
		t.Fatal(err)
	}
	for index, item := range cases {
		if item.CaseID != document.Blocks[index].CaseID || item.FunctionID != document.Blocks[index].FunctionID || item.ScenarioID == "" || item.TestRelativePath != set.Files[0].Path {
			t.Fatalf("managed case %d = %+v block=%+v", index, item, document.Blocks[index])
		}
	}
	var persisted productionValidationEvidence
	if err := json.Unmarshal(artifacts.bodies[validated.Candidates[0].CaseID], &persisted); err != nil || !validProductionManagedCases(persisted.ManagedCases, persisted.Set, source) {
		t.Fatalf("invalid persisted managed evidence: %+v err=%v", persisted.ManagedCases, err)
	}
	if _, err := adapter.loadRecord(context.Background(), run, validated.Candidates[0]); err != nil {
		t.Fatalf("direct managed evidence reload: %v", err)
	}
	adapter.mu.Lock()
	adapter.records = make(map[string]productionValidatedRecord)
	adapter.mu.Unlock()
	_, replayed, replayReceipt, replayDigest, err := adapter.ManagedEvidence(context.Background(), run, validated.Candidates)
	if err != nil || !reflect.DeepEqual(replayed, cases) || !bytes.Equal(replayReceipt, receipt) || replayDigest != digest {
		t.Fatalf("replayed managed evidence cases=%+v digest=%s err=%v", replayed, replayDigest, err)
	}
}

func TestProductionValidationBindsEveryGeneratedFileFunction(t *testing.T) {
	adapter, run, _, _, authority, _ := productionValidationFixture(t)
	_, _, _, _, target, pipeline := productionManagedFileFixture(t)
	run.Request = target.request
	validated, err := adapter.Validate(context.Background(), run, target, pipeline)
	if err != nil || len(validated.Candidates) != 1 {
		t.Fatalf("file validation=%+v err=%v", validated, err)
	}
	want := []string{strings.Repeat("c", 64), strings.Repeat("e", 64)}
	if authority.binding.TargetSymbol != "file:"+target.fileID || !reflect.DeepEqual(authority.binding.TargetFunctionIDs, want) {
		t.Fatalf("binding=%+v want functions=%v", authority.binding, want)
	}
}

func TestProductionGenerationValidationFailureRetainsNothing(t *testing.T) {
	adapter, run, target, pipeline, authority, artifacts := productionValidationFixture(t)
	authority.planner.fail = testgenvalidate.StageCompile
	result, err := adapter.Validate(context.Background(), run, target, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	if result.Next != testgendomain.StateRejected || len(result.Candidates) != 0 || len(result.Artifacts) != 0 || len(artifacts.bodies) != 0 {
		t.Fatalf("failed validation retained data: %+v", result)
	}
}
