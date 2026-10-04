package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/discovery"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionCoverageBaselineFixture struct {
	data []byte
}

func (fixture *productionCoverageBaselineFixture) ReadCoverageBaseline(context.Context, string) ([]byte, error) {
	return append([]byte(nil), fixture.data...), nil
}

func TestProductionValidationAuthorityBindsCurrentBaselineWorkspaceAndTargetLines(t *testing.T) {
	authority, binding, baseline, root := productionValidationAuthorityTestFixture(t)

	input, err := authority.Prepare(context.Background(), binding)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !reflect.DeepEqual(input.BaselineCoverage, baseline.data) || input.Metrics != (testgenvalidate.Metrics{Lines: true}) {
		t.Fatalf("Prepare() = %+v", input)
	}
	resolved, err := authority.ResolveCandidate(context.Background(), binding.ValidationID)
	if err != nil {
		t.Fatalf("ResolveCandidate() error = %v", err)
	}
	wantLines := []int64{1, 2}
	if resolved.ID != binding.ValidationID || resolved.TargetFileURI != binding.SourceRelativePath ||
		!reflect.DeepEqual(resolved.TargetLines, wantLines) || !reflect.DeepEqual(resolved.TargetFunctionIDs, binding.TargetFunctionIDs) ||
		resolved.BaselineSHA256 != productionBytesDigest(baseline.data) || !validProductionDigest(resolved.SourceSnapshotDigest) {
		t.Fatalf("resolved candidate = %+v", resolved)
	}
	evidence := testgenvalidate.AssertionEvidence{Kind: binding.Kind, IndependentProofDigest: binding.AssertionDigest}
	if !authority.VerifyEvidence(context.Background(), binding.ValidationID, evidence) {
		t.Fatal("prepared assertion evidence was rejected")
	}

	receipts := productionValidationReceiptsFixture()
	if err := authority.Verify(context.Background(), binding, receipts); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root.NativePath, "src", "choose.c"), []byte("int choose(void) { return 99; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := authority.Verify(context.Background(), binding, receipts); err == nil {
		t.Fatal("Verify() accepted changed workspace bytes")
	}
}

func TestProductionValidationAuthorityRejectsStaleOrIncompleteBindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*productionValidationBinding, *productionResolverAuthorityFixture, *productionCoverageBaselineFixture)
	}{
		{"wrong report digest", func(binding *productionValidationBinding, _ *productionResolverAuthorityFixture, _ *productionCoverageBaselineFixture) {
			binding.BaselineReportDigest = strings.Repeat("0", 64)
		}},
		{"wrong source digest", func(binding *productionValidationBinding, _ *productionResolverAuthorityFixture, _ *productionCoverageBaselineFixture) {
			binding.SourceDigest = strings.Repeat("0", 64)
		}},
		{"missing function", func(binding *productionValidationBinding, _ *productionResolverAuthorityFixture, _ *productionCoverageBaselineFixture) {
			binding.CoverageFunctionIDs = binding.CoverageFunctionIDs[:1]
		}},
		{"baseline/index drift", func(_ *productionValidationBinding, resolver *productionResolverAuthorityFixture, _ *productionCoverageBaselineFixture) {
			resolver.context.index.Files[0].Lines[0].Count++
		}},
		{"baseline bytes drift", func(_ *productionValidationBinding, _ *productionResolverAuthorityFixture, baseline *productionCoverageBaselineFixture) {
			baseline.data = productionValidationAuthorityCoverage(strings.Repeat("d", 64), 1, 1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authority, binding, baseline, _ := productionValidationAuthorityTestFixture(t)
			test.mutate(&binding, authority.config.resolver.(*productionResolverAuthorityFixture), baseline)
			binding.ValidationID = ""
			binding.ValidationID = productionValidationDigest(binding)
			if _, err := authority.Prepare(context.Background(), binding); err == nil {
				t.Fatal("Prepare() error = nil")
			}
		})
	}
}

