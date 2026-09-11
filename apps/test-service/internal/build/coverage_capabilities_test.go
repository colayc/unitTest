//go:build windows

package build

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"unit-test-ide.local/test-service/internal/coverageplatform"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestCoverageCapabilityViewsAreIndependentAndNonOwning(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	identity := strings.Repeat("a", 64)
	boundary := preparedCoverageBoundary(t, fixture, identity, "views")
	defer boundary.Release()

	source := boundary.coverageSourceRoot()
	objects := boundary.coverageObjectDirectory()
	if source == nil || objects == nil || source.Verify() != nil || objects.Verify() != nil {
		t.Fatalf("coverage views are not independently valid: source=%v objects=%v", source, objects)
	}
	if source.Path() != fixture.root.NativePath || objects.Path() != boundary.coverageDirectory.path {
		t.Fatalf("coverage view paths = %q, %q", source.Path(), objects.Path())
	}
	if closer, ok := source.(interface{ Close() error }); ok {
		t.Fatalf("source view leaked ownership through Close: %#v", closer)
	}
}

func TestCoverageCapabilityViewMintsIndependentRetainedClone(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	boundary := preparedCoverageBoundary(t, fixture, strings.Repeat("a", 64), "retained-view")
	view := boundary.coverageSourceRoot()
	retained, err := coverageplatform.RetainDirectory(view)
	if err != nil {
		_ = boundary.Release()
		t.Fatalf("RetainDirectory() = %v", err)
	}
	if retained.Path() != view.Path() || retained.Verify() != nil {
		_ = boundary.Release()
		t.Fatalf("retained clone is invalid: path=%q err=%v", retained.Path(), retained.Verify())
	}
	if err := boundary.Release(); err != nil {
		t.Fatal(err)
	}
	if view.Verify() == nil {
		t.Fatal("non-owning view remained valid after boundary release")
	}
	if err := retained.Verify(); err != nil {
		t.Fatalf("retained clone depended on closed boundary: %v", err)
	}
	if closer, ok := retained.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCoverageCapabilityViewsRejectClosedOrReplacedDirectories(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	identity := strings.Repeat("a", 64)
	boundary := preparedCoverageBoundary(t, fixture, identity, "replaced")
	source := boundary.coverageSourceRoot()
	objects := boundary.coverageObjectDirectory()
	objectPath := objects.Path()
	replacement := objectPath + "-old"
	if err := os.Rename(objectPath, replacement); err == nil {
		if err := os.Mkdir(objectPath, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := objects.Verify(); err == nil {
			t.Fatal("object capability accepted a replacement directory")
		}
	} else if err := objects.Verify(); err != nil {
		t.Fatalf("blocked replacement damaged object capability: %v", err)
	}
	if err := boundary.Release(); err != nil {
		t.Fatal(err)
	}
	if err := source.Verify(); err == nil {
		t.Fatal("workspace capability remained valid after boundary release")
	}
	if err := objects.Verify(); err == nil {
		t.Fatal("object capability remained valid after boundary release")
	}
}

func TestCoverageCapabilityViewVerifySerializesWithBoundaryRelease(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	boundary := preparedCoverageBoundary(t, fixture, strings.Repeat("a", 64), "concurrent")
	view := boundary.coverageSourceRoot()
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		for range 100 {
			_ = view.Verify()
		}
	}()
	go func() {
		defer group.Done()
		<-start
		_ = boundary.Release()
	}()
	close(start)
	group.Wait()
}

func TestCoverageAttachmentFailureLeavesCallerCapabilitiesOpen(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	boundary, err := newExecutionBoundary(fixture.installation, fixture.root, fixture.dataRoot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer boundary.Release()
	toolset := capabilityToolset{version: "20.1.8", identity: strings.Repeat("a", 64), tools: []coveragerun.TrustedPath{
		capabilityPath{path: fixture.toolchain.CCompiler},
	}}
	if err := boundary.attachCoverageToolset(&toolset); err == nil {
		t.Fatal("attachment without a coverage plan succeeded")
	}
	if toolset.closed != 0 || toolset.claimed {
		t.Fatalf("failed attachment consumed caller toolset: closed=%d claimed=%v", toolset.closed, toolset.claimed)
	}
}

func TestCoverageBoundaryRejectsTypedNilCollectorCapability(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	boundary, err := newExecutionBoundary(fixture.installation, fixture.root, fixture.dataRoot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer boundary.Release()
	var collector *capabilityCollector
	if err := boundary.AttachCoverageExecution(collector); err == nil {
		t.Fatal("typed-nil collector capability was accepted")
	}
}

func TestCoverageBoundaryReleasesCollectorBeforeToolset(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	identity := strings.Repeat("a", 64)
	boundary := preparedCoverageBoundary(t, fixture, identity, "release-order")
	order := []string{}
	toolset := capabilityToolset{
		version: "20.1.8", identity: identity,
		tools:      []coveragerun.TrustedPath{capabilityPath{path: fixture.toolchain.CCompiler}},
		closeOrder: &order,
	}
	collector := &capabilityCollector{
		spec:       task.ProcessSpec{Executable: fixture.installation.Executable, Dir: fixture.root.NativePath},
		closeOrder: &order,
	}
	if err := boundary.attachCoverageToolset(&toolset); err != nil {
		t.Fatalf("AttachCoverageToolset() = %v", err)
	}
	if err := boundary.AttachCoverageExecution(collector); err != nil {
		t.Fatalf("AttachCoverageExecution() = %v", err)
	}
	if err := boundary.Release(); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "collector" || order[1] != "toolset" {
		t.Fatalf("coverage release order = %v, want collector then toolset", order)
	}
}

func TestCoverageCapabilityAttachmentRejectsMismatchedCompilers(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	identity := strings.Repeat("a", 64)
	boundary := preparedCoverageBoundary(t, fixture, identity, "mismatch")
	defer boundary.Release()
	instance := fixture.toolchain
	instance.Version = "20.1.8"
	plan := &PreparedPlan{prepared: &preparedBuild{
		boundary:  boundary,
		coverage:  &CoverageOptions{ToolsetIdentity: identity},
		toolchain: instance,
	}}
	toolset := capabilityToolset{
		version: "20.1.8", identity: identity,
		c:     capabilityPath{path: filepath.Join(fixture.dataRoot(), "wrong-c")},
		cxx:   capabilityPath{path: fixture.toolchain.CXXCompiler},
		tools: []coveragerun.TrustedPath{capabilityPath{path: fixture.toolchain.CXXCompiler}},
	}
	if err := plan.AttachCoverageToolset(&toolset); err == nil {
		t.Fatal("mismatched C compiler was accepted")
	}
	if toolset.closed != 0 || toolset.claimed {
		t.Fatalf("failed attachment consumed mismatched toolset: closed=%d claimed=%v", toolset.closed, toolset.claimed)
	}
}

func TestCoveragePreparedPlanRejectsCXXVersionAndIdentityMismatches(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	identity := strings.Repeat("a", 64)
	for _, test := range []struct {
		name   string
		mutate func(*capabilityToolset)
	}{
		{"CXX compiler", func(toolset *capabilityToolset) {
			toolset.cxx = capabilityPath{path: filepath.Join(fixture.dataRoot(), "wrong-cxx")}
		}},
		{"version", func(toolset *capabilityToolset) { toolset.version = "19.0.0" }},
		{"identity", func(toolset *capabilityToolset) { toolset.identity = strings.Repeat("b", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			boundary := preparedCoverageBoundary(t, fixture, identity, strings.ReplaceAll(test.name, " ", "-"))
			defer boundary.Release()
			instance := fixture.toolchain
			instance.Version = "20.1.8"
			plan := &PreparedPlan{prepared: &preparedBuild{
				boundary: boundary, coverage: &CoverageOptions{ToolsetIdentity: identity}, toolchain: instance,
			}}
			toolset := &capabilityToolset{
				version: "20.1.8", identity: identity,
				c: capabilityPath{path: fixture.toolchain.CCompiler}, cxx: capabilityPath{path: fixture.toolchain.CXXCompiler},
				tools: []coveragerun.TrustedPath{capabilityPath{path: fixture.toolchain.CCompiler}},
			}
			test.mutate(toolset)
			if err := plan.AttachCoverageToolset(toolset); err == nil {
				t.Fatal("mismatched toolset was accepted")
			}
			if toolset.closed != 0 || toolset.claimed {
				t.Fatalf("failed attachment consumed caller toolset: closed=%d claimed=%v", toolset.closed, toolset.claimed)
			}
		})
	}
}

func TestCoverageAttachmentRejectsCollectorWithoutCompleteCoverageBuild(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	boundary, err := newExecutionBoundary(fixture.installation, fixture.root, fixture.dataRoot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer boundary.Release()
	collector := &capabilityCollector{spec: task.ProcessSpec{Executable: fixture.installation.Executable, Dir: fixture.root.NativePath}}
	if err := boundary.AttachCoverageExecution(collector); err == nil {
		t.Fatal("collector attached without a complete coverage build")
	}
	if collector.closed {
		t.Fatal("failed collector attachment closed caller-owned collector")
	}
}

func TestCoverageFamilySwitchKeepsOrdinaryGCCBuildsButRejectsIncompleteCoverage(t *testing.T) {
	gcc := toolchain.Instance{Family: toolchain.FamilyGCC}
	if _, err := coverageToolsetIdentity(gcc, false); err != nil {
		t.Fatalf("ordinary GCC build capability = %v", err)
	}
	if _, err := coverageToolsetIdentity(gcc, true); err == nil {
		t.Fatal("incomplete GCC coverage capability was accepted")
	}
}

type capabilityPath struct {
	path string
	err  error
}

func (path capabilityPath) Path() string  { return path.path }
func (path capabilityPath) Verify() error { return path.err }

type capabilityClaim struct{ toolset *capabilityToolset }

func (*capabilityClaim) Commit() {}
func (claim *capabilityClaim) Rollback() {
	if claim != nil && claim.toolset != nil {
		claim.toolset.claimed = false
	}
}

type capabilityToolset struct {
	version, identity string
	c, cxx            coveragerun.TrustedPath
	tools             []coveragerun.TrustedPath
	claimed, valid    bool
	closed            int
	closeOrder        *[]string
}

func (toolset *capabilityToolset) Version() string  { return toolset.version }
func (toolset *capabilityToolset) Identity() string { return toolset.identity }
func (toolset *capabilityToolset) CCompiler() coveragerun.TrustedPath {
	if toolset.c != nil {
		return toolset.c
	}
	return capabilityPath{path: toolset.tools[0].Path()}
}
func (toolset *capabilityToolset) CXXCompiler() coveragerun.TrustedPath {
	if toolset.cxx != nil {
		return toolset.cxx
	}
	return capabilityPath{path: toolset.tools[0].Path()}
}
func (toolset *capabilityToolset) Tools() []coveragerun.TrustedPath {
	return append([]coveragerun.TrustedPath(nil), toolset.tools...)
}
func (toolset *capabilityToolset) Verify() error {
	if toolset == nil || toolset.closed != 0 {
		return errors.New("closed")
	}
	return nil
}
func (toolset *capabilityToolset) ClaimOwnership() (coverageplatform.OwnershipClaim, error) {
	if toolset == nil || toolset.claimed || toolset.closed != 0 {
		return nil, errors.New("already claimed")
	}
	toolset.claimed = true
	return &capabilityClaim{toolset: toolset}, nil
}
func (toolset *capabilityToolset) Close() error {
	toolset.closed++
	if toolset.closeOrder != nil {
		*toolset.closeOrder = append(*toolset.closeOrder, "toolset")
	}
	return nil
}

type capabilityCollector struct {
	spec       task.ProcessSpec
	closed     bool
	closeOrder *[]string
}

func (collector *capabilityCollector) ProcessSpec() task.ProcessSpec { return collector.spec }
func (collector *capabilityCollector) Verify() error {
	if collector == nil || collector.closed {
		return errors.New("closed")
	}
	return nil
}
func (collector *capabilityCollector) VerifyAfter() error { return collector.Verify() }
func (collector *capabilityCollector) ValidateProcessTarget(executable string, args, env, unset []string, directory string) error {
	if collector.Verify() != nil || executable != collector.spec.Executable || directory != collector.spec.Dir {
		return errors.New("invalid target")
	}
	return nil
}
func (*capabilityCollector) PinnedOutput() (coverageplatform.Output, error) {
	return nil, errors.New("not ready")
}
func (collector *capabilityCollector) Close() error {
	collector.closed = true
	if collector.closeOrder != nil {
		*collector.closeOrder = append(*collector.closeOrder, "collector")
	}
	return nil
}

var _ coverageplatform.Toolset = (*capabilityToolset)(nil)
var _ coverageplatform.CollectorExecution = (*capabilityCollector)(nil)
var _ coveragerun.TrustedPath = capabilityPath{}
var _ = task.ErrInvalidArgument
