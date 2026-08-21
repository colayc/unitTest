//go:build windows

package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	"unit-test-ide.local/test-service/internal/task"
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

func TestPlannerRejectsUntrustedCompileAndLinkOptionDeclarations(t *testing.T) {
	activatePlannerWFPRegistration(t)
	for _, test := range []struct {
		name     string
		contents string
	}{
		{
			name: "copied retained options in project source",
			contents: "project(fixture LANGUAGES CXX)\n" +
				"add_compile_options(\"$<$<COMPILE_LANGUAGE:C,CXX>:-fprofile-instr-generate>\" \"$<$<COMPILE_LANGUAGE:C,CXX>:-fcoverage-mapping>\")\n" +
				"add_link_options(\"-fprofile-instr-generate\")\n",
		},
		{
			name:     "clang plugin",
			contents: "project(fixture LANGUAGES CXX)\nadd_compile_options(\"/clang:-fplugin=unknown.dll\")\n",
		},
		{
			name:     "pass plugin",
			contents: "project(fixture LANGUAGES CXX)\nadd_compile_options(\"-fpass-plugin=unknown.dll\")\n",
		},
		{
			name:     "linker plugin",
			contents: "project(fixture LANGUAGES CXX)\nadd_link_options(\"-Wl,-plugin,unknown.dll\")\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Plan(PlanInput{
				Installation: fixture.installation, WorkspaceRoot: fixture.root,
				Project: fixture.project, Profile: fixture.profile,
				Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
			}); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want untrusted option declaration rejected", err)
			}
		})
	}
}

func TestPlannerRejectsModifiedCoverageInstrumentationOptions(t *testing.T) {
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	instrumentationRoot := filepath.Join(fixture.dataRoot, "modified-instrumentation")
	if err := os.Mkdir(instrumentationRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	include := filepath.Join(instrumentationRoot, "coverage-instrumentation.cmake")
	contents := "add_compile_options(\"-fpass-plugin=unknown.dll\")\nadd_link_options(\"-Wl,-plugin,unknown.dll\")\n"
	if err := os.WriteFile(include, []byte(contents), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := Plan(PlanInput{
		Installation: fixture.installation, WorkspaceRoot: fixture.root,
		Project: fixture.project, Profile: fixture.profile,
		Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
		Coverage: &CoverageOptions{
			BinaryDir: filepath.Join(fixture.dataRoot, "coverage-build"),
			TopLevelInclude: cmake.FingerprintFile{
				Path: include, Identity: coveragellvm.InstrumentationFingerprint(), SHA256: coveragellvm.InstrumentationSHA256(),
			},
			InstrumentationFingerprint: coveragellvm.InstrumentationFingerprint(),
		},
	}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("Plan() error = %v, want modified instrumentation options rejected", err)
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