func productionValidationAuthorityTestFixture(t *testing.T) (*runtimeProductionValidationAuthority, productionValidationBinding, *productionCoverageBaselineFixture, workspace.Root) {
	t.Helper()
	root, target := productionCompileBindingFixture(t)
	source, err := os.ReadFile(target.CompileUnits[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	fileID := strings.Repeat("1", 32)
	coverageFunctionIDs := []string{strings.Repeat("2", 32), strings.Repeat("3", 32)}
	symbolIDs := []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}
	resolver := &productionResolverAuthorityFixture{context: productionResolverContext{
		snapshot: discovery.Snapshot{Generation: strings.Repeat("4", 64)},
		project:  workspace.ProjectConfig{ID: "core", SourceDir: "."},
		index: coveragedetail.Index{
			WorkspaceGeneration: strings.Repeat("4", 64), ProjectID: "core", ReportID: strings.Repeat("5", 32), ToolchainID: strings.Repeat("6", 64),
			Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent},
			Files: []coveragedetail.File{{
				ID: fileID, RelativePath: "src/choose.c", SourceSHA256: productionBytesDigest(source), Status: coveragedetail.StatusCurrent,
				Lines: []coveragedetail.Line{{Line: 1, Count: 0}, {Line: 2, Count: 1}},
				Functions: []coveragedetail.Function{
					{ID: coverageFunctionIDs[0], Status: coveragedetail.StatusCurrent, Lines: []coveragedetail.Line{{Line: 1, Count: 0}}},
					{ID: coverageFunctionIDs[1], Status: coveragedetail.StatusCurrent, Lines: []coveragedetail.Line{{Line: 2, Count: 1}}},
				},
			}},
		},
		baselineReportDigest: strings.Repeat("7", 64),
	}}
	baseline := &productionCoverageBaselineFixture{data: productionValidationAuthorityCoverage(productionBytesDigest(source), 0, 1)}
	authority, err := newRuntimeProductionValidationAuthority(productionValidationAuthorityConfig{
		root: root, resolver: resolver, baseline: baseline,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := productionValidationBinding{
		RunID: strings.Repeat("8", 32), TaskDigest: strings.Repeat("9", 64), SnapshotDigest: strings.Repeat("a", 64),
		EditDigest: strings.Repeat("b", 64), AssertionDigest: strings.Repeat("c", 64), TargetSymbol: "file:" + fileID,
		ProjectID: "core", WorkspaceGeneration: resolver.context.index.WorkspaceGeneration, CoverageReportID: resolver.context.index.ReportID,
		BaselineReportDigest: resolver.context.baselineReportDigest, SourceRelativePath: "src/choose.c", SourceDigest: productionBytesDigest(source),
		TargetFunctionIDs: symbolIDs, CoverageFunctionIDs: coverageFunctionIDs, Kind: testgendomain.KindVerified,
	}
	binding.ValidationID = productionValidationDigest(binding)
	return authority, binding, baseline, root
}

func productionValidationAuthorityCoverage(sourceDigest string, first, second int64) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":"1.0","provenance":{"platform":"windows","architecture":"x64","compiler":{"family":"clang-cl","version":"22.1.8"},"driver":{"name":"llvm-cov","version":"22.1.8"},"collector":{"name":"llvm-cov","version":"22.1.8"},"normalizerVersion":"1","instrumentationFingerprint":"%s"},"completeness":{"outcome":"available","reasons":[]},"summary":{"functions":{"covered":1,"total":2},"lines":{"covered":1,"total":2},"branches":{"covered":0,"total":0}},"files":[{"uri":"src/choose.c","sha256":"%s","summary":{"functions":{"covered":1,"total":2},"lines":{"covered":1,"total":2},"branches":{"covered":0,"total":0}},"lines":[{"line":1,"count":%d,"branches":{"covered":0,"total":0}},{"line":2,"count":%d,"branches":{"covered":0,"total":0}}]}]}`, strings.Repeat("e", 64), sourceDigest, first, second))
}

func productionValidationReceiptsFixture() []testgenvalidate.StageReceipt {
	stages := []testgenvalidate.Stage{testgenvalidate.StageConfigure, testgenvalidate.StageCompile, testgenvalidate.StageDiscover, testgenvalidate.StageCandidate, testgenvalidate.StageSuite, testgenvalidate.StageCoverage}
	result := make([]testgenvalidate.StageReceipt, 0, len(stages))
	for _, stage := range stages {
		result = append(result, testgenvalidate.StageReceipt{Stage: stage, Digest: strings.Repeat("1", 64), OutputDigest: strings.Repeat("2", 64), CoverageDigest: strings.Repeat("3", 64)})
	}
	return result
}
