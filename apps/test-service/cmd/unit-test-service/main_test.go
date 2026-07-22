package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	code := run([]string{"--endpoint", "unused-endpoint", "--token-file", tokenPath, "--data-dir", unsafePath}, strings.NewReader(""), &stdout, &stderr)
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
	code := run([]string{"--endpoint", "test-endpoint", "--token-file", tokenPath, "--data-dir", filepath.Join(directory, "data")}, strings.NewReader(""), &stdout, &stderr)
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
	code := run([]string{"--endpoint", "test-endpoint", "--token-file", tokenPath, "--data-dir", filepath.Join(directory, "data")}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run code = %d, want 1", code)
	}
	if stdout.String() != "READY test-endpoint\n" || stderr.String() != "service transport failed\n" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
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

type failingListener struct{ err error }

func (l failingListener) Accept() (net.Conn, error) { return nil, l.err }
func (failingListener) Close() error                { return nil }
func (failingListener) Addr() net.Addr              { return failingAddr("test") }

type failingAddr string

func (a failingAddr) Network() string { return string(a) }
func (a failingAddr) String() string  { return string(a) }
