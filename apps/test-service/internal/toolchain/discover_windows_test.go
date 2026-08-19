//go:build windows

package toolchain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
)

type stagedWindowsContext struct {
	context.Context
	mu        sync.Mutex
	once      sync.Once
	done      chan struct{}
	calls     int
	failAt    int
	err       error
	triggered bool
}

func newStagedWindowsContext(failAt int, err error) *stagedWindowsContext {
	return &stagedWindowsContext{
		Context: context.Background(),
		done:    make(chan struct{}),
		failAt:  failAt,
		err:     err,
	}
}

func (ctx *stagedWindowsContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *stagedWindowsContext) Err() error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if ctx.triggered {
		return ctx.err
	}
	ctx.calls++
	if ctx.calls < ctx.failAt {
		return nil
	}
	ctx.triggered = true
	ctx.once.Do(func() { close(ctx.done) })
	return ctx.err
}

func TestWindowsDefaultDiscoveryBuildsFixedMinimumOSEnvironment(t *testing.T) {
	untrustedPath := t.TempDir()
	t.Setenv("PATH", untrustedPath)
	options, err := defaultWindowsDiscoveryOptions()
	if err != nil {
		t.Fatalf("defaultWindowsDiscoveryOptions() error = %v", err)
	}
	systemRoot := os.Getenv("SystemRoot")
	want := map[string]string{
		"COMSPEC":     filepath.Join(systemRoot, "System32", "cmd.exe"),
		"PATH":        filepath.Join(systemRoot, "System32") + ";" + systemRoot,
		"PROGRAMDATA": os.Getenv("ProgramData"),
		"SYSTEMROOT":  systemRoot,
		"TEMP":        os.Getenv("TEMP"),
		"TMP":         os.Getenv("TMP"),
	}
	if got := windowsEnvironmentValues(options.BaseEnvironment); !reflect.DeepEqual(got, want) {
		t.Fatalf("default fixed environment = %#v, want %#v", got, want)
	}
	if strings.Contains(
		strings.ToLower(windowsEnvironmentValues(options.BaseEnvironment)["PATH"]),
		strings.ToLower(untrustedPath),
	) {
		t.Fatalf("default fixed environment inherited user PATH %q", untrustedPath)
	}
}

func TestWindowsInitialBaseDirectoryVerificationPreservesContextErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "cancellation", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWindowsToolchainFixture(t)
			runner := newWindowsFakeRunner(fixture)
			adapters, err := newWindowsAdapters(runner, nil, fixture.options())
			if err != nil {
				t.Fatal(err)
			}
			msvc := adapters[0].(*msvcAdapter)
			ctx := newStagedWindowsContext(3, test.err)
			installations, discoverErr := discoverVisualStudioInstallations(
				ctx,
				runner,
				msvc.options.config,
			)
			if installations != nil || !errors.Is(discoverErr, test.err) {
				t.Fatalf(
					"initial base verification = %#v, %v, want %v",
					installations,
					discoverErr,
					test.err,
				)
			}
			var carrier issueCarrier
			if errors.As(discoverErr, &carrier) {
				t.Fatalf(
					"initial base verification context error became issues: %#v",
					carrier.ToolchainIssues(),
				)
			}
			if runner.findCall(fixture.vswhere, vswhereArguments()) != nil {
				t.Fatal("initial base verification probed vswhere after context error")
			}
		})
	}
}

func TestWindowsInitialBaseDirectoryCancellationCreatesNoRegistryIssue(t *testing.T) {
	fixture := newWindowsToolchainFixture(t)
	runner := newWindowsFakeRunner(fixture)
	adapters, err := newWindowsAdapters(runner, nil, fixture.options())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(adapters[0])
	if err != nil {
		t.Fatal(err)
	}
	ctx := newStagedWindowsContext(5, context.Canceled)
	instances, issues := registry.Discover(ctx)
	if instances != nil || issues != nil {
		t.Fatalf(
			"Registry Discover(initial cancellation) = %#v, %#v, want no ordinary issue",
			instances,
			issues,
		)
	}
}

