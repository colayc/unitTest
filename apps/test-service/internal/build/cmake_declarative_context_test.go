package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
)

func TestPlannerWindowsDeclarativeLiteralVariablePaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, syntax := range []struct{ name, pattern string }{
		{"escaped dollar", `\%s`},
		{"quoted escaped dollar", `"\%s"`},
		{"bracket", `[[%s]]`},
		{"equals bracket", `[=[%s]=]`},
	} {
		for _, consumer := range []struct{ name, command, value string }{
			{"executable source", "add_executable(check %s)", "${CMAKE_CURRENT_SOURCE_DIR}/extra.cpp"},
			{"library source", "add_library(check STATIC %s)", "${CMAKE_CURRENT_SOURCE_DIR}/extra.cpp"},
			{"target source", "target_sources(core PRIVATE %s)", "${CMAKE_CURRENT_SOURCE_DIR}/extra.cpp"},
			{"include directory", "target_include_directories(core PRIVATE %s)", "${CMAKE_CURRENT_SOURCE_DIR}/include"},
			{"include file", "include(%s)", "${CMAKE_CURRENT_SOURCE_DIR}/sdk/empty.cmake"},
			{"subdirectory source", "add_subdirectory(%s)", "${CMAKE_CURRENT_SOURCE_DIR}/child"},
			{"subdirectory explicit source", "add_subdirectory(%s child-output)", "${CMAKE_CURRENT_SOURCE_DIR}/child"},
			{"subdirectory binary", "add_subdirectory(child %s)", "${CMAKE_CURRENT_BINARY_DIR}/child-output"},
			{"set script loader", "set(CMAKE_PROJECT_INCLUDE %s)", "${CMAKE_CURRENT_SOURCE_DIR}/sdk/empty.cmake"},
			{"list script loader", "list(APPEND CMAKE_PROJECT_INCLUDE %s)", "${CMAKE_CURRENT_SOURCE_DIR}/sdk/empty.cmake"},
		} {
			t.Run(syntax.name+"/"+consumer.name, func(t *testing.T) {
				fixture := newPlannerFixture(t)
				command := fmt.Sprintf(consumer.command, fmt.Sprintf(syntax.pattern, consumer.value))
				writeDeclarativeFixture(t, fixture, map[string]string{
					"CMakeLists.txt": "add_library(core STATIC main.cpp)\n" + command + "\n",
					"extra.cpp":      "int extra() { return 1; }\n", "include/value.h": "#define VALUE 1\n",
					"sdk/empty.cmake": "message(empty)\n", "child/CMakeLists.txt": "message(child)\n",
				})
				if _, err := planDeclarativeFixture(fixture); !errors.Is(err, task.ErrInvalidArgument) {
					t.Fatalf("literal variable path accepted for %s: %v", command, err)
				}
			})
		}
	}
}

func TestPlannerWindowsDeclarativeSupportedPathSyntax(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production launch declaration")
	}
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	writeDeclarativeFixture(t, fixture, map[string]string{
		"CMakeLists.txt": `add_library(core STATIC [[main.cpp]])
target_sources(core PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/extra.cpp")
target_include_directories(core PUBLIC [[include]])
include("${CMAKE_CURRENT_SOURCE_DIR}/sdk/empty.cmake")
add_subdirectory([[child]] "${CMAKE_CURRENT_BINARY_DIR}/child-output")
`,
		"extra.cpp": "int extra() { return 1; }\n", "include/value.h": "#define VALUE 1\n",
		"sdk/empty.cmake": "message(empty)\n", "child/CMakeLists.txt": "message(child)\n",
	})
	if _, err := planDeclarativeFixture(fixture); err != nil {
		t.Fatalf("supported literal/quoted paths rejected: %v", err)
	}
}

func TestPlannerWindowsDeclarativeChildCMakeAliasRetarget(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production launch declaration")
	}
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	writeDeclarativeFixture(t, fixture, map[string]string{
		"CMakeLists.txt":     "add_subdirectory(child)\n",
		"child/first.cmake":  "message(first)\n",
		"child/second.cmake": "message(second)\n",
	})
	alias := filepath.Join(fixture.sourceDir, "child", "CMakeLists.txt")
	if err := os.Symlink(filepath.Join(fixture.sourceDir, "child", "first.cmake"), alias); err != nil {
		t.Fatal(err)
	}
	plan, firstErr := planDeclarativeFixture(fixture)
	if !errors.Is(firstErr, task.ErrInvalidArgument) {
		t.Errorf("child CMakeLists alias accepted: %v", firstErr)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fixture.sourceDir, "child", "second.cmake"), alias); err != nil {
		t.Fatal(err)
	}
	// Before the fix the evidence names first.cmake, whose identity and digest
	// remain valid even after CMakeLists.txt points at different declarations.
	if firstErr == nil {
		for _, input := range plan.Steps[0].Process.LaunchInputs {
			if filepath.Base(filepath.FromSlash(input.Path)) == "first.cmake" {
				if err := cmake.VerifyLaunchInput(input, maxCMakeLaunchFileBytes); err != nil {
					t.Fatal(err)
				}
				t.Log("retargeted alias left the captured target evidence unchanged")
			}
		}
	}
	if _, err := planDeclarativeFixture(fixture); !errors.Is(err, task.ErrInvalidArgument) {
		t.Errorf("retargeted child CMakeLists alias accepted: %v", err)
	}
}

func TestPlannerWindowsDeclarativeChildCMakeReplacement(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production launch declaration")
	}
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	writeDeclarativeFixture(t, fixture, map[string]string{
		"CMakeLists.txt":       "add_subdirectory(child)\n",
		"child/CMakeLists.txt": "message(first)\n",
		"child/second.cmake":   "message(second)\n",
	})
	plan, err := planDeclarativeFixture(fixture)
	if err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(fixture.sourceDir, "child", "CMakeLists.txt")
	index := slices.IndexFunc(plan.Steps[0].Process.LaunchInputs, func(input cmake.FingerprintFile) bool {
		return cmakeLaunchPathKey(filepath.FromSlash(input.Path)) == cmakeLaunchPathKey(child)
	})
	if index < 0 {
		t.Fatal("direct child CMakeLists.txt not captured")
	}
	state := plan.Steps[0].Process.LaunchInputs[index]
	if err := cmake.VerifyLaunchInput(state, maxCMakeLaunchFileBytes); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fixture.sourceDir, "child", "second.cmake"), child); err != nil {
		t.Fatal(err)
	}
	if err := cmake.VerifyLaunchInput(state, maxCMakeLaunchFileBytes); err == nil {
		t.Fatal("child replaced with alias accepted by snapshot verification")
	}
	if _, err := planDeclarativeFixture(fixture); !errors.Is(err, task.ErrInvalidArgument) {
		t.Errorf("child replaced with alias accepted by Plan: %v", err)
	}
}

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
