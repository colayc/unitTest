package testgenanalysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/probe"
)

type fixtureBundle struct {
	path, resource, manifest string
	verified                 *int
}

func (b fixtureBundle) ClangPath() string      { return b.path }
func (b fixtureBundle) ResourceDir() string    { return b.resource }
func (b fixtureBundle) ManifestSHA256() string { return b.manifest }
func (b fixtureBundle) Verify() error          { *b.verified++; return nil }

type fixtureRunner struct {
	calls  int
	spec   probe.Spec
	result probe.Result
	err    error
}

func (r *fixtureRunner) Run(_ context.Context, s probe.Spec) (probe.Result, error) {
	r.calls++
	r.spec = s
	return r.result, r.err
}
func fixtureAnalysis(t *testing.T) (Analyzer, AnalysisRequest, *fixtureRunner, *int) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "scalar.c")
	content := []byte("int choose(int x){return x>0 ? 1 : 0;}\n")
	if err := os.WriteFile(source, content, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	verified := 0
	runner := &fixtureRunner{result: probe.Result{ExitCode: 0, Stdout: []byte(scalarAST)}}
	return Analyzer{bundle: fixtureBundle{path: filepath.Join(root, "fixed", "clang"), resource: filepath.Join(root, "fixed", "lib", "clang", "22"), manifest: strings.Repeat("b", 64), verified: &verified}, runner: runner}, AnalysisRequest{WorkspaceRoot: root, SourceRelative: "scalar.c", SourceDigest: hex.EncodeToString(sum[:]), CompileSnapshotDigest: strings.Repeat("c", 64), Arguments: []string{"-std=c11"}, Timeout: 5 * time.Second}, runner, &verified
}

func TestAnalyzeUsesOnlyVerifiedFixedCompilerWithCleanEnvironment(t *testing.T) {
	a, request, runner, verified := fixtureAnalysis(t)
	program, err := a.Analyze(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if *verified != 2 || runner.calls != 1 {
		t.Fatalf("bundle verifications=%d, launches=%d", *verified, runner.calls)
	}
	want := []string{"-Xclang", "-ast-dump=json", "-fsyntax-only", "-nostdinc", "-nostdinc++", "-isystem", filepath.Join(a.bundle.ResourceDir(), "include"), "-std=c11", "-resource-dir", a.bundle.ResourceDir(), filepath.Join(request.WorkspaceRoot, request.SourceRelative)}
	if !reflect.DeepEqual(runner.spec.Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.spec.Args, want)
	}
	if runner.spec.Executable != a.bundle.ClangPath() || runner.spec.Dir != request.WorkspaceRoot || len(runner.spec.Env) != 0 || runner.spec.MaxOutput <= 0 || runner.spec.Timeout != request.Timeout {
		t.Fatalf("unsafe process spec %#v", runner.spec)
	}
	if len(program.Functions) != 1 || program.Functions[0].Decision.Kind != DecisionSupported {
		t.Fatalf("unexpected program %#v", program)
	}
	wantExcerpt := sha256.Sum256([]byte("int choose(int x){return x>0 ? 1 : 0;}"))
	if program.Functions[0].Effect != EffectLocalMemory || program.Functions[0].Excerpt.Digest != hex.EncodeToString(wantExcerpt[:]) || program.Functions[0].Excerpt.LocationDigest != program.Functions[0].LocationDigest || program.Functions[0].Excerpt.StartByte != 0 || program.Functions[0].Excerpt.EndByte != 38 {
		t.Fatalf("missing closed effect/excerpt evidence: %#v", program.Functions[0])
	}
}

func TestAnalyzeSuppressesImplicitHostHeaders(t *testing.T) {
	a, request, runner, _ := fixtureAnalysis(t)
	if _, err := a.Analyze(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.spec.Args, " ")
	if !strings.Contains(joined, "-nostdinc") || !strings.Contains(joined, "-nostdinc++") {
		t.Fatalf("host include roots remain enabled: %#v", runner.spec.Args)
	}
}

func TestAnalyzeRejectsInjectedArgumentsBeforeLaunch(t *testing.T) {
	bad := [][]string{{"@evil.rsp"}, {"-Xclang", "-load"}, {"-fplugin=evil"}, {"-o", "out.exe"}, {"-include", "evil.h"}, {"-I../escape"}, {"-DSECRET=$(echo x)"}, {"-std=c11", "-std=c++20"}, {"-target", "evil"}, {"-resource-dir=/tmp/evil"}, {"-Wl,--version"}}
	for _, args := range bad {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			a, req, runner, _ := fixtureAnalysis(t)
			req.Arguments = args
			if _, err := a.Analyze(context.Background(), req); err == nil {
				t.Fatalf("accepted %#v", args)
			}
			if runner.calls != 0 {
				t.Fatal("launched hostile arguments")
			}
		})
	}
}

func TestAnalyzeRejectsStaleSourceAndCancellation(t *testing.T) {
	a, req, runner, _ := fixtureAnalysis(t)
	if err := os.WriteFile(filepath.Join(req.WorkspaceRoot, req.SourceRelative), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Analyze(context.Background(), req); err == nil || runner.calls != 0 {
		t.Fatalf("stale source launched: %v", err)
	}
	a, req, runner, _ = fixtureAnalysis(t)
	runner.err = context.Canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Analyze(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
}

func TestAnalyzeRedactsCompilerFailure(t *testing.T) {
	a, req, runner, _ := fixtureAnalysis(t)
	runner.result = probe.Result{ExitCode: 1, Stderr: []byte("secret C:\\private\\source.c")}
	_, err := a.Analyze(context.Background(), req)
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private") {
		t.Fatalf("leaked compiler output: %v", err)
	}
}