func TestWindowsVSWhereUsesOnlyFixedArguments(t *testing.T) {
	want := []string{
		"-all",
		"-products",
		"*",
		"-requires",
		"Microsoft.VisualStudio.Component.VC.Tools.x86.x64",
		"-format",
		"json",
		"-utf8",
	}
	got := vswhereArguments()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("vswhereArguments() = %#v, want %#v", got, want)
	}
	got[0] = "-latest"
	if next := vswhereArguments(); next[0] != "-all" {
		t.Fatalf("vswhereArguments() leaked caller mutation: %#v", next)
	}
}

func TestWindowsVSWhereDistinguishesExpectedMetadataFromNoInstallation(t *testing.T) {
	for _, test := range []struct {
		name             string
		metadataExpected bool
		wantError        bool
	}{
		{name: "metadata expected", metadataExpected: true, wantError: true},
		{name: "no metadata", metadataExpected: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWindowsToolchainFixture(t)
			runner := newWindowsFakeRunner(fixture)
			runner.setOutput(
				fixture.vswhere,
				vswhereArguments(),
				successfulWindowsOutput("[]"),
			)
			options := fixture.options()
			options.VSInstallationMetadataExpected = test.metadataExpected
			adapters, err := newWindowsAdapters(runner, nil, options)
			if err != nil {
				t.Fatalf("newWindowsAdapters() error = %v", err)
			}
			instances, discoverErr := adapters[0].Discover(context.Background())
			if len(instances) != 0 || (discoverErr != nil) != test.wantError {
				t.Fatalf(
					"MSVC Discover(empty vswhere) = %#v, %v, want error %t",
					instances,
					discoverErr,
					test.wantError,
				)
			}
			if discoverErr != nil && strings.Contains(
				discoverErr.Error(),
				fixture.programData,
			) {
				t.Fatalf("empty metadata issue leaked ProgramData path: %v", discoverErr)
			}
		})
	}
}

func TestWindowsProductionVSWhereFindsExpectedFixedMetadata(t *testing.T) {
	options, err := defaultWindowsDiscoveryOptions()
	if err != nil {
		t.Skipf("fixed Windows discovery environment is unavailable: %v", err)
	}
	if !options.VSInstallationMetadataExpected {
		t.Skip("fixed Visual Studio installation metadata is not present")
	}
	if _, err := os.Stat(options.VSWherePath); err != nil {
		t.Skipf("fixed vswhere is unavailable: %v", err)
	}
	runner := &windowsProductionProbeRunner{delegate: probe.NewRunner()}
	adapters, err := newWindowsAdapters(runner, nil, options)
	if err != nil {
		t.Fatalf("newWindowsAdapters(production) error = %v", err)
	}
	msvc, ok := adapters[0].(*msvcAdapter)
	if !ok {
		t.Fatalf("production MSVC adapter = %T", adapters[0])
	}
	installations, discoverErr := discoverVisualStudioInstallations(
		context.Background(),
		runner,
		msvc.options.config,
	)
	if discoverErr != nil && runner.timedOut(discoverErr) {
		t.Skipf("fixed vswhere probe exceeded its bounded budget: %v", discoverErr)
	}
	if discoverErr != nil || len(installations) == 0 {
		t.Fatalf(
			"production vswhere installations = %#v, %v",
			installations,
			discoverErr,
		)
	}
}

