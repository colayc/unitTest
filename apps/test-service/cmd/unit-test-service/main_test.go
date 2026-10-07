package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	serviceruntime "unit-test-ide.local/test-service/internal/runtime"
)

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp(".", ".service-test-tmp-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("TEMP", root)
	_ = os.Setenv("TMP", root)
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

func TestProductionRuntimeConfigEnablesAtomicGenerationProvider(t *testing.T) {
	config := productionRuntimeConfig(serviceruntime.Config{})
	if config.ProductionGenerationFactory == nil {
		t.Fatal("production generation factory is disabled")
	}
}

func TestRunPrepareTokenFileModeCreatesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--prepare-token-file", path}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("run code = %d, stderr = %q", code, stderr.String())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("prepared file size = %d, want 0", info.Size())
	}
}

func TestRunRejectsMixedPreparationAndServiceModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--prepare-token-file", path,
		"--endpoint", "unused-endpoint",
		"--token-file", "unused-token",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("stderr = %q, want combination error", stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mixed mode created token path: %v", err)
	}
}

func TestRunRejectsEmptyEndpointFlagInPreparationMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--prepare-token-file", path, "--endpoint="}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("stderr = %q, want combination error", stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mixed mode created token path: %v", err)
	}
}

func TestRunRejectsEmptyTokenFileFlagInPreparationMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--prepare-token-file", path, "--token-file="}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("stderr = %q, want combination error", stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mixed mode created token path: %v", err)
	}
}

func TestRunRejectsExplicitEmptyPreparationPath(t *testing.T) {
	tests := []struct {
		name        string
		serviceArgs []string
		wantError   string
	}{
		{name: "preparation only", wantError: "--prepare-token-file requires a non-empty path"},
		{name: "with endpoint", serviceArgs: []string{"--endpoint", "unused-endpoint"}, wantError: "cannot be combined"},
		{name: "with token file", serviceArgs: []string{"--token-file", "TOKEN_FILE"}, wantError: "cannot be combined"},
		{name: "with both service flags", serviceArgs: []string{"--endpoint", "unused-endpoint", "--token-file", "TOKEN_FILE"}, wantError: "cannot be combined"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			tokenPath := filepath.Join(directory, "service-token")
			if err := os.WriteFile(tokenPath, []byte("0123456789abcdef"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := prepareTokenFileForTest(tokenPath); err != nil {
				t.Fatal(err)
			}

			args := []string{"--prepare-token-file="}
			for _, arg := range test.serviceArgs {
				if arg == "TOKEN_FILE" {
					arg = tokenPath
				}
				args = append(args, arg)
			}

			var stdout, stderr bytes.Buffer
			if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 2 {
				t.Fatalf("run code = %d, want 2; stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), test.wantError) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantError)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			contents, err := os.ReadFile(tokenPath)
			if err != nil {
				t.Fatalf("service token was consumed: %v", err)
			}
			if string(contents) != "0123456789abcdef" {
				t.Fatalf("service token contents = %q, want unchanged", contents)
			}
		})
	}
}

func TestRunRejectsPositionalArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unexpected"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("run code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "positional arguments") {
		t.Fatalf("stderr = %q, want positional argument error", stderr.String())
	}
}

func TestRunServiceModeRequiresDataDirBeforeConsumingToken(t *testing.T) {
	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "token")
	if err := os.WriteFile(tokenPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareTokenFileForTest(tokenPath); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--endpoint", "unused-endpoint", "--token-file", tokenPath}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run code = %d, want 2; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--data-dir") || stdout.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if contents, err := os.ReadFile(tokenPath); err != nil || string(contents) != "0123456789abcdef" {
		t.Fatalf("token was consumed before required-flag validation: %q, %v", contents, err)
	}
}

func TestRunServiceModeRequiresWorkspaceRootBeforeConsumingToken(t *testing.T) {
	directory := t.TempDir()
	tokenPath := preparedServiceToken(t, directory)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--endpoint", "unused-endpoint", "--token-file", tokenPath,
		"--data-dir", filepath.Join(directory, "data"),
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--workspace-root") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if contents, err := os.ReadFile(tokenPath); err != nil || string(contents) != "0123456789abcdef" {
		t.Fatalf("token was consumed before workspace validation: %q, %v", contents, err)
	}
}

