package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionManagedSelectionStoreFixture struct{ run testgendomain.Run }

func (fixture productionManagedSelectionStoreFixture) GetGeneration(context.Context, string) (testgendomain.Run, error) {
	return testgendomain.CloneRun(fixture.run), nil
}

type productionManagedSelectionResolverFixture struct{ target generationTarget }

func (fixture productionManagedSelectionResolverFixture) ResolveRequest(context.Context, testgendomain.Request) (generationTarget, error) {
	return fixture.target, nil
}

func TestProductionManagedSelectionAuthorityResolvesCurrentFileAndFunctionCoverage(t *testing.T) {
	for _, id := range productionToolchainIdentityTestIDs()[:3] {
		t.Run(id, func(t *testing.T) {
			run, index, _, _, target, _ := productionManagedFixture(t)
			index.ToolchainID, target.toolchainID = id, id
			metric := coveragedomain.Summary{
				Functions: coveragedomain.Metric{Covered: 0, Total: 1},
				Lines:     coveragedomain.Metric{Covered: 2, Total: 4},
				Branches:  coveragedomain.Metric{Covered: 1, Total: 2},
			}
			index.Project.Summary = metric
			index.Files[0].Summary = metric
			index.Files[0].Functions[0].Summary = metric
			rootPath := t.TempDir()
			if err := os.MkdirAll(filepath.Join(rootPath, "src"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(rootPath, "src", "classify.cpp"), []byte("int classify(int value) { return value; }\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			root, err := workspace.OpenRoot(rootPath)
			if err != nil {
				t.Fatal(err)
			}
			authority, err := newProductionManagedSelectionAuthority(productionManagedSelectionAuthorityConfig{
				root: root, store: productionManagedSelectionStoreFixture{run: run},
				resolver: productionManagedSelectionResolverFixture{target: target}, current: productionManagedIndexFixture{index: index},
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := authority.Resolve(context.Background(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantSource, err := testgenvalidate.WorkspaceSnapshotDigest(root.NativePath)
			if err != nil {
				t.Fatal(err)
			}
			wantCoverage := testgenvalidate.SelectedCoverage{
				Project: metric,
				Files:   []testgenvalidate.SelectedFileCoverage{{ID: index.Files[0].ID, Summary: metric}},
				Functions: []testgenvalidate.SelectedFunctionCoverage{{
					ID: index.Files[0].Functions[0].ID, FileID: index.Files[0].ID, Summary: metric,
				}},
			}
			if got.SnapshotDigest != run.Record.SnapshotDigest || got.ToolchainID != id || got.SourceDigest != wantSource ||
				!reflect.DeepEqual(got.Baseline, wantCoverage) || len(got.PreviousReceipt) != 0 {
				t.Fatalf("context=%+v wantCoverage=%+v", got, wantCoverage)
			}
		})
	}
}

func TestProductionManagedSelectionRejectsSecondReadIdentityDrift(t *testing.T) {
	resolver, contextAuthority, input, selected := productionResolverFixture(t)
	secondID := "Other.Clang_1"
	contextAuthority.context.snapshot.Toolchains = append(contextAuthority.context.snapshot.Toolchains, toolchain.Instance{ID: secondID, Family: toolchain.FamilyClangCL})
	target, err := resolver.ResolveManagedStart(context.Background(), strings.Repeat("9", 64), input, selected)
	if err != nil {
		t.Fatal(err)
	}
	run, _, _, _, _, _ := productionManagedFixture(t)
	run.Request = target.request
	run.Record = testgendomain.NewGenerationRecord(run.Request)
	run.Record.MinimizedCaseIDs = []string{run.ID}
	if err := testgendomain.ValidateRun(run); err != nil {
		t.Fatalf("invalid declared run fixture: %v", err)
	}
	current := contextAuthority.context.index
	current.Files = append([]coveragedetail.File(nil), current.Files...)
	for i := range current.Files {
		current.Files[i].Functions = append([]coveragedetail.Function(nil), current.Files[i].Functions...)
	}
	metric := coveragedomain.Summary{Functions: coveragedomain.Metric{Total: 2}, Lines: coveragedomain.Metric{Total: 2}}
	current.Project.Summary, current.Files[0].Summary = metric, metric
	for i := range current.Files[0].Functions {
		current.Files[0].Functions[i].Summary = coveragedomain.Summary{Functions: coveragedomain.Metric{Total: 1}, Lines: coveragedomain.Metric{Total: 1}}
	}
	authority, err := newProductionManagedSelectionAuthority(productionManagedSelectionAuthorityConfig{
		root: resolver.config.root, store: productionManagedSelectionStoreFixture{run: run},
		resolver: resolver, current: productionManagedIndexFixture{index: current},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := authority.Resolve(context.Background(), run.ID)
	if err != nil || got.ToolchainID != target.toolchainID {
		t.Fatalf("matching second read failed: %v", err)
	}
	current.ToolchainID = secondID
	authority.config.current = productionManagedIndexFixture{index: current}
	_, err = authority.Resolve(context.Background(), run.ID)
	if !errors.Is(err, errProductionManagedUnavailable) {
		t.Fatalf("different valid second-read ID accepted: %v", err)
	}
}

func TestProductionManagedSelectionAuthorityFailsClosedOnDrift(t *testing.T) {
	run, index, _, _, target, _ := productionManagedFixture(t)
	metric := coveragedomain.Summary{Functions: coveragedomain.Metric{Total: 1}, Lines: coveragedomain.Metric{Total: 1}}
	index.Project.Summary, index.Files[0].Summary, index.Files[0].Functions[0].Summary = metric, metric, metric
	rootPath := t.TempDir()
	root, err := workspace.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*testgendomain.Run, *coveragedetail.Index, *generationTarget){
		"run state": func(run *testgendomain.Run, _ *coveragedetail.Index, _ *generationTarget) {
			run.State = testgendomain.StateAccepted
		},
		"snapshot": func(run *testgendomain.Run, _ *coveragedetail.Index, _ *generationTarget) {
			run.Record.SnapshotDigest = productionBytesDigest([]byte("stale"))
		},
		"toolchain": func(_ *testgendomain.Run, index *coveragedetail.Index, _ *generationTarget) { index.ToolchainID = "" },
		"coverage identity": func(_ *testgendomain.Run, index *coveragedetail.Index, _ *generationTarget) {
			index.ReportID = productionBytesDigest([]byte("wrong"))[:32]
		},
		"resolved request": func(_ *testgendomain.Run, _ *coveragedetail.Index, target *generationTarget) {
			target.request.SourceDigest = productionBytesDigest([]byte("wrong"))
		},
		"file status": func(_ *testgendomain.Run, index *coveragedetail.Index, _ *generationTarget) {
			index.Files[0].Status = coveragedetail.StatusStale
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidateRun, candidateIndex, candidateTarget := testgendomain.CloneRun(run), index, target
			candidateIndex.Files = append([]coveragedetail.File(nil), index.Files...)
			candidateIndex.Files[0].Functions = append([]coveragedetail.Function(nil), index.Files[0].Functions...)
			mutate(&candidateRun, &candidateIndex, &candidateTarget)
			authority, err := newProductionManagedSelectionAuthority(productionManagedSelectionAuthorityConfig{
				root: root, store: productionManagedSelectionStoreFixture{run: candidateRun},
				resolver: productionManagedSelectionResolverFixture{target: candidateTarget}, current: productionManagedIndexFixture{index: candidateIndex},
			})
			if err != nil {
				t.Fatal(err)
			}
			if value, err := authority.Resolve(context.Background(), run.ID); err == nil || !reflect.DeepEqual(value, testgenvalidate.SelectionContext{}) {
				t.Fatalf("context=%+v error=%v", value, err)
			}
		})
	}
}