func TestWindowsVSWhereStrictlyBoundsAndValidatesInstallations(t *testing.T) {
	valid := `[{
		"instanceId":"visual-studio-17",
		"installationPath":"C:\\Program Files\\Microsoft Visual Studio\\2022\\BuildTools",
		"installationVersion":"17.10.4",
		"isComplete":true,
		"isLaunchable":true
	}]`
	installations, err := parseVSWhereOutput([]byte(valid))
	if err != nil {
		t.Fatalf("parseVSWhereOutput(valid) error = %v", err)
	}
	if len(installations) != 1 ||
		installations[0].ID != "visual-studio-17" ||
		installations[0].Path != `C:\Program Files\Microsoft Visual Studio\2022\BuildTools` ||
		installations[0].Version != "17.10.4" {
		t.Fatalf("parseVSWhereOutput(valid) = %#v", installations)
	}
	extended := `[{
		"instanceId":"visual-studio-18",
		"installationPath":"C:\\Program Files\\Microsoft Visual Studio\\18\\Enterprise",
		"installationVersion":"18.6.11819.183",
		"isComplete":true,
		"isLaunchable":true,
		"installChannelUri":"https://aka.ms/vs/18/release/channel",
		"installCatalogUri":"https://aka.ms/vs/18/release/catalog",
		"futureMetadata":{"command":"ignored","values":[1,true,"bounded"]}
	}]`
	installations, err = parseVSWhereOutput([]byte(extended))
	if err != nil {
		t.Fatalf("parseVSWhereOutput(extended metadata) error = %v", err)
	}
	if len(installations) != 1 ||
		installations[0].ID != "visual-studio-18" ||
		installations[0].Path != `C:\Program Files\Microsoft Visual Studio\18\Enterprise` ||
		installations[0].Version != "18.6.11819.183" {
		t.Fatalf("parseVSWhereOutput(extended metadata) = %#v", installations)
	}

	cases := map[string]string{
		"malformed":               `[`,
		"trailing value":          valid + `{}`,
		"duplicate key":           `[{"instanceId":"a","instanceId":"b","installationPath":"C:\\VS","installationVersion":"17.0","isComplete":true,"isLaunchable":true}]`,
		"duplicate extension key": `[{"instanceId":"a","installationPath":"C:\\VS","installationVersion":"17.0","isComplete":true,"isLaunchable":true,"futureMetadata":{"value":1,"VALUE":2}}]`,
		"missing id":              `[{"installationPath":"C:\\VS","installationVersion":"17.0","isComplete":true,"isLaunchable":true}]`,
		"missing state":           `[{"instanceId":"a","installationPath":"C:\\VS","installationVersion":"17.0"}]`,
		"duplicate install":       `[{"instanceId":"a","installationPath":"C:\\VS1","installationVersion":"17.0","isComplete":true,"isLaunchable":true},{"instanceId":"A","installationPath":"C:\\VS2","installationVersion":"17.0","isComplete":true,"isLaunchable":true}]`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := parseVSWhereOutput([]byte(input)); err == nil {
				t.Fatalf("parseVSWhereOutput() = %#v, want error", got)
			}
		})
	}

	oversized := make([]byte, maxVSWhereOutputBytes+1)
	for index := range oversized {
		oversized[index] = ' '
	}
	if _, err := parseVSWhereOutput(oversized); err == nil {
		t.Fatal("parseVSWhereOutput() accepted output limit + 1")
	}
}

func TestWindowsVSWhereIgnoresIncompleteOrUnlaunchableInstallations(t *testing.T) {
	input := `[
		{"instanceId":"incomplete","installationPath":"C:\\VS1","installationVersion":"17.0","isComplete":false,"isLaunchable":true},
		{"instanceId":"unlaunchable","installationPath":"C:\\VS2","installationVersion":"17.0","isComplete":true,"isLaunchable":false},
		{"instanceId":"ready","installationPath":"C:\\VS3","installationVersion":"17.0","isComplete":true,"isLaunchable":true}
	]`
	installations, err := parseVSWhereOutput([]byte(input))
	if err != nil {
		t.Fatalf("parseVSWhereOutput() error = %v", err)
	}
	if len(installations) != 1 || installations[0].ID != "ready" {
		t.Fatalf("parseVSWhereOutput() = %#v, want only ready installation", installations)
	}
}

