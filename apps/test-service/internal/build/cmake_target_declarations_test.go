package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
)

func TestCMakeClosedMacro(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  bool
	}{
		{"FEATURE", true}, {"FEATURE=1", true}, {"FEATURE=-1", true}, {"FEATURE=VALUE", true},
		{"FEATURE=", false}, {"-DFEATURE=1", false}, {"FEATURE=\"x\"", false},
		{"FEATURE=1;BAD", false}, {"FEATURE=${OTHER}", false}, {"FEATURE=$<BOOL:1>", false},
		{"FEATURE=1\nBAD", false}, {"FEATURE=foo\\bar", false},
		{"FEATURE=+1", true}, {"_FEATURE_1=+0", true}, {"FEATURE=1.0", false}, {"1FEATURE", false}, {"FÉATURE=1", false},
	} {
		if got := validClosedCompileDefinition(tt.value); got != tt.want {
			t.Fatalf("definition %q = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestPlannerWindowsDeclarativeFileAPIOnlyTargetRejected(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, command := range []string{"target_sources(api PRIVATE main.cpp)", "target_include_directories(api PRIVATE include)", "target_compile_definitions(api PRIVATE FEATURE)", "target_link_libraries(api PRIVATE core)"} {
		t.Run(command, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			writeDeclarativeFixture(t, fixture, map[string]string{"CMakeLists.txt": "add_library(core STATIC main.cpp)\n" + command,
				"main.cpp": "int main() { return 0; }\n", "include/value.h": "#define VALUE 1\n"})
			_, err := Plan(PlanInput{Installation: fixture.installation, WorkspaceRoot: fixture.root, Project: fixture.project,
				Profile: fixture.profile, Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
				Targets: []cmake.Target{{ID: fixture.targetID, Name: "api", Type: "EXECUTABLE", Artifacts: []string{filepath.Join(fixture.profile.BinaryDir, "api.exe")}}}})
			if !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("File API-only declaration accepted: %v", err)
			}
		})
	}
}

func TestCMakeDeclarativeContextVariableSubdirectory(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production launch declaration")
	}
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	writeDeclarativeFixture(t, fixture, map[string]string{"CMakeLists.txt": "add_subdirectory(${CMAKE_CURRENT_SOURCE_DIR}/child)\n",
		"child/CMakeLists.txt": "add_executable(child child.cpp)\n", "child/child.cpp": "int main() { return 0; }\n"})
	plan, err := planDeclarativeFixture(fixture)
	if err != nil {
		t.Fatalf("variable subdirectory rejected: %v", err)
	}
	want := filepath.Join(fixture.profile.BinaryDir, "child", "child.exe")
	found := false
	for _, path := range plan.Steps[0].Process.LaunchPlan {
		if path == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("child output missing: %#v", plan.Steps[0].Process.LaunchPlan)
	}
}

