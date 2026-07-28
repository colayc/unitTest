//go:build windows

package toolchain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestMSVCBuildsTypedFixedVsDevCmdArguments(t *testing.T) {
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
		"call",
		`C:\Program Files\微软 Visual Studio\Common7\Tools\VsDevCmd.bat`,
		"-no_logo",
		"-host_arch=x64",
		"-arch=arm64",
		"-vcvars_ver=14.40.33807",
		"&&",
		"set",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildVsDevCmdArguments() = %#v, want %#v", got, want)
	}
	got[4] = "changed"
	next, err := buildVsDevCmdArguments(path, config)
	if err != nil || next[4] != want[4] {
		t.Fatalf("buildVsDevCmdArguments() leaked caller mutation: %#v, %v", next, err)
	}
}

func TestMSVCTypedVsDevCmdArgumentsRunThroughProductionRunner(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Unicode 空 格 (x86)")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(root, "VsDevCmd.bat")
	content := []byte(
		"@echo off\r\n" +
			"echo VSDEVCMD_TYPED_ARGS=%*\r\n" +
			"set \"VSDEVCMD_TYPED_CAPTURE=ok\"\r\n" +
			"set\r\n",
	)
	if err := os.WriteFile(batch, content, 0o600); err != nil {
		t.Fatal(err)
	}
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		t.Skip("SystemRoot is unavailable")
	}
	cmd := filepath.Join(systemRoot, "System32", "cmd.exe")
	for _, target := range []string{"x86", "x64", "arm64"} {
		t.Run(target, func(t *testing.T) {
			args, err := buildVsDevCmdArguments(batch, MSVCConfig{
				ToolsetVersion:     "14.40.33807",
				HostArchitecture:   "x64",
				TargetArchitecture: target,
			})
			if err != nil {
				t.Fatalf("buildVsDevCmdArguments() error = %v", err)
			}
			result, runErr := probe.NewRunner().Run(context.Background(), probe.Spec{
				Executable: cmd,
				Args:       args,
				Env: []string{
					"ComSpec=" + cmd,
					"Path=" + filepath.Join(systemRoot, "System32") + ";" + systemRoot,
					"SystemRoot=" + systemRoot,
					"TEMP=" + t.TempDir(),
					"TMP=" + t.TempDir(),
				},
				Timeout:   windowsProbeTimeout,
				MaxOutput: maxWindowsProbeOutput,
			})
			if runErr != nil || result.ExitCode != 0 || len(result.Stderr) != 0 {
				t.Fatalf(
					"production Runner typed batch = exit %d, stderr %q, error %v",
					result.ExitCode,
					result.Stderr,
					runErr,
				)
			}
			output := string(result.Stdout)
			if !strings.Contains(output, "VSDEVCMD_TYPED_CAPTURE=ok") ||
				!strings.Contains(output, "-host_arch=x64 -arch="+target) {
				t.Fatalf("production Runner output omitted typed arguments: %q", output)
			}
		})
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

func TestMSVCCompilerBannerParsesLocalizedOEMFileVersionFallback(t *testing.T) {
	output := append(
		[]byte{0xd6, 0xd0, 0xce, 0xc4, ' ', 'x', '6', '4', '\r', '\n'},
		[]byte(
			`C:\Program Files (x86)\Microsoft Visual Studio\VC\Tools\MSVC\14.44\bin\Hostx64\x64\cl.exe:`,
		)...,
	)
	output = append(output, 0xb0, 0xe6, 0xb1, 0xbe, ':', ' ')
	output = append(output, []byte("19.44.35228.0\r\n")...)
	version, architecture, err := parseMSVCCompilerBanner(output)
	if err != nil || version != "19.44.35228.0" || architecture != "x64" {
		t.Fatalf(
			"parseMSVCCompilerBanner(localized OEM) = %q, %q, %v",
			version,
			architecture,
			err,
		)
	}
}

func TestMSVCAdapterUsesBoundedLocalizedOEMProbePolicies(t *testing.T) {
	localizedCompilerOutput := append(
		[]byte{0xd6, 0xd0, 0xce, 0xc4, ' ', 'x', '6', '4', '\r', '\n'},
		[]byte(
			`C:\Program Files (x86)\Microsoft Visual Studio\VC\Tools\MSVC\14.40\bin\Hostx64\x64\cl.exe:`,
		)...,
	)
	localizedCompilerOutput = append(
		localizedCompilerOutput,
		0xb0, 0xe6, 0xb1, 0xbe, ':', ' ',
	)
	localizedCompilerOutput = append(
		localizedCompilerOutput,
		[]byte("19.40.33811.0\r\n")...,
	)
	for _, test := range []struct {
		name             string
		compilerExitCode int
		wantSuccess      bool
	}{
		{name: "documented localized exit", compilerExitCode: 2, wantSuccess: true},
		{name: "unexpected exit", compilerExitCode: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWindowsToolchainFixture(t)
			runner := newWindowsFakeRunner(fixture)
			runner.setOutput(fixture.cl, []string{"/Bv"}, probe.Result{
				ExitCode: test.compilerExitCode,
				Stderr:   localizedCompilerOutput,
			})
			runner.setOutput(fixture.link, []string{"/?"}, probe.Result{
				ExitCode: 1100,
				Stdout: []byte(
					"Microsoft (R) Incremental Linker Version 14.40.33811.0\r\n",
				),
				Stderr: []byte{0xb0, 0xe6, 0xb1, 0xbe},
			})
			adapters, err := newWindowsAdapters(
				runner,
				fixture.manualMSVC(),
				fixture.options(),
			)
			if err != nil {
				t.Fatal(err)
			}
			instances, discoverErr := adapters[0].Discover(context.Background())
			if test.wantSuccess {
				if discoverErr != nil || len(instances) != 1 {
					t.Fatalf("localized MSVC Discover() = %#v, %v", instances, discoverErr)
				}
				return
			}
			if len(instances) != 0 || discoverErr == nil {
				t.Fatalf(
					"MSVC Discover(unexpected exit) = %#v, %v, want rejection",
					instances,
					discoverErr,
				)
			}
		})
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

func TestMSVCAcceptsProductionShapedEnvironmentAndFiltersUntrustedPaths(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	runner.setOutput(
		fixture.cmd,
		fixture.vsDevCmdArgs("x64", "x64"),
		successfulWindowsOutput(fixture.productionEnvironmentOutput("x64", "x64")),
	)
	adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	instances, discoverErr := adapters[0].Discover(context.Background())
	if discoverErr != nil || len(instances) != 1 {
		t.Fatalf("MSVC Discover(production environment) = %#v, %v", instances, discoverErr)
	}
	instance := instances[0]
	if !reflect.DeepEqual(
		instance.Generators,
		[]string{"Ninja", "Visual Studio 17 2022"},
	) {
		t.Fatalf("production generators = %#v", instance.Generators)
	}
	values := windowsEnvironmentValues(instance.Environment)
	for _, required := range []string{
		filepath.Join(fixture.installation, "VC", "Auxiliary", "VS", "include"),
		filepath.Join(fixture.sdk, "Include", fixture.sdkVersion, "winrt"),
		filepath.Join(fixture.sdk, "Include", fixture.sdkVersion, "cppwinrt"),
		filepath.Join(
			fixture.netFXSDK,
			"References",
			"CommonConfiguration",
			"Neutral",
		),
		filepath.Join(
			fixture.root,
			"Windows",
			"Microsoft.NET",
			"Framework64",
			"v4.0.30319",
		),
	} {
		if !windowsPathListContains(values, required) {
			t.Fatalf("filtered environment omitted verified path %q: %#v", required, values)
		}
	}
	if !windowsPathListContains(
		map[string]string{"PATH": values["PATH"]},
		filepath.Dir(fixture.ninja),
	) {
		t.Fatalf("verified Ninja root missing from final PATH: %q", values["PATH"])
	}
	if strings.Contains(
		strings.ToLower(strings.Join(instance.Environment, "\n")),
		strings.ToLower(filepath.Join(fixture.root, "untrusted inherited")),
	) {
		t.Fatalf("final environment leaked an untrusted path: %#v", instance.Environment)
	}
}

func TestMSVCProductionShapedEnvironmentSupportsAllTargetArchitectures(t *testing.T) {
	for _, test := range []struct {
		target string
		triple string
	}{
		{target: "x86", triple: "i686-pc-windows-msvc"},
		{target: "x64", triple: "x86_64-pc-windows-msvc"},
		{target: "arm64", triple: "aarch64-pc-windows-msvc"},
	} {
		t.Run(test.target, func(t *testing.T) {
			fixture := newWindowsToolchainFixture(t)
			binaryDirectory := filepath.Join(
				fixture.toolset,
				"bin",
				"Hostx64",
				test.target,
			)
			cl := filepath.Join(binaryDirectory, "cl.exe")
			link := filepath.Join(binaryDirectory, "link.exe")
			for _, directory := range []string{
				filepath.Join(fixture.toolset, "lib", test.target),
				filepath.Join(
					fixture.sdk,
					"Lib",
					fixture.sdkVersion,
					"ucrt",
					test.target,
				),
				filepath.Join(
					fixture.sdk,
					"Lib",
					fixture.sdkVersion,
					"um",
					test.target,
				),
				filepath.Join(fixture.sdk, "bin", fixture.sdkVersion, test.target),
			} {
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			writeWindowsTool(t, cl)
			writeWindowsTool(t, link)
			runner := newWindowsFakeRunner(fixture)
			runner.setOutput(
				fixture.cmd,
				fixture.vsDevCmdArgs("x64", test.target),
				successfulWindowsOutput(
					fixture.productionEnvironmentOutput("x64", test.target),
				),
			)
			runner.setOutput(
				cl,
				[]string{"/Bv"},
				successfulWindowsOutput(
					"Microsoft (R) C/C++ Optimizing Compiler Version 19.40.33811 for "+
						test.target+"\r\n",
				),
			)
			runner.setOutput(
				link,
				[]string{"/?"},
				successfulWindowsOutput(
					"Microsoft (R) Incremental Linker Version 14.40.33811.0\r\n",
				),
			)
			manual := []workspace.ToolchainConfig{{
				ID:                 "manual-msvc-" + test.target,
				Family:             string(FamilyMSVC),
				InstallationID:     fixture.installationID,
				ToolsetVersion:     fixture.toolsetVersion,
				HostArchitecture:   "x64",
				TargetArchitecture: test.target,
			}}
			adapters, err := newWindowsAdapters(runner, manual, fixture.options())
			if err != nil {
				t.Fatal(err)
			}
			instances, discoverErr := adapters[0].Discover(context.Background())
			if discoverErr != nil || len(instances) != 1 {
				t.Fatalf("MSVC Discover(%s) = %#v, %v", test.target, instances, discoverErr)
			}
			if instances[0].TargetArchitecture != test.target ||
				instances[0].TargetTriple != test.triple {
				t.Fatalf("MSVC %s instance = %#v", test.target, instances[0])
			}
		})
	}
}

func TestMSVCFiltersUntrustedNETFXSDKDirectory(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	untrusted := filepath.Join(fixture.root, "untrusted inherited")
	output := strings.Replace(
		fixture.productionEnvironmentOutput("x64", "x64"),
		"NETFXSDKDir="+fixture.netFXSDK,
		"NETFXSDKDir="+untrusted,
		1,
	)
	runner.setOutput(
		fixture.cmd,
		fixture.vsDevCmdArgs("x64", "x64"),
		successfulWindowsOutput(output),
	)
	adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	instances, discoverErr := adapters[0].Discover(context.Background())
	if discoverErr != nil || len(instances) != 1 {
		t.Fatalf("MSVC Discover(untrusted NETFXSDKDir) = %#v, %v", instances, discoverErr)
	}
	if strings.Contains(
		strings.ToLower(strings.Join(instances[0].Environment, "\n")),
		strings.ToLower(untrusted),
	) {
		t.Fatalf("final environment leaked untrusted NETFXSDKDir: %#v", instances[0].Environment)
	}
}

func TestMSVCProductionDiscoveryFindsInstalledVisualStudio(t *testing.T) {
	options, err := defaultWindowsDiscoveryOptions()
	if err != nil {
		t.Skipf("fixed Windows discovery environment is unavailable: %v", err)
	}
	if !options.VSInstallationMetadataExpected {
		t.Skip("fixed Visual Studio installation metadata is not present")
	}
	adapters, err := newWindowsAdapters(probe.NewRunner(), nil, options)
	if err != nil {
		t.Fatalf("newWindowsAdapters(production) error = %v", err)
	}
	msvc, ok := adapters[0].(*msvcAdapter)
	if !ok {
		t.Fatalf("production MSVC adapter = %T", adapters[0])
	}
	installations, err := discoverVisualStudioInstallations(
		context.Background(),
		probe.NewRunner(),
		msvc.options.config,
	)
	if err != nil || len(installations) == 0 {
		t.Fatalf("production vswhere stage = %#v, %v", installations, err)
	}
	toolsetVersion, err := latestMSVCToolset(
		context.Background(),
		installations[0].Path,
	)
	if err != nil {
		t.Fatalf("production toolset stage error = %v", err)
	}
	captured, err := captureMSVCContext(
		context.Background(),
		msvc.options,
		installations[0],
		workspace.ToolchainConfig{
			Family:             string(FamilyMSVC),
			InstallationID:     installations[0].ID,
			ToolsetVersion:     toolsetVersion,
			HostArchitecture:   msvc.options.config.HostArchitecture,
			TargetArchitecture: msvc.options.config.HostArchitecture,
		},
	)
	if err != nil {
		t.Fatalf("production VsDevCmd capture stage error = %v", err)
	}
	if _, err := msvc.probeContext(context.Background(), captured); err != nil {
		t.Fatalf("production compiler/generator stage error = %v", err)
	}
	instances, discoverErr := adapters[0].Discover(context.Background())
	if discoverErr != nil || len(instances) == 0 {
		t.Fatalf("production MSVC Discover() = %#v, %v", instances, discoverErr)
	}
	foundVisualStudioGenerator := false
	for _, instance := range instances {
		for _, generator := range instance.Generators {
			if generator == "Visual Studio 17 2022" ||
				generator == "Visual Studio 18 2026" {
				foundVisualStudioGenerator = true
			}
		}
	}
	if !foundVisualStudioGenerator {
		t.Fatalf("production MSVC instances lack Visual Studio generator: %#v", instances)
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

func TestMSVCAdapterPreservesCaptureCancellationWithoutDiscoveryIssues(t *testing.T) {
	t.Run("runner returned cancellation", func(t *testing.T) {
		fixture := newWindowsToolchainFixture(t)
		runner := newWindowsFakeRunner(fixture)
		runner.setError(
			fixture.cmd,
			fixture.vsDevCmdArgs("x64", "x64"),
			context.Canceled,
		)
		adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
		if err != nil {
			t.Fatalf("newWindowsAdapters() error = %v", err)
		}
		instances, discoverErr := adapters[0].Discover(context.Background())
		if instances != nil || !errors.Is(discoverErr, context.Canceled) {
			t.Fatalf("MSVC Discover(runner cancellation) = %#v, %v", instances, discoverErr)
		}
		var issueCarrier interface{ ToolchainIssues() []Issue }
		if errors.As(discoverErr, &issueCarrier) {
			t.Fatalf(
				"cancellation was converted to discovery issues: %#v",
				issueCarrier.ToolchainIssues(),
			)
		}
	})

	t.Run("tail cancellation", func(t *testing.T) {
		fixture := newWindowsToolchainFixture(t)
		runner := newWindowsFakeRunner(fixture)
		ctx, cancel := context.WithCancel(context.Background())
		options := fixture.options()
		options.afterEnvironmentValidation = cancel
		adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), options)
		if err != nil {
			t.Fatalf("newWindowsAdapters() error = %v", err)
		}
		instances, discoverErr := adapters[0].Discover(ctx)
		if instances != nil || !errors.Is(discoverErr, context.Canceled) {
			t.Fatalf("MSVC Discover(tail cancellation) = %#v, %v", instances, discoverErr)
		}
		var issueCarrier interface{ ToolchainIssues() []Issue }
		if errors.As(discoverErr, &issueCarrier) {
			t.Fatalf(
				"cancellation was converted to discovery issues: %#v",
				issueCarrier.ToolchainIssues(),
			)
		}
	})

	t.Run("tail deadline", func(t *testing.T) {
		fixture := newWindowsToolchainFixture(t)
		runner := newWindowsFakeRunner(fixture)
		ctx := &mutableWindowsContext{Context: context.Background()}
		options := fixture.options()
		options.afterEnvironmentValidation = func() {
			ctx.setError(context.DeadlineExceeded)
		}
		adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), options)
		if err != nil {
			t.Fatalf("newWindowsAdapters() error = %v", err)
		}
		instances, discoverErr := adapters[0].Discover(ctx)
		if instances != nil || !errors.Is(discoverErr, context.DeadlineExceeded) {
			t.Fatalf("MSVC Discover(tail deadline) = %#v, %v", instances, discoverErr)
		}
		var issueCarrier interface{ ToolchainIssues() []Issue }
		if errors.As(discoverErr, &issueCarrier) {
			t.Fatalf(
				"deadline was converted to discovery issues: %#v",
				issueCarrier.ToolchainIssues(),
			)
		}
	})
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
	got, err := latestMSVCToolset(context.Background(), installation)
	if err != nil {
		t.Fatalf("latestMSVCToolset() error = %v", err)
	}
	if got != "14.10.1" {
		t.Fatalf("latestMSVCToolset() = %q, want 14.10.1", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, canceledErr := latestMSVCToolset(ctx, installation); !errors.Is(
		canceledErr,
		context.Canceled,
	) {
		t.Fatalf("latestMSVCToolset(canceled) error = %v", canceledErr)
	}
}

func TestWindowsDiscoveryDeduplicatesIdenticalIssues(t *testing.T) {
	issue := Issue{
		Code:    "TOOLCHAIN_PROBE_FAILED",
		Message: "Windows toolchain candidate probe failed",
	}
	instances, err := finishWindowsDiscovery(nil, []Issue{issue, issue})
	if instances != nil || err == nil {
		t.Fatalf("finishWindowsDiscovery() = %#v, %v", instances, err)
	}
	var carrier issueCarrier
	if !errors.As(err, &carrier) {
		t.Fatalf("finishWindowsDiscovery() error does not carry issues: %v", err)
	}
	if got := carrier.ToolchainIssues(); !reflect.DeepEqual(got, []Issue{issue}) {
		t.Fatalf("deduplicated issues = %#v, want %#v", got, []Issue{issue})
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
	for _, directory := range []string{
		filepath.Join(fixture.sdk, "Include", "10.0.26100.0", "ucrt"),
		filepath.Join(fixture.sdk, "Include", "10.0.26100.0", "um"),
		filepath.Join(fixture.sdk, "Include", "10.0.26100.0", "shared"),
		filepath.Join(fixture.sdk, "Lib", "10.0.26100.0", "ucrt", "x64"),
		filepath.Join(fixture.sdk, "Lib", "10.0.26100.0", "um", "x64"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	secondRunner.setOutput(
		fixture.cmd,
		fixture.vsDevCmdArgs("x64", "x64"),
		successfulWindowsOutput(strings.ReplaceAll(
			fixture.environmentOutput("x64", "x64"),
			fixture.sdkVersion,
			"10.0.26100.0",
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

func TestMSVCAutomaticIDTracksAcceptedEnvironmentDirectoryIdentity(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	runner.setOutput(
		fixture.cmd,
		fixture.vsDevCmdArgs("x64", "x64"),
		successfulWindowsOutput(fixture.productionEnvironmentOutput("x64", "x64")),
	)
	firstAdapters, err := newWindowsAdapters(runner, nil, fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters(first) error = %v", err)
	}
	first, err := firstAdapters[0].Discover(context.Background())
	if err != nil || len(first) != 1 {
		t.Fatalf("first Discover() = %#v, %v", first, err)
	}
	accepted := filepath.Join(fixture.toolset, "lib", "x86", "store", "references")
	if err := os.RemoveAll(accepted); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(accepted, 0o755); err != nil {
		t.Fatal(err)
	}
	secondAdapters, err := newWindowsAdapters(runner, nil, fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters(second) error = %v", err)
	}
	second, err := secondAdapters[0].Discover(context.Background())
	if err != nil || len(second) != 1 {
		t.Fatalf("second Discover() = %#v, %v", second, err)
	}
	if first[0].ID == second[0].ID {
		t.Fatalf("automatic ID did not track accepted LIBPATH identity: %q", first[0].ID)
	}
}

func TestMSVCAutomaticIDTracksOptionalEnvironmentAndGeneratorCapability(t *testing.T) {
	t.Run("optional SDK include", func(t *testing.T) {
		fixture := newWindowsToolchainFixture(t)
		firstRunner := newWindowsFakeRunner(fixture)
		firstOutput := fixture.productionEnvironmentOutput("x64", "x64")
		firstRunner.setOutput(
			fixture.cmd,
			fixture.vsDevCmdArgs("x64", "x64"),
			successfulWindowsOutput(firstOutput),
		)
		firstAdapters, err := newWindowsAdapters(firstRunner, nil, fixture.options())
		if err != nil {
			t.Fatal(err)
		}
		first, err := firstAdapters[0].Discover(context.Background())
		if err != nil || len(first) != 1 {
			t.Fatalf("first Discover() = %#v, %v", first, err)
		}

		secondRunner := newWindowsFakeRunner(fixture)
		winrt := ";" + filepath.Join(
			fixture.sdk,
			"Include",
			fixture.sdkVersion,
			"winrt",
		)
		secondOutput := strings.Replace(firstOutput, winrt, "", 1)
		secondRunner.setOutput(
			fixture.cmd,
			fixture.vsDevCmdArgs("x64", "x64"),
			successfulWindowsOutput(secondOutput),
		)
		secondAdapters, err := newWindowsAdapters(secondRunner, nil, fixture.options())
		if err != nil {
			t.Fatal(err)
		}
		second, err := secondAdapters[0].Discover(context.Background())
		if err != nil || len(second) != 1 {
			t.Fatalf("second Discover() = %#v, %v", second, err)
		}
		if first[0].ID == second[0].ID {
			t.Fatalf("automatic ID ignored optional include removal: %q", first[0].ID)
		}
	})

	t.Run("Ninja capability", func(t *testing.T) {
		fixture := newWindowsToolchainFixture(t)
		firstRunner := newWindowsFakeRunner(fixture)
		firstRunner.setOutput(
			fixture.cmd,
			fixture.vsDevCmdArgs("x64", "x64"),
			successfulWindowsOutput(fixture.productionEnvironmentOutput("x64", "x64")),
		)
		firstAdapters, err := newWindowsAdapters(firstRunner, nil, fixture.options())
		if err != nil {
			t.Fatal(err)
		}
		first, err := firstAdapters[0].Discover(context.Background())
		if err != nil || len(first) != 1 {
			t.Fatalf("first Discover() = %#v, %v", first, err)
		}
		if err := os.Remove(fixture.ninja); err != nil {
			t.Fatal(err)
		}
		secondRunner := newWindowsFakeRunner(fixture)
		secondRunner.setOutput(
			fixture.cmd,
			fixture.vsDevCmdArgs("x64", "x64"),
			successfulWindowsOutput(fixture.productionEnvironmentOutput("x64", "x64")),
		)
		secondAdapters, err := newWindowsAdapters(secondRunner, nil, fixture.options())
		if err != nil {
			t.Fatal(err)
		}
		second, err := secondAdapters[0].Discover(context.Background())
		if err != nil || len(second) != 1 {
			t.Fatalf("second Discover() = %#v, %v", second, err)
		}
		if !reflect.DeepEqual(second[0].Generators, []string{"Visual Studio 17 2022"}) {
			t.Fatalf("generators without Ninja = %#v", second[0].Generators)
		}
		if first[0].ID == second[0].ID {
			t.Fatalf("automatic ID ignored Ninja capability removal: %q", first[0].ID)
		}
	})
}

func TestMSVCAutomaticIDNormalizesEquivalentEnvironmentPathVariants(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	firstOutput := fixture.productionEnvironmentOutput("x64", "x64")
	firstRunner := newWindowsFakeRunner(fixture)
	firstRunner.setOutput(
		fixture.cmd,
		fixture.vsDevCmdArgs("x64", "x64"),
		successfulWindowsOutput(firstOutput),
	)
	firstAdapters, err := newWindowsAdapters(firstRunner, nil, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	first, err := firstAdapters[0].Discover(context.Background())
	if err != nil || len(first) != 1 {
		t.Fatalf("first Discover() = %#v, %v", first, err)
	}

	auxiliary := filepath.Join(fixture.installation, "VC", "Auxiliary", "VS", "include")
	equivalent := strings.Replace(
		firstOutput,
		`"`+strings.ToUpper(auxiliary)+`"`,
		auxiliary,
		1,
	)
	equivalent = strings.ReplaceAll(equivalent, ";\r\n", "\r\n")
	secondRunner := newWindowsFakeRunner(fixture)
	secondRunner.setOutput(
		fixture.cmd,
		fixture.vsDevCmdArgs("x64", "x64"),
		successfulWindowsOutput(equivalent),
	)
	secondAdapters, err := newWindowsAdapters(secondRunner, nil, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondAdapters[0].Discover(context.Background())
	if err != nil || len(second) != 1 {
		t.Fatalf("second Discover() = %#v, %v", second, err)
	}
	if first[0].ID != second[0].ID {
		t.Fatalf("equivalent path variants changed ID: %q != %q", first[0].ID, second[0].ID)
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

func TestWindowsDiscoveryRejectsFixedBaseDirectoryReplacement(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	outside := filepath.Join(fixture.root, "outside-program-data")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(fixture.programData); err != nil {
		t.Fatal(err)
	}
	createWindowsToolchainJunction(t, fixture.programData, outside)
	instances, discoverErr := adapters[0].Discover(context.Background())
	if len(instances) != 0 || discoverErr == nil {
		t.Fatalf(
			"MSVC Discover(replaced ProgramData) = %#v, %v, want rejection",
			instances,
			discoverErr,
		)
	}
	if runner.findCall(fixture.vswhere, vswhereArguments()) != nil {
		t.Fatal("MSVC discovery probed vswhere after fixed base root replacement")
	}
}

func TestMSVCRejectsIntermediateDirectoryJunctionEscapes(t *testing.T) {
	t.Run("compiler and linker leave toolset", func(t *testing.T) {
		fixture := newWindowsToolchainFixture(t)
		binaryDirectory := filepath.Dir(fixture.cl)
		if err := os.RemoveAll(binaryDirectory); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(fixture.root, "outside-toolset-bin")
		outsideCL := filepath.Join(outside, "cl.exe")
		outsideLink := filepath.Join(outside, "link.exe")
		writeWindowsTool(t, outsideCL)
		writeWindowsTool(t, outsideLink)
		createWindowsToolchainJunction(t, binaryDirectory, outside)

		runner := newWindowsFakeRunner(fixture)
		runner.setOutput(
			outsideCL,
			[]string{"/Bv"},
			successfulWindowsOutput(
				"Microsoft (R) C/C++ Optimizing Compiler Version 19.40.33811 for x64\r\n",
			),
		)
		runner.setOutput(
			outsideLink,
			[]string{"/?"},
			successfulWindowsOutput(
				"Microsoft (R) Incremental Linker Version 14.40.33811.0\r\n",
			),
		)
		adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
		if err != nil {
			t.Fatalf("newWindowsAdapters() error = %v", err)
		}
		instances, discoverErr := adapters[0].Discover(context.Background())
		if len(instances) != 0 || discoverErr == nil {
			t.Fatalf("MSVC Discover(tool junction escape) = %#v, %v", instances, discoverErr)
		}
	})

	t.Run("VsDevCmd leaves installation", func(t *testing.T) {
		fixture := newWindowsToolchainFixture(t)
		toolsDirectory := filepath.Dir(fixture.vsDevCmd)
		if err := os.RemoveAll(toolsDirectory); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(fixture.root, "outside-vsdevcmd")
		outsideVsDevCmd := filepath.Join(outside, "VsDevCmd.bat")
		writeWindowsTool(t, outsideVsDevCmd)
		createWindowsToolchainJunction(t, toolsDirectory, outside)

		runner := newWindowsFakeRunner(fixture)
		args, err := buildVsDevCmdArguments(outsideVsDevCmd, MSVCConfig{
			ToolsetVersion:     fixture.toolsetVersion,
			HostArchitecture:   "x64",
			TargetArchitecture: "x64",
		})
		if err != nil {
			t.Fatal(err)
		}
		runner.setOutput(
			fixture.cmd,
			args,
			successfulWindowsOutput(fixture.environmentOutput("x64", "x64")),
		)
		adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
		if err != nil {
			t.Fatalf("newWindowsAdapters() error = %v", err)
		}
		instances, discoverErr := adapters[0].Discover(context.Background())
		if len(instances) != 0 || discoverErr == nil {
			t.Fatalf("MSVC Discover(VsDevCmd escape) = %#v, %v", instances, discoverErr)
		}
	})

	t.Run("MSBuild leaves installation", func(t *testing.T) {
		fixture := newWindowsToolchainFixture(t)
		msbuildDirectory := filepath.Dir(fixture.msbuild)
		if err := os.RemoveAll(msbuildDirectory); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(fixture.root, "outside-msbuild")
		outsideMSBuild := filepath.Join(outside, "MSBuild.exe")
		writeWindowsTool(t, outsideMSBuild)
		createWindowsToolchainJunction(t, msbuildDirectory, outside)

		runner := newWindowsFakeRunner(fixture)
		runner.setOutput(
			outsideMSBuild,
			[]string{"-version", "-nologo"},
			successfulWindowsOutput("17.10.4\r\n"),
		)
		adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
		if err != nil {
			t.Fatalf("newWindowsAdapters() error = %v", err)
		}
		instances, discoverErr := adapters[0].Discover(context.Background())
		if discoverErr != nil || len(instances) != 1 {
			t.Fatalf("MSVC Discover(MSBuild escape) = %#v, %v", instances, discoverErr)
		}
		if !reflect.DeepEqual(instances[0].Generators, []string{"Ninja"}) {
			t.Fatalf(
				"MSVC generators with escaped MSBuild = %#v, want only Ninja",
				instances[0].Generators,
			)
		}
		if runner.findCall(outsideMSBuild, []string{"-version", "-nologo"}) != nil {
			t.Fatal("MSVC adapter probed MSBuild outside canonical installation")
		}
	})
}

func TestMSVCRejectsIncompleteOrEscapedBuildEnvironment(t *testing.T) {
	tests := map[string]func(*testing.T, *windowsToolchainFixture, *windowsFakeRunner){
		"empty SDK root": func(
			t *testing.T,
			fixture *windowsToolchainFixture,
			runner *windowsFakeRunner,
		) {
			output := strings.Replace(
				fixture.environmentOutput("x64", "x64"),
				"WindowsSdkDir="+fixture.sdk+string(filepath.Separator),
				"WindowsSdkDir=",
				1,
			)
			runner.setOutput(
				fixture.cmd,
				fixture.vsDevCmdArgs("x64", "x64"),
				successfulWindowsOutput(output),
			)
		},
		"missing SDK version tree": func(
			t *testing.T,
			fixture *windowsToolchainFixture,
			_ *windowsFakeRunner,
		) {
			if err := os.RemoveAll(filepath.Join(fixture.sdk, "Include", fixture.sdkVersion)); err != nil {
				t.Fatal(err)
			}
		},
		"missing required INCLUDE entry": func(
			_ *testing.T,
			fixture *windowsToolchainFixture,
			runner *windowsFakeRunner,
		) {
			missing := ";" + filepath.Join(
				fixture.sdk, "Include", fixture.sdkVersion, "shared",
			)
			output := strings.Replace(
				fixture.environmentOutput("x64", "x64"),
				missing,
				"",
				1,
			)
			runner.setOutput(
				fixture.cmd,
				fixture.vsDevCmdArgs("x64", "x64"),
				successfulWindowsOutput(output),
			)
		},
		"missing required LIB entry": func(
			_ *testing.T,
			fixture *windowsToolchainFixture,
			runner *windowsFakeRunner,
		) {
			missing := ";" + filepath.Join(
				fixture.sdk, "Lib", fixture.sdkVersion, "um", "x64",
			)
			output := strings.Replace(
				fixture.environmentOutput("x64", "x64"),
				missing,
				"",
				1,
			)
			runner.setOutput(
				fixture.cmd,
				fixture.vsDevCmdArgs("x64", "x64"),
				successfulWindowsOutput(output),
			)
		},
		"INCLUDE escapes verified roots": func(
			t *testing.T,
			fixture *windowsToolchainFixture,
			runner *windowsFakeRunner,
		) {
			outside := filepath.Join(fixture.root, "outside-include")
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			output := strings.Replace(
				fixture.environmentOutput("x64", "x64"),
				"INCLUDE="+filepath.Join(fixture.toolset, "include"),
				"INCLUDE="+outside,
				1,
			)
			runner.setOutput(
				fixture.cmd,
				fixture.vsDevCmdArgs("x64", "x64"),
				successfulWindowsOutput(output),
			)
		},
		"LIB escapes verified roots": func(
			t *testing.T,
			fixture *windowsToolchainFixture,
			runner *windowsFakeRunner,
		) {
			outside := filepath.Join(fixture.root, "outside-lib")
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			output := strings.Replace(
				fixture.environmentOutput("x64", "x64"),
				"LIB="+filepath.Join(fixture.toolset, "lib", "x64"),
				"LIB="+outside,
				1,
			)
			runner.setOutput(
				fixture.cmd,
				fixture.vsDevCmdArgs("x64", "x64"),
				successfulWindowsOutput(output),
			)
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newWindowsToolchainFixture(t)
			runner := newWindowsFakeRunner(fixture)
			mutate(t, fixture, runner)
			adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), fixture.options())
			if err != nil {
				t.Fatalf("newWindowsAdapters() error = %v", err)
			}
			instances, discoverErr := adapters[0].Discover(context.Background())
			if len(instances) != 0 || discoverErr == nil {
				t.Fatalf(
					"MSVC Discover(invalid build environment) = %#v, %v, want rejection",
					instances,
					discoverErr,
				)
			}
		})
	}
}

func TestMSVCRejectsSDKDirectoryReplacementAfterEnvironmentValidation(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	replaced := filepath.Join(
		fixture.sdk,
		"Include",
		fixture.sdkVersion,
		"shared",
	)
	outside := filepath.Join(fixture.root, "outside-sdk-shared")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	options := fixture.options()
	options.afterEnvironmentValidation = func() {
		if err := os.RemoveAll(replaced); err != nil {
			t.Fatal(err)
		}
		createWindowsToolchainJunction(t, replaced, outside)
	}
	adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), options)
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	instances, discoverErr := adapters[0].Discover(context.Background())
	if len(instances) != 0 || discoverErr == nil {
		t.Fatalf(
			"MSVC Discover(replaced SDK directory) = %#v, %v, want rejection",
			instances,
			discoverErr,
		)
	}
}

func TestMSVCRejectsAcceptedLIBPATHReplacementAfterEnvironmentValidation(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	runner.setOutput(
		fixture.cmd,
		fixture.vsDevCmdArgs("x64", "x64"),
		successfulWindowsOutput(fixture.productionEnvironmentOutput("x64", "x64")),
	)
	replaced := filepath.Join(fixture.toolset, "lib", "x86", "store", "references")
	options := fixture.options()
	options.afterEnvironmentValidation = func() {
		if err := os.RemoveAll(replaced); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(replaced, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	adapters, err := newWindowsAdapters(runner, fixture.manualMSVC(), options)
	if err != nil {
		t.Fatalf("newWindowsAdapters() error = %v", err)
	}
	instances, discoverErr := adapters[0].Discover(context.Background())
	if len(instances) != 0 || discoverErr == nil {
		t.Fatalf(
			"MSVC Discover(replaced LIBPATH directory) = %#v, %v, want rejection",
			instances,
			discoverErr,
		)
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
	netFXSDK        string
	llvmRoot        string
	clang           string
	lld             string
	llvmProfdata    string
	llvmCov         string
	baseEnvironment []string
	programData     string
	vsMetadata      string
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
		netFXSDK:        filepath.Join(root, "Windows Kits", "NETFXSDK", "4.8"),
		llvmRoot:        filepath.Join(root, "LLVM", "bin"),
		programData:     filepath.Join(root, "ProgramData"),
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
	fixture.vsMetadata = filepath.Join(
		fixture.programData,
		"Microsoft",
		"VisualStudio",
		"Packages",
		"_Instances",
	)
	fixture.baseEnvironment = []string{
		"ComSpec=" + fixture.cmd,
		"Path=" + filepath.Dir(fixture.cmd) + ";" + filepath.Join(root, "Windows"),
		"ProgramData=" + fixture.programData,
		"SystemRoot=" + filepath.Join(root, "Windows"),
		"TEMP=" + filepath.Join(root, "Temp"),
		"TMP=" + filepath.Join(root, "Temp"),
	}
	for _, directory := range []string{
		fixture.sdk,
		fixture.programData,
		fixture.vsMetadata,
		filepath.Join(root, "Temp"),
		filepath.Join(fixture.toolset, "include"),
		filepath.Join(fixture.toolset, "lib", "x64"),
		filepath.Join(fixture.sdk, "Include", fixture.sdkVersion, "ucrt"),
		filepath.Join(fixture.sdk, "Include", fixture.sdkVersion, "um"),
		filepath.Join(fixture.sdk, "Include", fixture.sdkVersion, "shared"),
		filepath.Join(fixture.sdk, "Include", fixture.sdkVersion, "winrt"),
		filepath.Join(fixture.sdk, "Include", fixture.sdkVersion, "cppwinrt"),
		filepath.Join(fixture.sdk, "Lib", fixture.sdkVersion, "ucrt", "x64"),
		filepath.Join(fixture.sdk, "Lib", fixture.sdkVersion, "um", "x64"),
		filepath.Join(fixture.installation, "VC", "Auxiliary", "VS", "include"),
		filepath.Join(fixture.toolset, "lib", "x86", "store", "references"),
		filepath.Join(fixture.sdk, "UnionMetadata", fixture.sdkVersion),
		filepath.Join(fixture.sdk, "References", fixture.sdkVersion),
		filepath.Join(fixture.sdk, "bin", fixture.sdkVersion, "x64"),
		filepath.Join(
			fixture.netFXSDK,
			"References",
			"CommonConfiguration",
			"Neutral",
		),
		filepath.Join(
			root,
			"Windows",
			"Microsoft.NET",
			"Framework64",
			"v4.0.30319",
		),
		filepath.Join(root, "untrusted inherited"),
	} {
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
		VSWherePath:                fixture.vswhere,
		CmdPath:                    fixture.cmd,
		NinjaPath:                  fixture.ninja,
		LLVMRoot:                   fixture.llvmRoot,
		BaseEnvironment:            append([]string(nil), fixture.baseEnvironment...),
		VSInstallationMetadataPath: fixture.vsMetadata,
		HostArchitecture:           "x64",
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
	binaryDirectory := filepath.Join(fixture.toolset, "bin", "Host"+host, target)
	toolsetInclude := filepath.Join(fixture.toolset, "include")
	toolsetLibrary := filepath.Join(fixture.toolset, "lib", target)
	sdkIncludeRoot := filepath.Join(fixture.sdk, "Include", fixture.sdkVersion)
	sdkLibraryRoot := filepath.Join(fixture.sdk, "Lib", fixture.sdkVersion)
	return strings.Join([]string{
		"Path=" + binaryDirectory + ";" + filepath.Dir(fixture.ninja),
		"INCLUDE=" + strings.Join([]string{
			toolsetInclude,
			filepath.Join(sdkIncludeRoot, "ucrt"),
			filepath.Join(sdkIncludeRoot, "um"),
			filepath.Join(sdkIncludeRoot, "shared"),
		}, ";"),
		"LIB=" + strings.Join([]string{
			toolsetLibrary,
			filepath.Join(sdkLibraryRoot, "ucrt", target),
			filepath.Join(sdkLibraryRoot, "um", target),
		}, ";"),
		"LIBPATH=" + toolsetLibrary,
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

func (fixture *windowsToolchainFixture) productionEnvironmentOutput(host, target string) string {
	binaryDirectory := filepath.Join(fixture.toolset, "bin", "Host"+host, target)
	toolsetLibrary := filepath.Join(fixture.toolset, "lib", target)
	sdkIncludeRoot := filepath.Join(fixture.sdk, "Include", fixture.sdkVersion)
	sdkLibraryRoot := filepath.Join(fixture.sdk, "Lib", fixture.sdkVersion)
	untrusted := filepath.Join(fixture.root, "untrusted inherited")
	include := []string{
		filepath.Join(fixture.toolset, "include"),
		`"` + strings.ToUpper(
			filepath.Join(fixture.installation, "VC", "Auxiliary", "VS", "include"),
		) + `"`,
		filepath.Join(sdkIncludeRoot, "ucrt"),
		filepath.Join(sdkIncludeRoot, "um"),
		filepath.Join(sdkIncludeRoot, "shared"),
		filepath.Join(sdkIncludeRoot, "winrt"),
		filepath.Join(sdkIncludeRoot, "cppwinrt"),
		untrusted,
		"",
	}
	libraries := []string{
		toolsetLibrary,
		filepath.Join(sdkLibraryRoot, "ucrt", target),
		filepath.Join(sdkLibraryRoot, "um", target),
		untrusted,
		"",
	}
	libPath := []string{
		toolsetLibrary,
		filepath.Join(fixture.toolset, "lib", "x86", "store", "references"),
		filepath.Join(fixture.sdk, "UnionMetadata", fixture.sdkVersion),
		filepath.Join(fixture.sdk, "References", fixture.sdkVersion),
		filepath.Join(
			fixture.netFXSDK,
			"References",
			"CommonConfiguration",
			"Neutral",
		),
		filepath.Join(
			fixture.root,
			"Windows",
			"Microsoft.NET",
			"Framework64",
			"v4.0.30319",
		),
		untrusted,
		"",
	}
	path := []string{
		binaryDirectory,
		filepath.Join(fixture.installation, "Common7", "Tools"),
		filepath.Join(fixture.sdk, "bin", fixture.sdkVersion, target),
		filepath.Join(fixture.root, "Windows", "System32"),
		filepath.Join(fixture.root, "Windows"),
		untrusted,
		"",
	}
	return strings.Join([]string{
		"Path=" + strings.Join(path, ";"),
		"INCLUDE=" + strings.Join(include, ";"),
		"LIB=" + strings.Join(libraries, ";"),
		"LIBPATH=" + strings.Join(libPath, ";"),
		"VCToolsInstallDir=" + fixture.toolset + string(filepath.Separator),
		"VCToolsVersion=" + fixture.toolsetVersion,
		"WindowsSdkDir=" + fixture.sdk + string(filepath.Separator),
		"WindowsSDKVersion=" + fixture.sdkVersion + string(filepath.Separator),
		"NETFXSDKDir=" + fixture.netFXSDK,
		"VSCMD_ARG_HOST_ARCH=" + host,
		"VSCMD_ARG_TGT_ARCH=" + target,
	}, "\r\n") + "\r\n"
}

func windowsPathListContains(values map[string]string, want string) bool {
	for _, key := range []string{"INCLUDE", "LIB", "LIBPATH", "PATH"} {
		for _, path := range filepath.SplitList(values[key]) {
			if identityPath(strings.TrimSpace(path)) == identityPath(want) {
				return true
			}
		}
	}
	return false
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

func createWindowsToolchainJunction(t *testing.T, link, target string) {
	t.Helper()
	command := exec.Command(os.Getenv("ComSpec"), "/d", "/s", "/c", "mklink", "/J", link, target)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Skipf("directory junctions are unavailable: %v: %s", err, output)
	}
}

func createWindowsToolchainFileSymlink(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("file symbolic links are unavailable: %v", err)
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

type mutableWindowsContext struct {
	context.Context
	mu  sync.Mutex
	err error
}

func (ctx *mutableWindowsContext) Err() error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if ctx.err != nil {
		return ctx.err
	}
	return ctx.Context.Err()
}

func (ctx *mutableWindowsContext) setError(err error) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.err = err
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