func TestWindowsVSWhereRejectsInstallationCountAndFieldLimitPlusOne(t *testing.T) {
	entries := make([]string, maxVSWhereInstallations+1)
	for index := range entries {
		entries[index] = `{"instanceId":"vs-` + strings.Repeat("x", index%2) +
			string(rune('a'+index)) +
			`","installationPath":"C:\\VS\\` + string(rune('a'+index)) +
			`","installationVersion":"17.0","isComplete":true,"isLaunchable":true}`
	}
	if _, err := parseVSWhereOutput([]byte("[" + strings.Join(entries, ",") + "]")); err == nil {
		t.Fatal("parseVSWhereOutput() accepted installation count limit + 1")
	}

	tooLongID := strings.Repeat("x", maxVSWhereIDBytes+1)
	input := `[{"instanceId":"` + tooLongID +
		`","installationPath":"C:\\VS","installationVersion":"17.0","isComplete":true,"isLaunchable":true}]`
	if _, err := parseVSWhereOutput([]byte(input)); err == nil {
		t.Fatal("parseVSWhereOutput() accepted instance ID limit + 1")
	}
}

func TestWindowsVSWhereAcceptsKnownOfficialFieldsAndFourPartVersion(t *testing.T) {
	input := `[{
		"instanceId":"visual-studio-18",
		"installDate":"2026-07-01T00:00:00Z",
		"installationName":"VisualStudio/18.0.0+12345",
		"installationPath":"C:\\Program Files\\Microsoft Visual Studio\\18\\BuildTools",
		"installationVersion":"18.0.12345.67",
		"productId":"Microsoft.VisualStudio.Product.BuildTools",
		"productPath":"C:\\Program Files\\Microsoft Visual Studio\\18\\BuildTools\\Common7\\IDE\\devenv.exe",
		"state":4294967295,
		"isComplete":true,
		"isLaunchable":true,
		"isPrerelease":false,
		"isRebootRequired":false,
		"displayName":"Visual Studio Build Tools 2026",
		"description":"Build Tools",
		"channelId":"VisualStudio.18.Release",
		"channelUri":"https://aka.ms/vs/18/release/channel",
		"enginePath":"C:\\Program Files (x86)\\Microsoft Visual Studio\\Installer\\resources\\app\\ServiceHub\\Services\\Microsoft.VisualStudio.Setup.Service",
		"installedChannelId":"VisualStudio.18.Release",
		"installedChannelUri":"https://aka.ms/vs/18/release/channel",
		"releaseNotes":"https://learn.microsoft.com/visualstudio/releases/18/release-notes",
		"resolvedInstallationPath":"C:\\Program Files\\Microsoft Visual Studio\\18\\BuildTools",
		"thirdPartyNotices":"https://go.microsoft.com/fwlink/?LinkId=000000",
		"updateDate":"2026-07-01T00:00:00Z",
		"catalog":{"buildBranch":"d18.0","productLineVersion":"18"},
		"properties":{"channelManifestId":"VisualStudio.18.Release/18.0.0+12345"}
	}]`
	installations, err := parseVSWhereOutput([]byte(input))
	if err != nil {
		t.Fatalf("parseVSWhereOutput(official shape) error = %v", err)
	}
	if len(installations) != 1 || installations[0].Version != "18.0.12345.67" {
		t.Fatalf("parseVSWhereOutput(official shape) = %#v", installations)
	}
}

func TestWindowsToolSnapshotAcceptsCanonicalRegularFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "工具", "vswhere.exe")
	writeWindowsTool(t, path)
	canonical, err := canonicalWindowsFile(path)
	if err != nil {
		t.Fatalf("canonicalWindowsFile() error = %v", err)
	}
	if info, err := os.Stat(canonical); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("canonical file = %q, info = %#v, error = %v", canonical, info, err)
	}
	snapshot, err := openWindowsToolSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("openWindowsToolSnapshot() error = %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("snapshot.Close() error = %v", err)
	}
}
