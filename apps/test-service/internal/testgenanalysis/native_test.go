package testgenanalysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/probe"
)

func TestAnalyzeCancelsLiveProcessTree(t *testing.T) {
	root := t.TempDir()
	name := "hanger"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	executable := filepath.Join(t.TempDir(), name)
	build := exec.Command("go", "build", "-o", executable, "./testdata/analyzer-hanger")
	build.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build local cancellation fixture: %v: %s", err, output)
	}
	source := []byte("int f(void){return 1;}\n")
	if err := os.WriteFile(filepath.Join(root, "sample.c"), source, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(source)
	verified := 0
	a := Analyzer{bundle: fixtureBundle{path: executable, resource: filepath.Join(root, "unused-resource"), manifest: strings.Repeat("b", 64), verified: &verified}, runner: probe.NewRunner()}
	request := AnalysisRequest{WorkspaceRoot: root, SourceRelative: "sample.c", SourceDigest: hex.EncodeToString(sum[:]), CompileSnapshotDigest: strings.Repeat("c", 64), Arguments: []string{"-std=c11"}, Timeout: 10 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := a.Analyze(ctx, request); done <- err }()
	heartbeat := filepath.Join(root, "heartbeat")
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(heartbeat); err == nil {
			break
		}
		if time.Now().After(deadline) {
			if detail, readErr := os.ReadFile(filepath.Join(root, "start-error")); readErr == nil {
				t.Fatalf("fixture child never started: %s", detail)
			}
			t.Fatal("fixture child never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("analyzer did not reap child tree")
	}
	before, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	after, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("descendant continued writing after cancellation")
	}
}

func TestNativeAnalyzeDigestIgnoresRootLocaleAndTimestamp(t *testing.T) {
	clang := os.Getenv("UTIDE_TESTGEN_TEST_CLANG")
	if clang == "" {
		t.Skip("requires local Clang 22")
	}
	resource := filepath.Join(filepath.Dir(filepath.Dir(clang)), "lib", "clang", "22")
	source := []byte("int choose(int x) { if (x > 0) return 1; return 0; }\n")
	sum := sha256.Sum256(source)
	var digests []string
	for index := 0; index < 2; index++ {
		root := t.TempDir()
		path := filepath.Join(root, "sample.c")
		if err := os.WriteFile(path, source, 0600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(int64(1000000000+index*100000), 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			t.Setenv("LANG", "C")
		} else {
			t.Setenv("LANG", "fr_FR.UTF-8")
		}
		verified := 0
		a := Analyzer{bundle: fixtureBundle{path: clang, resource: resource, manifest: strings.Repeat("b", 64), verified: &verified}, runner: probe.NewRunner()}
		p, err := a.Analyze(context.Background(), AnalysisRequest{WorkspaceRoot: root, SourceRelative: "sample.c", SourceDigest: hex.EncodeToString(sum[:]), CompileSnapshotDigest: strings.Repeat("c", 64), Arguments: []string{"-std=c11"}, Timeout: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, p.Digest)
	}
	if digests[0] != digests[1] {
		t.Fatalf("path/locale/time changed digest %q != %q", digests[0], digests[1])
	}
}

func TestNativeClangSearchPathIsPinned(t *testing.T) {
	clang := os.Getenv("UTIDE_TESTGEN_TEST_CLANG")
	if clang == "" {
		t.Skip("requires local Clang 22")
	}
	resource := filepath.Join(filepath.Dir(filepath.Dir(clang)), "lib", "clang", "22")
	path := filepath.Join("testdata", "safe", "scalar-branches.c")
	r, err := probe.NewRunner().Run(context.Background(), probe.Spec{Executable: clang, Args: []string{"-E", "-v", "-nostdinc", "-nostdinc++", "-isystem", filepath.Join(resource, "include"), "-resource-dir", resource, "-std=c11", path}, Env: []string{}, Timeout: 10 * time.Second, MaxOutput: 1 << 20})
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("include search probe: %v/%d", err, r.ExitCode)
	}
	start := strings.Index(string(r.Stderr), "#include <...> search starts here:")
	end := strings.Index(string(r.Stderr), "End of search list.")
	if start < 0 || end <= start {
		t.Fatalf("no effective search list: %q", r.Stderr)
	}
	list := strings.TrimSpace(string(r.Stderr[start+len("#include <...> search starts here:") : end]))
	if filepath.Clean(list) != filepath.Clean(filepath.Join(resource, "include")) {
		t.Fatalf("unapproved include search paths: %q", list)
	}
}
