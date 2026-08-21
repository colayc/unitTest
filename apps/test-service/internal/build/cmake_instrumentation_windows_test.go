//go:build windows

package build

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragellvm"
)

func TestPlannerAcceptsRetainedCoverageInstrumentation(t *testing.T) {
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	instrumentationRoot := filepath.Join(fixture.dataRoot, "retained-instrumentation")
	makeOwnerOnlyPlannerInstrumentationRoot(t, instrumentationRoot)
	instrumentation, err := coveragellvm.WriteInstrumentation(instrumentationRoot)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(PlanInput{
		Installation: fixture.installation, WorkspaceRoot: fixture.root,
		Project: fixture.project, Profile: fixture.profile,
		Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
		Coverage: &CoverageOptions{
			BinaryDir: filepath.Join(fixture.dataRoot, "coverage-build"),
			TopLevelInclude: cmake.FingerprintFile{
				Path: instrumentation.IncludePath, Identity: instrumentation.Fingerprint, SHA256: instrumentation.SHA256,
			},
			InstrumentationFingerprint: instrumentation.Fingerprint,
		},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v, want retained instrumentation grammar accepted", err)
	}
	if !slices.ContainsFunc(plan.Steps[0].Process.LaunchInputs, func(input cmake.FingerprintFile) bool {
		return input.Path == instrumentation.IncludePath && input.SHA256 == instrumentation.SHA256
	}) {
		t.Fatalf("LaunchInputs = %#v, want retained instrumentation snapshot", plan.Steps[0].Process.LaunchInputs)
	}
}

func makeOwnerOnlyPlannerInstrumentationRoot(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		t.Fatal("current Windows user SID is unavailable")
	}
	sid := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString(
		fmt.Sprintf("O:%sD:P(A;OICI;GA;;;%s)", sid, sid),
	)
	if err != nil {
		t.Fatal(err)
	}
	attributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.CreateDirectory(pointer, attributes); err != nil {
		t.Fatal(err)
	}
}