// These inputs catch fail-open declaration parsing, missing source ownership,
// ignored trailing arguments, context reuse, recursion, and per-file budgets.
func TestPlannerWindowsDeclarativeRejects(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, tt := range []struct{ name, contents string }{
		{"unknown variable", "target_sources(core PRIVATE ${OTHER}/main.cpp)"},
		{"environment", "target_sources(core PRIVATE $ENV{HOME}/main.cpp)"},
		{"nested variable", "target_sources(core PRIVATE ${CMAKE_${OTHER}}/main.cpp)"},
		{"multiple variables", "target_sources(core PRIVATE ${CMAKE_SOURCE_DIR}/${CMAKE_SOURCE_DIR}/main.cpp)"},
		{"nonprefix variable", "target_sources(core PRIVATE x${CMAKE_SOURCE_DIR}/main.cpp)"},
		{"genexpr", "target_sources(core PRIVATE $<BOOL:1>)"},
		{"semicolon", "target_sources(core PRIVATE \"main.cpp;extra.cpp\")"},
		{"undeclared target", "target_sources(absent PRIVATE main.cpp)"},
		{"target case mismatch", "target_sources(CORE PRIVATE main.cpp)"},
		{"link case mismatch", "add_executable(check main.cpp)\ntarget_link_libraries(check PRIVATE CORE)"},
		{"alias target", "add_library(alias ALIAS core)\ntarget_sources(alias PRIVATE main.cpp)"},
		{"imported target", "add_library(imported STATIC IMPORTED)\ntarget_sources(imported PRIVATE main.cpp)"},
		{"executable alias", "add_executable(alias ALIAS core)"},
		{"executable imported", "add_executable(imported IMPORTED)"},
		{"duplicate executable", "add_executable(check main.cpp)\nadd_executable(check main.cpp)"},
		{"file set", "target_sources(core PRIVATE FILE_SET HEADERS BASE_DIRS include FILES include/value.h)"},
		{"glob", "file(GLOB sources *.cpp)\ntarget_sources(core PRIVATE ${sources})"},
		{"missing source", "target_sources(core PRIVATE missing.cpp)"},
		{"outside source", "target_sources(core PRIVATE ../outside.cpp)"},
		{"directory source", "target_sources(core PRIVATE include)"},
		{"missing library source", "add_library(missing STATIC missing.cpp)"},
		{"missing executable source", "add_executable(missing missing.cpp)"},
		{"missing include directory", "target_include_directories(core PUBLIC absent)"},
		{"file include directory", "target_include_directories(core PUBLIC main.cpp)"},
		{"outside include directory", "target_include_directories(core PUBLIC ..)"},
		{"include extra", "include(sdk/empty.cmake OPTIONAL)"},
		{"include module", "include(sdk/empty)"},
		{"subdirectory system", "add_subdirectory(child SYSTEM)"},
		{"subdirectory extra", "add_subdirectory(child child-out EXCLUDE_FROM_ALL extra)"},
		{"subdirectory outside binary", "add_subdirectory(child ../outside-build)"},
		{"different context reuse", "include(sdk/empty.cmake)\nadd_subdirectory(child)"},
		{"include cycle", "include(sdk/cycle.cmake)"},
		{"no scope", "target_sources(core main.cpp)"},
		{"empty scope", "target_sources(core PRIVATE PUBLIC main.cpp)"},
		{"trailing scope", "target_sources(core PRIVATE main.cpp PUBLIC)"},
		{"include system", "target_include_directories(core SYSTEM PUBLIC include)"},
		{"bad macro", "target_compile_definitions(core PUBLIC FEATURE=)"},
		{"escaped macro", "target_compile_definitions(core PUBLIC FEATURE=foo\\bar)"},
		{"quoted macro", "target_compile_definitions(core PUBLIC \"FEATURE=1\")"},
		{"file command limit", strings.Repeat("message(x)\n", maxCMakeLaunchCommands+1)},
		{"file argument limit", "message(" + strings.Repeat("x ", maxCMakeLaunchArguments+1) + ")"},
		{"cross file command limit", "include(sdk/commands.cmake)\n" + strings.Repeat("message(x)\n", maxCMakeLaunchCommands/2)},
		{"cross file argument limit", "include(sdk/arguments.cmake)\nmessage(" + strings.Repeat("x ", maxCMakeLaunchArguments/2) + ")"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			writeDeclarativeFixture(t, fixture, map[string]string{
				"CMakeLists.txt": "add_library(core STATIC main.cpp)\n" + tt.contents + "\n",
				"main.cpp":       "int main() { return 0; }\n", "extra.cpp": "int extra() { return 1; }\n",
				"include/value.h": "#define VALUE 1\n", "sdk/empty.cmake": "message(empty)\n",
				"sdk/cycle.cmake":      "include(sdk/cycle.cmake)\n",
				"child/CMakeLists.txt": "include(${CMAKE_SOURCE_DIR}/sdk/empty.cmake)\n",
				"sdk/commands.cmake":   strings.Repeat("message(x)\n", maxCMakeLaunchCommands/2),
				"sdk/arguments.cmake":  "message(" + strings.Repeat("x ", maxCMakeLaunchArguments/2) + ")",
			})
			if err := os.WriteFile(filepath.Join(filepath.Dir(fixture.sourceDir), "outside.cpp"), []byte("int outside;\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := planDeclarativeFixture(fixture); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan accepted %s: %v", tt.name, err)
			}
		})
	}
	for _, variable := range []string{"CMAKE_SOURCE_DIR", "CMAKE_CURRENT_SOURCE_DIR", "CMAKE_BINARY_DIR", "CMAKE_CURRENT_BINARY_DIR", "CMAKE_CURRENT_LIST_DIR"} {
		for _, writer := range []string{"set(%s include)", "list(APPEND %s include)", "string(CONCAT %s include)", "cmake_path(SET %s include)"} {
			t.Run(fmt.Sprintf(writer, variable), func(t *testing.T) {
				fixture := newPlannerFixture(t)
				writeDeclarativeFixture(t, fixture, map[string]string{"CMakeLists.txt": fmt.Sprintf(writer, variable) + "\n"})
				if _, err := planDeclarativeFixture(fixture); !errors.Is(err, task.ErrInvalidArgument) {
					t.Fatalf("controlled variable write accepted: %v", err)
				}
			})
		}
	}
}