func TestRunServiceModeRequiresEveryProductBundleRootBeforeConsumingToken(t *testing.T) {
	directory := t.TempDir()
	bundleParent := filepath.Join(directory, "bundles")
	roots := map[string]string{
		"--cmake-bundle-root":    filepath.Join(bundleParent, "cmake"),
		"--coverage-bundle-root": filepath.Join(bundleParent, "coverage"),
		"--testgen-bundle-root":  filepath.Join(bundleParent, "testgen"),
	}
	for _, root := range roots {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for omitted := range roots {
		t.Run(omitted, func(t *testing.T) {
			tokenPath := preparedServiceToken(t, directory)
			args := []string{
				"--endpoint", "unused-endpoint", "--token-file", tokenPath,
				"--data-dir", filepath.Join(directory, "data"),
				"--workspace-root", directory,
			}
			for flagName, root := range roots {
				if flagName != omitted {
					args = append(args, flagName, root)
				}
			}
			var stdout, stderr bytes.Buffer
			if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 2 ||
				!strings.Contains(stderr.String(), "product bundle roots are required") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if contents, err := os.ReadFile(tokenPath); err != nil || string(contents) != "0123456789abcdef" {
				t.Fatalf("token was consumed before product bundle validation: %q, %v", contents, err)
			}
		})
	}
}

func TestRunTrustedWorkspaceRequiresExplicitBoolean(t *testing.T) {
	for _, args := range [][]string{
		{"--trusted-workspace"},
		{"--trusted-workspace=maybe"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 2 ||
			stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestRunRejectsWorkspaceAndCMakeFlagsInInternalModes(t *testing.T) {
	for _, extra := range [][]string{
		{"--workspace-root", t.TempDir()},
		{"--trusted-workspace=true"},
		{"--cmake-bundle-root", t.TempDir()},
		{"--coverage-bundle-root", t.TempDir()},
		{"--testgen-bundle-root", t.TempDir()},
		{"--dev-cmake-executable", os.Args[0]},
	} {
		args := append([]string{"--task-fixture", "success"}, extra...)
		var stdout, stderr bytes.Buffer
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 2 ||
			!strings.Contains(stderr.String(), "internal modes cannot be combined") {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestRunInvalidWorkspaceRootNeverCreatesListenerOrPrintsReady(t *testing.T) {
	directory := t.TempDir()
	tokenPath := preparedServiceToken(t, directory)
	workspacePath := filepath.Join(directory, "not-a-directory")
	if err := os.WriteFile(workspacePath, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := listenTransport
	listenerCalled := false
	listenTransport = func(string) (net.Listener, error) {
		listenerCalled = true
		return nil, errors.New("listener must not be called")
	}
	defer func() { listenTransport = previous }()

	var stdout, stderr bytes.Buffer
	args := []string{
		"--endpoint", "unused-endpoint", "--token-file", tokenPath,
		"--data-dir", filepath.Join(directory, "data"),
		"--workspace-root", workspacePath,
	}
	code := run(append(args, productBundleArgs(t)...), strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if listenerCalled || strings.Contains(stdout.String(), "READY") {
		t.Fatalf("listenerCalled=%v stdout=%q", listenerCalled, stdout.String())
	}
}

func TestRunUnsafeDataDirNeverCreatesListenerOrPrintsReady(t *testing.T) {
	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "token")
	if err := os.WriteFile(tokenPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareTokenFileForTest(tokenPath); err != nil {
		t.Fatal(err)
	}
	unsafePath := filepath.Join(directory, "not-a-directory")
	if err := os.WriteFile(unsafePath, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := listenTransport
	listenerCalled := false
	listenTransport = func(string) (net.Listener, error) {
		listenerCalled = true
		return nil, errors.New("listener must not be called")
	}
	defer func() { listenTransport = previous }()

	var stdout, stderr bytes.Buffer
	args := []string{
		"--endpoint", "unused-endpoint", "--token-file", tokenPath,
		"--data-dir", unsafePath, "--workspace-root", directory,
	}
	code := run(append(args, productBundleArgs(t)...), strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if listenerCalled || strings.Contains(stdout.String(), "READY") {
		t.Fatalf("listenerCalled=%v stdout=%q", listenerCalled, stdout.String())
	}
	if strings.Contains(stderr.String(), unsafePath) {
		t.Fatalf("stderr leaked data directory: %q", stderr.String())
	}
}

func TestRunSanitizesListenerSetupFailure(t *testing.T) {
	directory := t.TempDir()
	tokenPath := preparedServiceToken(t, directory)
	previous := listenTransport
	listenTransport = func(string) (net.Listener, error) {
		return nil, errors.New(`listen C:\secret\endpoint.sock with token 0123456789abcdef failed`)
	}
	defer func() { listenTransport = previous }()

	var stdout, stderr bytes.Buffer
	args := []string{
		"--endpoint", "test-endpoint", "--token-file", tokenPath,
		"--data-dir", filepath.Join(directory, "data"), "--workspace-root", directory,
	}
	code := run(append(args, productBundleArgs(t)...), strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run code = %d, want 1", code)
	}
	if stdout.Len() != 0 || stderr.String() != "local transport unavailable\n" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunSanitizesServeFailureAfterReady(t *testing.T) {
	directory := t.TempDir()
	tokenPath := preparedServiceToken(t, directory)
	previous := listenTransport
	listenTransport = func(string) (net.Listener, error) {
		return failingListener{err: errors.New(`accept C:\secret\endpoint.sock with token 0123456789abcdef failed`)}, nil
	}
	defer func() { listenTransport = previous }()

	var stdout, stderr bytes.Buffer
	args := []string{
		"--endpoint", "test-endpoint", "--token-file", tokenPath,
		"--data-dir", filepath.Join(directory, "data"), "--workspace-root", directory,
	}
	code := run(append(args, productBundleArgs(t)...), strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run code = %d, want 1", code)
	}
	if stdout.String() != "READY test-endpoint\n" || stderr.String() != "service transport failed\n" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunSanitizesPrepareTokenFailure(t *testing.T) {
	previous := prepareTokenFileForRun
	prepareTokenFileForRun = func(string) error {
		return errors.New(`prepare C:\secret\token-file with token 0123456789abcdef and ENV_SECRET failed`)
	}
	defer func() { prepareTokenFileForRun = previous }()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--prepare-token-file", filepath.Join(t.TempDir(), "token")}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || stderr.String() != "authentication token file preparation failed\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunSanitizesConsumeTokenFailure(t *testing.T) {
	previous := consumeTokenFileForRun
	consumeTokenFileForRun = func(string) (string, error) {
		return "", errors.New(`consume C:\secret\token-file with token 0123456789abcdef and ENV_SECRET failed`)
	}
	defer func() { consumeTokenFileForRun = previous }()

	var stdout, stderr bytes.Buffer
	args := []string{
		"--endpoint", "test-endpoint", "--token-file", `C:\secret\token-file`, "--data-dir", filepath.Join(t.TempDir(), "data"),
		"--workspace-root", t.TempDir(),
	}
	code := run(append(args, productBundleArgs(t)...), strings.NewReader(""), &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || stderr.String() != "authentication token unavailable\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func preparedServiceToken(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "service-token")
	if err := os.WriteFile(path, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareTokenFileForTest(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func productBundleArgs(t *testing.T) []string {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "bundles")
	args := make([]string, 0, 6)
	for _, item := range []struct {
		flag string
		leaf string
	}{
		{"--cmake-bundle-root", "cmake"},
		{"--coverage-bundle-root", "coverage"},
		{"--testgen-bundle-root", "testgen"},
	} {
		root := filepath.Join(parent, item.leaf)
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		args = append(args, item.flag, root)
	}
	return args
}

type failingListener struct{ err error }

func (l failingListener) Accept() (net.Conn, error) { return nil, l.err }
func (failingListener) Close() error                { return nil }
func (failingListener) Addr() net.Addr              { return failingAddr("test") }

type failingAddr string

func (a failingAddr) Network() string { return string(a) }
func (a failingAddr) String() string  { return string(a) }
