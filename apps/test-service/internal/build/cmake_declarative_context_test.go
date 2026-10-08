package build

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
)

func writeDeclarativeFixture(t *testing.T, fixture plannerFixture, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(fixture.sourceDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func planDeclarativeFixture(fixture plannerFixture) (task.ExecutionPlan, error) {
	return Plan(PlanInput{Installation: fixture.installation, WorkspaceRoot: fixture.root,
		Project: fixture.project, Profile: fixture.profile, Toolchain: fixture.toolchain,
		Jobs: 1, Configure: true})
}

// Incorrect include-relative resolution, stale child contexts, and incorrect
// child output directories must all break this real production Plan test.
func TestCMakeDeclarativeContext(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production launch declaration")
	}
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	writeDeclarativeFixture(t, fixture, map[string]string{
		"CMakeLists.txt": `include(sdk/entry.cmake)
add_subdirectory(child child-output EXCLUDE_FROM_ALL)
add_executable(parent main.cpp)
target_sources(parent PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/extra.cpp)
add_test(NAME parent COMMAND parent)
`,
		"sdk/entry.cmake": `include(root.cmake)
include(${CMAKE_CURRENT_LIST_DIR}/local.cmake)
add_executable(from-include ${CMAKE_CURRENT_SOURCE_DIR}/main.cpp)
target_include_directories(from-include PUBLIC include ${CMAKE_CURRENT_LIST_DIR}/headers)
`,
		"root.cmake":          "message(root)\n",
		"sdk/local.cmake":     "message(local)\n",
		"sdk/headers/value.h": "#define LOCAL 1\n",
		"include/value.h":     "#define VALUE 1\n",
		"main.cpp":            "int main() { return 0; }\n",
		"extra.cpp":           "int extra() { return 1; }\n",
		"child/CMakeLists.txt": `add_executable(child child.cpp)
target_sources(child PRIVATE ${CMAKE_SOURCE_DIR}/extra.cpp ${CMAKE_CURRENT_LIST_DIR}/child.cpp)
target_include_directories(child PUBLIC ${CMAKE_BINARY_DIR} ${CMAKE_CURRENT_BINARY_DIR})
add_test(NAME child COMMAND child)
`,
		"child/child.cpp": "int main() { return 0; }\n",
	})
	if err := os.MkdirAll(filepath.Join(fixture.profile.BinaryDir, "child-output"), 0700); err != nil {
		t.Fatal(err)
	}
	plan, err := planDeclarativeFixture(fixture)
	if err != nil {
		t.Fatalf("directory declarations rejected: %v", err)
	}
	for _, executable := range []string{"parent.exe", "from-include.exe", "child-output/child.exe"} {
		if !slices.Contains(plan.Steps[0].Process.LaunchPlan, filepath.Join(fixture.profile.BinaryDir, filepath.FromSlash(executable))) {
			t.Fatalf("missing output %s in %#v", executable, plan.Steps[0].Process.LaunchPlan)
		}
	}
	inputs := plan.Steps[0].Process.LaunchInputs
	for _, relative := range []string{"main.cpp", "extra.cpp", "child/child.cpp", "root.cmake", "sdk/local.cmake"} {
		path := filepath.Join(fixture.sourceDir, filepath.FromSlash(relative))
		matches := 0
		for _, input := range inputs {
			if cmakeLaunchPathKey(filepath.FromSlash(input.Path)) == cmakeLaunchPathKey(path) {
				matches++
				if err := cmake.VerifyLaunchInput(input, maxCMakeLaunchFileBytes); err != nil {
					t.Fatal(err)
				}
			}
		}
		if matches != 1 {
			t.Fatalf("snapshot %s count = %d, want 1", relative, matches)
		}
	}
	for _, input := range inputs {
		if strings.Contains(input.Path, "headers") || strings.HasSuffix(input.Path, "/include") {
			t.Fatalf("directory snapshotted as file: %#v", input)
		}
	}
}

func TestPlannerWindowsDeclarativeInputReplacement(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production launch declaration")
	}
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	writeDeclarativeFixture(t, fixture, map[string]string{
		"CMakeLists.txt": "add_executable(check main.cpp)\ntarget_sources(check PRIVATE extra.cpp)\n",
		"main.cpp":       "int main() { return 0; }\n", "extra.cpp": "int extra() { return 1; }\n",
	})
	plan, err := planDeclarativeFixture(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"main.cpp", "extra.cpp"} {
		path := filepath.Join(fixture.sourceDir, relative)
		index := slices.IndexFunc(plan.Steps[0].Process.LaunchInputs, func(input cmake.FingerprintFile) bool {
			return cmakeLaunchPathKey(filepath.FromSlash(input.Path)) == cmakeLaunchPathKey(path)
		})
		if index < 0 {
			t.Fatalf("source input %s not captured", relative)
		}
		state := plan.Steps[0].Process.LaunchInputs[index]
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("// replaced input\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := cmake.VerifyLaunchInput(state, maxCMakeLaunchFileBytes); err == nil {
			t.Fatalf("replaced %s accepted", relative)
		}
	}
}