func TestPlannerWindowsDeclarativeBudgets(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, tt := range []struct {
		name  string
		files map[string]string
	}{
		{"single file bytes", map[string]string{"CMakeLists.txt": "#" + strings.Repeat("x", maxCMakeLaunchFileBytes)}},
		{"total bytes", func() map[string]string {
			files := map[string]string{}
			var root strings.Builder
			for i := 0; i < maxCMakeLaunchTotalBytes/maxCMakeLaunchFileBytes; i++ {
				name := fmt.Sprintf("large%d.cmake", i)
				root.WriteString("include(" + name + ")\n")
				files[name] = "#" + strings.Repeat("x", maxCMakeLaunchFileBytes-1)
			}
			files["CMakeLists.txt"] = root.String()
			return files
		}()},
		{"launch entries", map[string]string{"CMakeLists.txt": func() string {
			var root strings.Builder
			for i := 0; i < 64; i++ {
				fmt.Fprintf(&root, "add_executable(check%d main.cpp)\n", i)
			}
			return root.String()
		}()}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			writeDeclarativeFixture(t, fixture, tt.files)
			if _, err := planDeclarativeFixture(fixture); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("budget exceeded but accepted: %v", err)
			}
		})
	}
}

func TestPlannerWindowsDeclarativeScopes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production launch declaration")
	}
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	writeDeclarativeFixture(t, fixture, map[string]string{
		"CMakeLists.txt": `add_library(core STATIC main.cpp)
target_include_directories(core PRIVATE include PUBLIC include INTERFACE include)
target_sources(core PRIVATE extra.cpp PUBLIC extra.cpp INTERFACE extra.cpp)
target_compile_definitions(core PRIVATE FEATURE PUBLIC FEATURE=+1 FEATURE=-1 INTERFACE FEATURE=VALUE)
include(sdk/empty.cmake)
include(sdk/empty.cmake)
`,
		"extra.cpp": "int extra() { return 1; }\n", "include/value.h": "#define VALUE 1\n", "sdk/empty.cmake": "message(empty)\n",
	})
	if _, err := planDeclarativeFixture(fixture); err != nil {
		t.Fatalf("closed multi-scope declarations rejected: %v", err)
	}
}

func TestPlannerWindowsDeclarativeAliasPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production launch declaration")
	}
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	writeDeclarativeFixture(t, fixture, map[string]string{"CMakeLists.txt": "add_executable(check alias.cpp)\n"})
	if err := os.Symlink(filepath.Join(fixture.sourceDir, "main.cpp"), filepath.Join(fixture.sourceDir, "alias.cpp")); err != nil {
		t.Skipf("Windows symlink privilege unavailable: %v", err)
	}
	if _, err := planDeclarativeFixture(fixture); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("alias source accepted: %v", err)
	}
}
