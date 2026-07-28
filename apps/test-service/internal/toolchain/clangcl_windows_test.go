//go:build windows

package toolchain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestClangCLParsesNativeCRLFVersionBanner(t *testing.T) {
	version, triple, err := parseClangCLBanner(
		[]byte("clang version 18.1.8\r\nTarget: x86_64-pc-windows-msvc\r\n"),
	)
	if err != nil || version != "18.1.8" || triple != "x86_64-pc-windows-msvc" {
		t.Fatalf("parseClangCLBanner() = %q, %q, %v", version, triple, err)
	}
}

func TestClangCLAdapterCombinesValidatedMSVCEnvironmentAndLLVMTools(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	manual := []workspace.ToolchainConfig{{
		ID:          "manual-clang-cl",
		Family:      string(FamilyClangCL),
		CCompiler:   fixture.clang,
		CPPCompiler: fixture.clang,
	}}
	adapters, err := newWindowsAdapters(runner, manual, fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	instances, err := adapters[1].Discover(context.Background())
	if err != nil {
		var carrier issueCarrier
		if errors.As(err, &carrier) {
			t.Fatalf("clang-cl Discover() error = %v, issues = %#v", err, carrier.ToolchainIssues())
		}
		t.Fatalf("clang-cl Discover() error = %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("clang-cl Discover() = %#v, want one instance", instances)
	}
	got := instances[0]
	if got.ID != "manual-clang-cl" || got.Family != FamilyClangCL ||
		got.CCompiler != fixture.clang || got.CXXCompiler != fixture.clang ||
		got.Version != "18.1.8" || got.TargetTriple != "x86_64-pc-windows-msvc" ||
		got.HostArchitecture != "x64" || got.TargetArchitecture != "x64" ||
		got.Sysroot != fixture.sdk {
		t.Fatalf("clang-cl instance = %#v", got)
	}
	if !reflect.DeepEqual(got.Generators, []string{"Ninja"}) {
		t.Fatalf("clang-cl generators = %#v, want Ninja", got.Generators)
	}
	if got.Coverage.LLVMProfdata != fixture.llvmProfdata ||
		got.Coverage.LLVMCov != fixture.llvmCov || got.Coverage.GCov != "" {
		t.Fatalf("clang-cl coverage = %#v", got.Coverage)
	}
	if strings.Contains(strings.ToUpper(strings.Join(got.Environment, "\n")), "TOKEN") {
		t.Fatalf("clang-cl environment leaked token metadata: %#v", got.Environment)
	}
	runner.requireCall(t, fixture.clang, []string{"--version"})
	runner.requireCall(t, fixture.lld, []string{"--version"})
	runner.requireCall(t, fixture.llvmProfdata, []string{"--version"})
	runner.requireCall(t, fixture.llvmCov, []string{"--version"})
	runner.requireCall(t, fixture.ninja, []string{"--version"})
}

func TestClangCLAdapterRejectsIncompatibleCompilerLinkerAndPair(t *testing.T) {
	t.Run("lld major", func(t *testing.T) {
		fixture := newWindowsToolchainFixture(t)
		runner := newWindowsFakeRunner(fixture)
		runner.setOutput(
			fixture.lld,
			[]string{"--version"},
			successfulWindowsOutput("LLD 17.0.6\r\n"),
		)
		adapters, err := newWindowsAdapters(runner, []workspace.ToolchainConfig{{
			ID:          "clang",
			Family:      string(FamilyClangCL),
			CCompiler:   fixture.clang,
			CPPCompiler: fixture.clang,
		}}, fixture.options())
		if err != nil {
			t.Fatalf("newWindowsAdapters() error = %v", err)
		}
		if instances, discoverErr := adapters[1].Discover(context.Background()); len(instances) != 0 ||
			discoverErr == nil {
			t.Fatalf("clang-cl Discover() = %#v, %v, want rejected candidate", instances, discoverErr)
		}
	})

	t.Run("compiler pair root", func(t *testing.T) {
		fixture := newWindowsToolchainFixture(t)
		other := filepath.Join(fixture.root, "Other LLVM", "bin", "clang-cl.exe")
		writeWindowsTool(t, other)
		runner := newWindowsFakeRunner(fixture)
		runner.setOutput(
			other,
			[]string{"--version"},
			successfulWindowsOutput("clang version 18.1.8\r\nTarget: x86_64-pc-windows-msvc\r\n"),
		)
		adapters, err := newWindowsAdapters(runner, []workspace.ToolchainConfig{{
			ID:          "clang",
			Family:      string(FamilyClangCL),
			CCompiler:   fixture.clang,
			CPPCompiler: other,
		}}, fixture.options())
		if err != nil {
			t.Fatalf("newWindowsAdapters() error = %v", err)
		}
		if instances, discoverErr := adapters[1].Discover(context.Background()); len(instances) != 0 ||
			discoverErr == nil {
			t.Fatalf("clang-cl Discover() = %#v, %v, want rejected pair", instances, discoverErr)
		}
	})
}

func TestClangCLAdapterDowngradesMissingOrMismatchedCoverageCapability(t *testing.T) {
	tests := map[string]func(*windowsToolchainFixture, *windowsFakeRunner){
		"mismatched major": func(fixture *windowsToolchainFixture, runner *windowsFakeRunner) {
			runner.setOutput(
				fixture.llvmProfdata,
				[]string{"--version"},
				successfulWindowsOutput("LLVM version 17.0.6\r\n"),
			)
		},
		"mismatched minor": func(fixture *windowsToolchainFixture, runner *windowsFakeRunner) {
			runner.setOutput(
				fixture.llvmProfdata,
				[]string{"--version"},
				successfulWindowsOutput("LLVM version 18.0.0\r\n"),
			)
			runner.setOutput(
				fixture.llvmCov,
				[]string{"--version"},
				successfulWindowsOutput("LLVM version 18.0.0\r\n"),
			)
		},
		"mismatched compiler patch": func(fixture *windowsToolchainFixture, runner *windowsFakeRunner) {
			runner.setOutput(
				fixture.llvmProfdata,
				[]string{"--version"},
				successfulWindowsOutput("LLVM version 18.1.7\r\n"),
			)
			runner.setOutput(
				fixture.llvmCov,
				[]string{"--version"},
				successfulWindowsOutput("LLVM version 18.1.7\r\n"),
			)
		},
		"mismatched coverage patch": func(fixture *windowsToolchainFixture, runner *windowsFakeRunner) {
			runner.setOutput(
				fixture.llvmCov,
				[]string{"--version"},
				successfulWindowsOutput("LLVM version 18.1.7\r\n"),
			)
		},
		"missing tool": func(fixture *windowsToolchainFixture, _ *windowsFakeRunner) {
			if err := os.Remove(fixture.llvmCov); err != nil {
				panic(err)
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newWindowsToolchainFixture(t)
			runner := newWindowsFakeRunner(fixture)
			mutate(fixture, runner)
			adapters, err := newWindowsAdapters(runner, []workspace.ToolchainConfig{{
				ID:          "clang",
				Family:      string(FamilyClangCL),
				CCompiler:   fixture.clang,
				CPPCompiler: fixture.clang,
			}}, fixture.options())
			if err != nil {
				t.Fatalf("newWindowsAdapters() error = %v", err)
			}
			instances, discoverErr := adapters[1].Discover(context.Background())
			if discoverErr != nil || len(instances) != 1 {
				t.Fatalf("clang-cl Discover() = %#v, %v", instances, discoverErr)
			}
			if instances[0].Coverage != (CoverageCapability{}) {
				t.Fatalf("coverage = %#v, want safe downgrade", instances[0].Coverage)
			}
		})
	}
}

func TestClangCLAdapterRequiresVerifiedGeneratorAndPropagatesCancellation(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	if err := os.Remove(fixture.ninja); err != nil {
		t.Fatal(err)
	}
	runner := newWindowsFakeRunner(fixture)
	adapters, err := newWindowsAdapters(runner, []workspace.ToolchainConfig{{
		ID:          "clang",
		Family:      string(FamilyClangCL),
		CCompiler:   fixture.clang,
		CPPCompiler: fixture.clang,
	}}, fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	instances, discoverErr := adapters[1].Discover(context.Background())
	if len(instances) != 0 || discoverErr == nil {
		t.Fatalf("clang-cl Discover(no Ninja) = %#v, %v", instances, discoverErr)
	}
	var carrier issueCarrier
	if !errors.As(discoverErr, &carrier) ||
		len(carrier.ToolchainIssues()) != 1 ||
		carrier.ToolchainIssues()[0].Code != "WINDOWS_BUILD_TOOL_NOT_FOUND" {
		t.Fatalf("clang-cl missing Ninja issues = %#v", carrier.ToolchainIssues())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if instances, discoverErr := adapters[1].Discover(ctx); instances != nil ||
		!errors.Is(discoverErr, context.Canceled) {
		t.Fatalf("clang-cl Discover(canceled) = %#v, %v", instances, discoverErr)
	}
}

func TestClangCLAdapterRejectsNonClangCLFrontendPaths(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	clangDriver := filepath.Join(fixture.llvmRoot, "clang.exe")
	writeWindowsTool(t, clangDriver)
	runner := newWindowsFakeRunner(fixture)
	if adapters, err := newWindowsAdapters(runner, []workspace.ToolchainConfig{{
		ID:          "wrong-frontend",
		Family:      string(FamilyClangCL),
		CCompiler:   clangDriver,
		CPPCompiler: clangDriver,
	}}, fixture.options()); err == nil {
		t.Fatalf("newWindowsAdapters() = %#v, want non-clang-cl frontend rejection", adapters)
	}
}

func TestClangCLAdapterFallsBackToLaterCompatibleMSVCContext(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	secondID := "visual-studio-18"
	secondInstallation := filepath.Join(fixture.root, "Program Files", "Visual Studio 18")
	secondToolsetVersion := "14.41.10000"
	secondVsDevCmd := filepath.Join(secondInstallation, "Common7", "Tools", "VsDevCmd.bat")
	secondToolset := filepath.Join(
		secondInstallation,
		"VC",
		"Tools",
		"MSVC",
		secondToolsetVersion,
	)
	for _, path := range []string{
		secondVsDevCmd,
		filepath.Join(secondToolset, "bin", "Hostx64", "x64", "cl.exe"),
		filepath.Join(secondToolset, "bin", "Hostx64", "x64", "link.exe"),
	} {
		writeWindowsTool(t, path)
	}
	for _, directory := range []string{
		filepath.Join(secondToolset, "include"),
		filepath.Join(secondToolset, "lib", "x64"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	runner := newWindowsFakeRunner(fixture)
	installations, err := json.Marshal([]map[string]any{
		{
			"instanceId":          secondID,
			"installationPath":    secondInstallation,
			"installationVersion": "18.0.12345.67",
			"isComplete":          true,
			"isLaunchable":        true,
		},
		{
			"instanceId":          fixture.installationID,
			"installationPath":    fixture.installation,
			"installationVersion": fixture.installationVer,
			"isComplete":          true,
			"isLaunchable":        true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.setOutput(fixture.vswhere, vswhereArguments(), successfulWindowsBytes(installations))
	secondArgs, err := buildVsDevCmdArguments(secondVsDevCmd, MSVCConfig{
		ToolsetVersion:     secondToolsetVersion,
		HostArchitecture:   "x64",
		TargetArchitecture: "x64",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondToolsetInclude := filepath.Join(secondToolset, "include")
	secondToolsetLibrary := filepath.Join(secondToolset, "lib", "x64")
	sdkIncludeRoot := filepath.Join(fixture.sdk, "Include", fixture.sdkVersion)
	sdkLibraryRoot := filepath.Join(fixture.sdk, "Lib", fixture.sdkVersion)
	secondEnvironment := strings.Join([]string{
		"Path=" + filepath.Join(secondToolset, "bin", "Hostx64", "x64") +
			";" + filepath.Dir(fixture.ninja),
		"INCLUDE=" + strings.Join([]string{
			secondToolsetInclude,
			filepath.Join(sdkIncludeRoot, "ucrt"),
			filepath.Join(sdkIncludeRoot, "um"),
			filepath.Join(sdkIncludeRoot, "shared"),
		}, ";"),
		"LIB=" + strings.Join([]string{
			secondToolsetLibrary,
			filepath.Join(sdkLibraryRoot, "ucrt", "x64"),
			filepath.Join(sdkLibraryRoot, "um", "x64"),
		}, ";"),
		"LIBPATH=" + secondToolsetLibrary,
		"VCToolsInstallDir=" + secondToolset + string(filepath.Separator),
		"VCToolsVersion=" + secondToolsetVersion,
		"WindowsSdkDir=" + fixture.sdk + string(filepath.Separator),
		"WindowsSDKVersion=" + fixture.sdkVersion + string(filepath.Separator),
		"VSCMD_ARG_HOST_ARCH=x64",
		"VSCMD_ARG_TGT_ARCH=x64",
	}, "\r\n") + "\r\n"
	runner.setOutput(
		fixture.cmd,
		secondArgs,
		successfulWindowsOutput(secondEnvironment),
	)
	runner.setDynamic(
		fixture.clang,
		[]string{"--version"},
		func(spec probe.Spec) (probe.Result, error) {
			if environmentContains(spec.Env, "VCToolsVersion="+fixture.toolsetVersion) {
				return successfulWindowsOutput(
					"clang version 18.1.8\r\nTarget: aarch64-pc-windows-msvc\r\n",
				), nil
			}
			return successfulWindowsOutput(
				"clang version 18.1.8\r\nTarget: x86_64-pc-windows-msvc\r\n",
			), nil
		},
	)

	adapters, err := newWindowsAdapters(runner, []workspace.ToolchainConfig{{
		ID:          "manual-clang-cl",
		Family:      string(FamilyClangCL),
		CCompiler:   fixture.clang,
		CPPCompiler: fixture.clang,
	}}, fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	instances, discoverErr := adapters[1].Discover(context.Background())
	if len(instances) != 1 || instances[0].ID != "manual-clang-cl" ||
		!environmentContains(
			instances[0].Environment,
			"VCToolsVersion="+secondToolsetVersion,
		) {
		t.Fatalf("clang-cl Discover() instances = %#v", instances)
	}
	var carrier issueCarrier
	if !errors.As(discoverErr, &carrier) || len(carrier.ToolchainIssues()) != 1 {
		t.Fatalf("clang-cl Discover() error = %v, want one partial-success issue", discoverErr)
	}
}

func TestClangCLAutomaticDiscoveryRejectsConfiguredLLVMRootReplacement(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	adapters, err := newWindowsAdapters(runner, nil, fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}

	llvmParent := filepath.Dir(fixture.llvmRoot)
	outsideParent := filepath.Join(fixture.root, "outside-llvm")
	if err := os.Rename(llvmParent, outsideParent); err != nil {
		t.Fatal(err)
	}
	createWindowsToolchainJunction(t, llvmParent, outsideParent)
	outsideRoot := filepath.Join(outsideParent, "bin")
	for _, tool := range []struct {
		path string
		args []string
		out  string
	}{
		{filepath.Join(outsideRoot, "clang-cl.exe"), []string{"--version"}, "clang version 18.1.8\r\nTarget: x86_64-pc-windows-msvc\r\n"},
		{filepath.Join(outsideRoot, "lld-link.exe"), []string{"--version"}, "LLD 18.1.8\r\n"},
		{filepath.Join(outsideRoot, "llvm-profdata.exe"), []string{"--version"}, "LLVM version 18.1.8\r\n"},
		{filepath.Join(outsideRoot, "llvm-cov.exe"), []string{"--version"}, "LLVM version 18.1.8\r\n"},
	} {
		runner.setOutput(tool.path, tool.args, successfulWindowsOutput(tool.out))
	}
	instances, discoverErr := adapters[1].Discover(context.Background())
	if len(instances) != 0 || discoverErr == nil {
		t.Fatalf("clang-cl Discover(replaced LLVM root) = %#v, %v", instances, discoverErr)
	}
}

func TestClangCLAutomaticDiscoveryRejectsLLVMToolFileEscapes(t *testing.T) {
	tests := map[string]struct {
		target       func(*windowsToolchainFixture) string
		args         []string
		output       string
		wantInstance bool
	}{
		"compiler": {
			target: func(fixture *windowsToolchainFixture) string { return fixture.clang },
			args:   []string{"--version"},
			output: "clang version 18.1.8\r\nTarget: x86_64-pc-windows-msvc\r\n",
		},
		"linker": {
			target: func(fixture *windowsToolchainFixture) string { return fixture.lld },
			args:   []string{"--version"},
			output: "LLD 18.1.8\r\n",
		},
		"profdata": {
			target:       func(fixture *windowsToolchainFixture) string { return fixture.llvmProfdata },
			args:         []string{"--version"},
			output:       "LLVM version 18.1.8\r\n",
			wantInstance: true,
		},
		"coverage": {
			target:       func(fixture *windowsToolchainFixture) string { return fixture.llvmCov },
			args:         []string{"--version"},
			output:       "LLVM version 18.1.8\r\n",
			wantInstance: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newWindowsToolchainFixture(t)
			runner := newWindowsFakeRunner(fixture)
			target := test.target(fixture)
			outside := filepath.Join(fixture.root, "outside-llvm-tool", filepath.Base(target))
			writeWindowsTool(t, outside)
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			createWindowsToolchainFileSymlink(t, target, outside)
			runner.setOutput(outside, test.args, successfulWindowsOutput(test.output))
			adapters, err := newWindowsAdapters(runner, nil, fixture.options())
			if err != nil {
				t.Fatalf("newWindowsAdapters() error = %v", err)
			}
			instances, discoverErr := adapters[1].Discover(context.Background())
			if !test.wantInstance {
				if len(instances) != 0 || discoverErr == nil {
					t.Fatalf(
						"clang-cl Discover(core file escape) = %#v, %v, want rejection",
						instances,
						discoverErr,
					)
				}
				return
			}
			if discoverErr != nil || len(instances) != 1 {
				t.Fatalf(
					"clang-cl Discover(coverage file escape) = %#v, %v",
					instances,
					discoverErr,
				)
			}
			if instances[0].Coverage != (CoverageCapability{}) {
				t.Fatalf("coverage file escape capability = %#v", instances[0].Coverage)
			}
		})
	}
}

func TestClangCLManualCompilerUsesItsOwnTrustRoot(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	manualRoot := filepath.Join(fixture.root, "manual-llvm")
	manualClang := filepath.Join(manualRoot, "clang-cl.exe")
	manualLLD := filepath.Join(manualRoot, "lld-link.exe")
	manualProfdata := filepath.Join(manualRoot, "llvm-profdata.exe")
	manualCov := filepath.Join(manualRoot, "llvm-cov.exe")
	for _, path := range []string{manualClang, manualLLD, manualProfdata, manualCov} {
		writeWindowsTool(t, path)
	}
	runner.setOutput(
		manualClang,
		[]string{"--version"},
		successfulWindowsOutput(
			"clang version 18.1.8\r\nTarget: x86_64-pc-windows-msvc\r\n",
		),
	)
	runner.setOutput(
		manualLLD,
		[]string{"--version"},
		successfulWindowsOutput("LLD 18.1.8\r\n"),
	)
	runner.setOutput(
		manualProfdata,
		[]string{"--version"},
		successfulWindowsOutput("LLVM version 18.1.8\r\n"),
	)
	runner.setOutput(
		manualCov,
		[]string{"--version"},
		successfulWindowsOutput("LLVM version 18.1.8\r\n"),
	)
	adapters, err := newWindowsAdapters(runner, []workspace.ToolchainConfig{{
		ID:          "manual-outside-configured-root",
		Family:      string(FamilyClangCL),
		CCompiler:   manualClang,
		CPPCompiler: manualClang,
	}}, fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	instances, discoverErr := adapters[1].Discover(context.Background())
	if discoverErr != nil || len(instances) != 1 {
		t.Fatalf("clang-cl Discover(manual trust root) = %#v, %v", instances, discoverErr)
	}
	if instances[0].CCompiler != manualClang ||
		instances[0].Coverage.LLVMProfdata != manualProfdata ||
		instances[0].Coverage.LLVMCov != manualCov {
		t.Fatalf("manual clang-cl instance = %#v", instances[0])
	}
}

func environmentContains(environment []string, want string) bool {
	for _, entry := range environment {
		if strings.EqualFold(entry, want) {
			return true
		}
	}
	return false
}
