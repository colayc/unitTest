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
	"sync"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestMSVCBuildsSingleFixedVsDevCmdArgument(t *testing.T) {
	config := MSVCConfig{
		ToolsetVersion:     "14.40.33807",
		HostArchitecture:   "x64",
		TargetArchitecture: "arm64",
	}
	path := `C:\Program Files\微软 Visual Studio\Common7\Tools\VsDevCmd.bat`
	got, err := buildVsDevCmdArguments(path, config)
	if err != nil {
		t.Fatalf("buildVsDevCmdArguments() error = %v", err)
	}
	want := []string{
		"/d",
		"/s",
		"/c",
		`"call \"C:\Program Files\微软 Visual Studio\Common7\Tools\VsDevCmd.bat\" -no_logo -host_arch=x64 -arch=arm64 -vcvars_ver=14.40.33807 && set"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildVsDevCmdArguments() = %#v, want %#v", got, want)
	}
	got[3] = "changed"
	next, err := buildVsDevCmdArguments(path, config)
	if err != nil || next[3] != want[3] {
		t.Fatalf("buildVsDevCmdArguments() leaked caller mutation: %#v, %v", next, err)
	}
}

func TestMSVCRejectsVsDevCmdAndConfigCommandInjection(t *testing.T) {
	base := MSVCConfig{
		ToolsetVersion:     "14.40.33807",
		HostArchitecture:   "x64",
		TargetArchitecture: "x64",
	}
	for _, path := range []string{
		"C:\\VS&whoami\\VsDevCmd.bat",
		"C:\\VS%TEMP%\\VsDevCmd.bat",
		"C:\\VS!X!\\VsDevCmd.bat",
		"C:\\VS^X\\VsDevCmd.bat",
		"C:\\VS|X\\VsDevCmd.bat",
		"C:\\VS<X\\VsDevCmd.bat",
		"C:\\VS>X\\VsDevCmd.bat",
		"C:\\VS\"X\\VsDevCmd.bat",
		"C:\\VS\rX\\VsDevCmd.bat",
		"C:\\VS\nX\\VsDevCmd.bat",
		"C:\\VS\x00X\\VsDevCmd.bat",
	} {
		if _, err := buildVsDevCmdArguments(path, base); err == nil {
			t.Fatalf("buildVsDevCmdArguments(%q) accepted command metacharacters", path)
		}
	}
	configCases := []MSVCConfig{
		{ToolsetVersion: "14.40&whoami", HostArchitecture: "x64", TargetArchitecture: "x64"},
		{ToolsetVersion: "14.40", HostArchitecture: "x64&whoami", TargetArchitecture: "x64"},
		{ToolsetVersion: "14.40", HostArchitecture: "x64", TargetArchitecture: "x64|whoami"},
		{ToolsetVersion: "14.40", HostArchitecture: "amd64", TargetArchitecture: "x64"},
	}
	for _, config := range configCases {
		if _, err := buildVsDevCmdArguments(`C:\VS\Common7\Tools\VsDevCmd.bat`, config); err == nil {
			t.Fatalf("buildVsDevCmdArguments(%#v) accepted invalid config", config)
		}
	}
}

func TestMSVCEnvironmentParsingIsCaseInsensitiveBoundedAndSanitized(t *testing.T) {
	input := strings.Join([]string{
		`Path=C:\VS\bin`,
		`PATH=C:\VS\bin`,
		`INCLUDE=C:\VS\include`,
		`=C:=C:\workspace`,
		`UNIT_TEST_IDE_TOKEN=ide-secret`,
		`unit_test_service_control_handle=handle-secret`,
		`UNIT_TEST_STATUS_HANDLE=status-secret`,
		`UNIT_TEST_CONTROL_PIPE=control-secret`,
		`GITHUB_TOKEN=github-secret`,
		`gh_token=gh-secret`,
		`ACTIONS_RUNTIME_TOKEN=actions-secret`,
		`SYSTEM_ACCESSTOKEN=azure-secret`,
		`DATABASE_URL=database-secret`,
		`DB_PASSWORD=password-secret`,
		`PRIVATE_KEY=key-secret`,
		`NORMAL_TOKEN_SHAPED_SECRET=generic-secret`,
		`VCToolsInstallDir=C:\VS\VC\Tools\MSVC\14.40.33807\`,
		`VCToolsVersion=14.40.33807`,
		`WindowsSdkDir=C:\SDK\`,
		`WindowsSDKVersion=10.0.22621.0\`,
		`VSCMD_ARG_HOST_ARCH=x64`,
		`VSCMD_ARG_TGT_ARCH=x64`,
	}, "\r\n") + "\r\n"
	got, err := parseCapturedEnvironment([]byte(input))
	if err != nil {
		t.Fatalf("parseCapturedEnvironment() error = %v", err)
	}
	want := []string{
		`INCLUDE=C:\VS\include`,
		`Path=C:\VS\bin`,
		`VCToolsInstallDir=C:\VS\VC\Tools\MSVC\14.40.33807\`,
		`VCToolsVersion=14.40.33807`,
		`VSCMD_ARG_HOST_ARCH=x64`,
		`VSCMD_ARG_TGT_ARCH=x64`,
		`WindowsSdkDir=C:\SDK\`,
		`WindowsSDKVersion=10.0.22621.0\`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseCapturedEnvironment() = %#v, want %#v", got, want)
	}
	if strings.Contains(strings.Join(got, "\n"), "secret") {
		t.Fatalf("parseCapturedEnvironment() leaked a secret: %#v", got)
	}
	got[0] = "CHANGED=1"
	next, err := parseCapturedEnvironment([]byte(input))
	if err != nil || next[0] != want[0] {
		t.Fatalf("parseCapturedEnvironment() leaked caller mutation: %#v, %v", next, err)
	}
}

func TestMSVCEnvironmentRejectsMalformedConflictingAndOversizedInput(t *testing.T) {
	cases := map[string][]byte{
		"malformed":           []byte("PATH\r\n"),
		"NUL":                 []byte("PATH=C:\\bin\x00bad\r\n"),
		"conflicting key":     []byte("Path=C:\\one\r\nPATH=C:\\two\r\n"),
		"invalid empty key":   []byte("=not-a-drive-pseudo-variable\r\n"),
		"invalid UTF-8":       {0xff, '\r', '\n'},
		"oversized total":     []byte("PATH=" + strings.Repeat("x", maxCapturedEnvironmentBytes) + "\r\n"),
		"oversized one entry": []byte("PATH=" + strings.Repeat("x", maxCapturedEnvironmentEntryBytes) + "\r\n"),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := parseCapturedEnvironment(input); err == nil {
				t.Fatalf("parseCapturedEnvironment() = %#v, want error", got)
			}
		})
	}

	lines := make([]string, maxCapturedEnvironmentEntries+1)
	for index := range lines {
		lines[index] = "KEY" + string(rune('A'+index%26)) + strings.Repeat("X", index/26) + "=value"
	}
	if got, err := parseCapturedEnvironment([]byte(strings.Join(lines, "\r\n"))); err == nil {
		t.Fatalf("parseCapturedEnvironment() = %#v, accepted entry count limit + 1", got)
	}
}

func TestMSVCAdapterDiscoversValidatedManualToolchain(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	if len(adapters) != 2 {
		t.Fatalf("newWindowsAdapters() returned %d adapters, want 2", len(adapters))
	}

	instances, err := adapters[0].Discover(context.Background())
	if err != nil {
		var carrier issueCarrier
		if errors.As(err, &carrier) {
			t.Fatalf("MSVC Discover() error = %v, issues = %#v", err, carrier.ToolchainIssues())
		}
		t.Fatalf("MSVC Discover() error = %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("MSVC Discover() = %#v, want one instance", instances)
	}
	got := instances[0]
	if got.ID != "manual-msvc" || got.Family != FamilyMSVC ||
		got.CCompiler != fixture.cl || got.CXXCompiler != fixture.cl ||
		got.Version != "19.40.33811" ||
		got.TargetTriple != "x86_64-pc-windows-msvc" ||
		got.HostArchitecture != "x64" || got.TargetArchitecture != "x64" ||
		got.Sysroot != fixture.sdk {
		t.Fatalf("MSVC instance = %#v", got)
	}
	if want := []string{"Ninja", "Visual Studio 17 2022"}; !reflect.DeepEqual(got.Generators, want) {
		t.Fatalf("MSVC generators = %#v, want %#v", got.Generators, want)
	}
	if strings.Contains(strings.ToUpper(strings.Join(got.Environment, "\n")), "TOKEN") {
		t.Fatalf("MSVC environment leaked token metadata: %#v", got.Environment)
	}
	if got.Coverage != (CoverageCapability{}) {
		t.Fatalf("MSVC coverage = %#v, want empty", got.Coverage)
	}

	cmdCall := runner.findCall(fixture.cmd, fixture.vsDevCmdArgs("x64", "x64"))
	if cmdCall == nil || !reflect.DeepEqual(cmdCall.Env, fixture.baseEnvironment) {
		t.Fatalf("VsDevCmd call = %#v, want controlled base environment %#v", cmdCall, fixture.baseEnvironment)
	}
	runner.requireCall(t, fixture.vswhere, vswhereArguments())
	runner.requireCall(t, fixture.cl, []string{"/Bv"})
	runner.requireCall(t, fixture.link, []string{"/?"})
	runner.requireCall(t, fixture.msbuild, []string{"-version", "-nologo"})
	runner.requireCall(t, fixture.ninja, []string{"--version"})

	got.Environment[0] = "MUTATED=1"
	got.Generators[0] = "MUTATED"
	again, err := adapters[0].Discover(context.Background())
	if err != nil || len(again) != 1 ||
		again[0].Environment[0] == "MUTATED=1" ||
		again[0].Generators[0] == "MUTATED" {
		t.Fatalf("MSVC Discover() leaked caller mutation: %#v, %v", again, err)
	}
}

func TestMSVCAdapterKeepsPartialSuccessAndReportsManualSelectionFailure(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	manual := append(fixture.manualMSVC(), workspace.ToolchainConfig{
		ID:                 "missing-msvc",
		Family:             string(FamilyMSVC),
		InstallationID:     "missing-installation",
		ToolsetVersion:     fixture.toolsetVersion,
		HostArchitecture:   "x64",
		TargetArchitecture: "x64",
	})
	adapters, err := newWindowsAdapters(runner, manual, fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	instances, discoverErr := adapters[0].Discover(context.Background())
	if len(instances) != 1 || instances[0].ID != "manual-msvc" {
		t.Fatalf("MSVC Discover() instances = %#v", instances)
	}
	var carrier issueCarrier
	if !errors.As(discoverErr, &carrier) {
		t.Fatalf("MSVC Discover() error = %v, want issue carrier", discoverErr)
	}
	issues := carrier.ToolchainIssues()
	if len(issues) != 1 || issues[0].Code != "TOOLCHAIN_MANUAL_SELECTION_FAILED" ||
		strings.Contains(issues[0].Message, "missing-installation") {
		t.Fatalf("MSVC issues = %#v", issues)
	}
}

func TestMSVCAdapterRejectsMismatchedEnvironmentAndCompilerIdentity(t *testing.T) {
	tests := map[string]func(*windowsToolchainFixture, *windowsFakeRunner){
		"toolset environment": func(fixture *windowsToolchainFixture, runner *windowsFakeRunner) {
			runner.setOutput(
				fixture.cmd,
				fixture.vsDevCmdArgs("x64", "x64"),
				successfulWindowsOutput(strings.ReplaceAll(
					fixture.environmentOutput("x64", "x64"),
					"VCToolsVersion="+fixture.toolsetVersion,
					"VCToolsVersion=14.39.0",
				)),
			)
		},
		"target architecture": func(fixture *windowsToolchainFixture, runner *windowsFakeRunner) {
			runner.setOutput(
				fixture.cmd,
				fixture.vsDevCmdArgs("x64", "x64"),
				successfulWindowsOutput(strings.ReplaceAll(
					fixture.environmentOutput("x64", "x64"),
					"VSCMD_ARG_TGT_ARCH=x64",
					"VSCMD_ARG_TGT_ARCH=arm64",
				)),
			)
		},
		"compiler version": func(fixture *windowsToolchainFixture, runner *windowsFakeRunner) {
			runner.setOutput(
				fixture.cl,
				[]string{"/Bv"},
				successfulWindowsOutput("Microsoft (R) C/C++ Optimizing Compiler Version 19.39.0 for x64\r\n"),
			)
		},
		"linker version": func(fixture *windowsToolchainFixture, runner *windowsFakeRunner) {
			runner.setOutput(
				fixture.link,
				[]string{"/?"},
				successfulWindowsOutput("Microsoft (R) Incremental Linker Version 14.39.0\r\n"),
			)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newWindowsToolchainFixture(t)
			runner := newWindowsFakeRunner(fixture)
			mutate(fixture, runner)
			adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
			if err != nil {
				t.Fatalf("newWindowsAdapters() error = %v", err)
			}
			instances, discoverErr := adapters[0].Discover(context.Background())
			if len(instances) != 0 || discoverErr == nil {
				t.Fatalf("MSVC Discover() = %#v, %v, want rejected candidate", instances, discoverErr)
			}
		})
	}
}

func TestMSVCAdapterPropagatesCancellationAndBoundsProbeFailures(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if instances, discoverErr := adapters[0].Discover(ctx); !errors.Is(discoverErr, context.Canceled) ||
		instances != nil {
		t.Fatalf("MSVC Discover(canceled) = %#v, %v", instances, discoverErr)
	}

	secret := "should-never-escape"
	runner.setError(fixture.vswhere, vswhereArguments(), errors.New(secret))
	if instances, discoverErr := adapters[0].Discover(context.Background()); len(instances) != 0 ||
		discoverErr == nil || strings.Contains(discoverErr.Error(), secret) {
		t.Fatalf("MSVC Discover(failed vswhere) = %#v, %v", instances, discoverErr)
	}
}

func TestMSVCAdapterDoesNotClaimVS2022GeneratorForOlderInstallation(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	fixture.installationVer = "16.11.34"
	runner := newWindowsFakeRunner(fixture)
	runner.setOutput(
		fixture.msbuild,
		[]string{"-version", "-nologo"},
		successfulWindowsOutput("16.11.34\r\n"),
	)
	adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	instances, discoverErr := adapters[0].Discover(context.Background())
	if discoverErr != nil || len(instances) != 1 {
		t.Fatalf("MSVC Discover() = %#v, %v", instances, discoverErr)
	}
	if !reflect.DeepEqual(instances[0].Generators, []string{"Ninja"}) {
		t.Fatalf(
			"MSVC generators for VS 16 = %#v, want only verified Ninja",
			instances[0].Generators,
		)
	}
}

func TestMSVCAdapterReportsCMake43VisualStudio18Generator(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	fixture.installationVer = "18.0.12345.67"
	runner := newWindowsFakeRunner(fixture)
	runner.setOutput(
		fixture.msbuild,
		[]string{"-version", "-nologo"},
		successfulWindowsOutput("18.0.1\r\n"),
	)
	adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	instances, discoverErr := adapters[0].Discover(context.Background())
	if discoverErr != nil || len(instances) != 1 {
		t.Fatalf("MSVC Discover() = %#v, %v", instances, discoverErr)
	}
	want := []string{"Ninja", "Visual Studio 18 2026"}
	if !reflect.DeepEqual(instances[0].Generators, want) {
		t.Fatalf("MSVC generators for VS 18 = %#v, want %#v", instances[0].Generators, want)
	}
}

func TestMSVCLatestToolsetUsesNumericVersionOrdering(t *testing.T) {
	installation := t.TempDir()
	for _, version := range []string{"14.9", "14.10", "14.10.1", "14.8.99999"} {
		if err := os.MkdirAll(
			filepath.Join(installation, "VC", "Tools", "MSVC", version),
			0o755,
		); err != nil {
			t.Fatal(err)
		}
	}
	got, err := latestMSVCToolset(installation)
	if err != nil {
		t.Fatalf("latestMSVCToolset() error = %v", err)
	}
	if got != "14.10.1" {
		t.Fatalf("latestMSVCToolset() = %q, want 14.10.1", got)
	}
}

func TestMSVCAutomaticIDIncludesSelectedWindowsSDKVersion(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	firstRunner := newWindowsFakeRunner(fixture)
	firstAdapters, err := newWindowsAdapters(firstRunner, nil, fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters(first) error = %v", err)
	}
	first, err := firstAdapters[0].Discover(context.Background())
	if err != nil || len(first) != 1 {
		t.Fatalf("first Discover() = %#v, %v", first, err)
	}

	secondRunner := newWindowsFakeRunner(fixture)
	secondRunner.setOutput(
		fixture.cmd,
		fixture.vsDevCmdArgs("x64", "x64"),
		successfulWindowsOutput(strings.ReplaceAll(
			fixture.environmentOutput("x64", "x64"),
			"WindowsSDKVersion="+fixture.sdkVersion,
			"WindowsSDKVersion=10.0.26100.0",
		)),
	)
	secondAdapters, err := newWindowsAdapters(secondRunner, nil, fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters(second) error = %v", err)
	}
	second, err := secondAdapters[0].Discover(context.Background())
	if err != nil || len(second) != 1 {
		t.Fatalf("second Discover() = %#v, %v", second, err)
	}
	if first[0].ID == second[0].ID {
		t.Fatalf(
			"automatic ID %q did not include Windows SDK version change",
			first[0].ID,
		)
	}
}

func TestWindowsAdaptersRejectProbeTimeExecutableMutation(t *testing.T) {
	tests := map[string]func(*windowsToolchainFixture, *windowsFakeRunner) string{
		"vswhere": func(fixture *windowsToolchainFixture, runner *windowsFakeRunner) string {
			runner.setHook(fixture.vswhere, vswhereArguments(), func() {
				_ = os.WriteFile(fixture.vswhere, []byte("mutated-vswhere"), 0o755)
			})
			return "msvc"
		},
		"VsDevCmd": func(fixture *windowsToolchainFixture, runner *windowsFakeRunner) string {
			runner.setHook(fixture.cmd, fixture.vsDevCmdArgs("x64", "x64"), func() {
				_ = os.WriteFile(fixture.vsDevCmd, []byte("mutated-vsdevcmd"), 0o755)
			})
			return "msvc"
		},
		"compiler": func(fixture *windowsToolchainFixture, runner *windowsFakeRunner) string {
			runner.setHook(fixture.cl, []string{"/Bv"}, func() {
				_ = os.WriteFile(fixture.cl, []byte("mutated-compiler"), 0o755)
			})
			return "msvc"
		},
	}
	for name, prepare := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newWindowsToolchainFixture(t)
			runner := newWindowsFakeRunner(fixture)
			_ = prepare(fixture, runner)
			adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
			if err != nil {
				t.Fatalf("newWindowsAdapters() error = %v", err)
			}
			instances, discoverErr := adapters[0].Discover(context.Background())
			if len(instances) != 0 || discoverErr == nil {
				t.Fatalf("MSVC Discover(mutated executable) = %#v, %v", instances, discoverErr)
			}
		})
	}
}

type windowsToolchainFixture struct {
	root            string
	vswhere         string
	cmd             string
	ninja           string
	installation    string
	installationID  string
	installationVer string
	vsDevCmd        string
	toolset         string
	toolsetVersion  string
	cl              string
	link            string
	msbuild         string
	sdk             string
	sdkVersion      string
	llvmRoot        string
	clang           string
	lld             string
	llvmProfdata    string
	llvmCov         string
	baseEnvironment []string
}

func newWindowsToolchainFixture(t *testing.T) *windowsToolchainFixture {
	t.Helper()
	root := t.TempDir()
	fixture := &windowsToolchainFixture{
		root:            root,
		vswhere:         filepath.Join(root, "Installer", "vswhere.exe"),
		cmd:             filepath.Join(root, "Windows", "System32", "cmd.exe"),
		ninja:           filepath.Join(root, "CMake", "bin", "ninja.exe"),
		installation:    filepath.Join(root, "Program Files", "微软 Visual Studio"),
		installationID:  "visual-studio-17",
		installationVer: "17.10.4",
		toolsetVersion:  "14.40.33807",
		sdk:             filepath.Join(root, "Windows Kits", "10"),
		sdkVersion:      "10.0.22621.0",
		llvmRoot:        filepath.Join(root, "LLVM", "bin"),
	}
	fixture.vsDevCmd = filepath.Join(fixture.installation, "Common7", "Tools", "VsDevCmd.bat")
	fixture.toolset = filepath.Join(
		fixture.installation, "VC", "Tools", "MSVC", fixture.toolsetVersion,
	)
	fixture.cl = filepath.Join(fixture.toolset, "bin", "Hostx64", "x64", "cl.exe")
	fixture.link = filepath.Join(fixture.toolset, "bin", "Hostx64", "x64", "link.exe")
	fixture.msbuild = filepath.Join(fixture.installation, "MSBuild", "Current", "Bin", "MSBuild.exe")
	fixture.clang = filepath.Join(fixture.llvmRoot, "clang-cl.exe")
	fixture.lld = filepath.Join(fixture.llvmRoot, "lld-link.exe")
	fixture.llvmProfdata = filepath.Join(fixture.llvmRoot, "llvm-profdata.exe")
	fixture.llvmCov = filepath.Join(fixture.llvmRoot, "llvm-cov.exe")
	fixture.baseEnvironment = []string{
		"ComSpec=" + fixture.cmd,
		"SystemRoot=" + filepath.Join(root, "Windows"),
		"TEMP=" + filepath.Join(root, "Temp"),
		"TMP=" + filepath.Join(root, "Temp"),
	}
	for _, directory := range []string{fixture.sdk, filepath.Join(root, "Temp")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		fixture.vswhere, fixture.cmd, fixture.ninja, fixture.vsDevCmd,
		fixture.cl, fixture.link, fixture.msbuild, fixture.clang, fixture.lld,
		fixture.llvmProfdata, fixture.llvmCov,
	} {
		writeWindowsTool(t, path)
	}
	return fixture
}

func (fixture *windowsToolchainFixture) options() windowsDiscoveryOptions {
	return windowsDiscoveryOptions{
		VSWherePath:      fixture.vswhere,
		CmdPath:          fixture.cmd,
		NinjaPath:        fixture.ninja,
		LLVMRoot:         fixture.llvmRoot,
		BaseEnvironment:  append([]string(nil), fixture.baseEnvironment...),
		HostArchitecture: "x64",
	}
}

func (fixture *windowsToolchainFixture) manualMSVC() []workspace.ToolchainConfig {
	return []workspace.ToolchainConfig{{
		ID:                 "manual-msvc",
		Family:             string(FamilyMSVC),
		InstallationID:     fixture.installationID,
		ToolsetVersion:     fixture.toolsetVersion,
		HostArchitecture:   "x64",
		TargetArchitecture: "x64",
	}}
}

func (fixture *windowsToolchainFixture) vsDevCmdArgs(host, target string) []string {
	args, err := buildVsDevCmdArguments(fixture.vsDevCmd, MSVCConfig{
		ToolsetVersion:     fixture.toolsetVersion,
		HostArchitecture:   host,
		TargetArchitecture: target,
	})
	if err != nil {
		panic(err)
	}
	return args
}

func (fixture *windowsToolchainFixture) environmentOutput(host, target string) string {
	return strings.Join([]string{
		"Path=" + filepath.Dir(fixture.cl) + ";" + filepath.Dir(fixture.ninja),
		"INCLUDE=" + filepath.Join(fixture.toolset, "include"),
		"LIB=" + filepath.Join(fixture.toolset, "lib", target),
		"VCToolsInstallDir=" + fixture.toolset + string(filepath.Separator),
		"VCToolsVersion=" + fixture.toolsetVersion,
		"WindowsSdkDir=" + fixture.sdk + string(filepath.Separator),
		"WindowsSDKVersion=" + fixture.sdkVersion + string(filepath.Separator),
		"VSCMD_ARG_HOST_ARCH=" + host,
		"VSCMD_ARG_TGT_ARCH=" + target,
		"UNIT_TEST_IDE_TOKEN=ide-secret",
		"GITHUB_TOKEN=github-secret",
	}, "\r\n") + "\r\n"
}

func writeWindowsTool(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture executable: "+filepath.Base(path)), 0o755); err != nil {
		t.Fatal(err)
	}
}

type windowsProbeCall struct {
	Executable string
	Args       []string
	Env        []string
}

type windowsProbeResponse struct {
	result  probe.Result
	err     error
	hook    func()
	dynamic func(probe.Spec) (probe.Result, error)
}

type windowsFakeRunner struct {
	mu        sync.Mutex
	responses map[string]windowsProbeResponse
	calls     []windowsProbeCall
}

func newWindowsFakeRunner(fixture *windowsToolchainFixture) *windowsFakeRunner {
	runner := &windowsFakeRunner{responses: make(map[string]windowsProbeResponse)}
	vswhereJSON, err := json.Marshal([]map[string]any{{
		"instanceId":          fixture.installationID,
		"installationPath":    fixture.installation,
		"installationVersion": fixture.installationVer,
		"isComplete":          true,
		"isLaunchable":        true,
	}})
	if err != nil {
		panic(err)
	}
	runner.setOutput(fixture.vswhere, vswhereArguments(), successfulWindowsBytes(vswhereJSON))
	runner.setOutput(
		fixture.cmd,
		fixture.vsDevCmdArgs("x64", "x64"),
		successfulWindowsOutput(fixture.environmentOutput("x64", "x64")),
	)
	runner.setOutput(
		fixture.cl,
		[]string{"/Bv"},
		successfulWindowsOutput("Microsoft (R) C/C++ Optimizing Compiler Version 19.40.33811 for x64\r\n"),
	)
	runner.setOutput(
		fixture.link,
		[]string{"/?"},
		successfulWindowsOutput("Microsoft (R) Incremental Linker Version 14.40.33811.0\r\n"),
	)
	runner.setOutput(
		fixture.msbuild,
		[]string{"-version", "-nologo"},
		successfulWindowsOutput("17.10.4\r\n"),
	)
	runner.setOutput(fixture.ninja, []string{"--version"}, successfulWindowsOutput("1.12.1\r\n"))
	runner.setOutput(
		fixture.clang,
		[]string{"--version"},
		successfulWindowsOutput("clang version 18.1.8\r\nTarget: x86_64-pc-windows-msvc\r\n"),
	)
	runner.setOutput(
		fixture.lld,
		[]string{"--version"},
		successfulWindowsOutput("LLD 18.1.8\r\n"),
	)
	runner.setOutput(
		fixture.llvmProfdata,
		[]string{"--version"},
		successfulWindowsOutput("llvm-profdata\r\nLLVM version 18.1.8\r\n"),
	)
	runner.setOutput(
		fixture.llvmCov,
		[]string{"--version"},
		successfulWindowsOutput("LLVM version 18.1.8\r\n"),
	)
	return runner
}

func (runner *windowsFakeRunner) Run(ctx context.Context, spec probe.Spec) (probe.Result, error) {
	if err := ctx.Err(); err != nil {
		return probe.Result{ExitCode: -1}, err
	}
	runner.mu.Lock()
	runner.calls = append(runner.calls, windowsProbeCall{
		Executable: spec.Executable,
		Args:       append([]string(nil), spec.Args...),
		Env:        append([]string(nil), spec.Env...),
	})
	response, ok := runner.responses[windowsProbeKey(spec.Executable, spec.Args)]
	runner.mu.Unlock()
	if !ok {
		return probe.Result{ExitCode: -1}, errors.New("unexpected Windows probe")
	}
	if spec.Timeout != windowsProbeTimeout {
		return probe.Result{ExitCode: -1}, errors.New("unexpected Windows probe timeout")
	}
	wantMaximum := maxWindowsProbeOutput
	if strings.EqualFold(filepath.Base(spec.Executable), "vswhere.exe") {
		wantMaximum = maxVSWhereOutputBytes
	}
	if spec.MaxOutput != wantMaximum {
		return probe.Result{ExitCode: -1}, errors.New("unexpected Windows probe output limit")
	}
	for _, entry := range spec.Env {
		separator := strings.IndexByte(entry, '=')
		if separator > 0 && sensitiveEnvironmentKey(strings.ToUpper(entry[:separator])) {
			return probe.Result{ExitCode: -1}, errors.New("Windows probe received sensitive environment")
		}
	}
	if response.hook != nil {
		response.hook()
	}
	if response.dynamic != nil {
		return response.dynamic(spec)
	}
	response.result.Stdout = append([]byte(nil), response.result.Stdout...)
	response.result.Stderr = append([]byte(nil), response.result.Stderr...)
	return response.result, response.err
}

func (runner *windowsFakeRunner) setOutput(
	executable string,
	args []string,
	result probe.Result,
) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	key := windowsProbeKey(executable, args)
	response := runner.responses[key]
	response.result = result
	response.err = nil
	runner.responses[key] = response
}

func (runner *windowsFakeRunner) setError(executable string, args []string, err error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.responses[windowsProbeKey(executable, args)] = windowsProbeResponse{
		result: probe.Result{ExitCode: -1},
		err:    err,
	}
}

func (runner *windowsFakeRunner) setHook(executable string, args []string, hook func()) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	key := windowsProbeKey(executable, args)
	response := runner.responses[key]
	response.hook = hook
	runner.responses[key] = response
}

func (runner *windowsFakeRunner) setDynamic(
	executable string,
	args []string,
	dynamic func(probe.Spec) (probe.Result, error),
) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	key := windowsProbeKey(executable, args)
	response := runner.responses[key]
	response.dynamic = dynamic
	runner.responses[key] = response
}

func (runner *windowsFakeRunner) findCall(
	executable string,
	args []string,
) *windowsProbeCall {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	key := windowsProbeKey(executable, args)
	for index := range runner.calls {
		if windowsProbeKey(runner.calls[index].Executable, runner.calls[index].Args) == key {
			call := runner.calls[index]
			return &call
		}
	}
	return nil
}

func (runner *windowsFakeRunner) requireCall(
	t *testing.T,
	executable string,
	args []string,
) {
	t.Helper()
	if runner.findCall(executable, args) == nil {
		t.Fatalf("probe call %q %#v was not observed; calls = %#v", executable, args, runner.calls)
	}
}

func windowsProbeKey(executable string, args []string) string {
	return identityPath(executable) + "\x00" + strings.Join(args, "\x00")
}

func successfulWindowsOutput(stdout string) probe.Result {
	return probe.Result{ExitCode: 0, Stdout: []byte(stdout)}
}

func successfulWindowsBytes(stdout []byte) probe.Result {
	return probe.Result{ExitCode: 0, Stdout: append([]byte(nil), stdout...)}
}
