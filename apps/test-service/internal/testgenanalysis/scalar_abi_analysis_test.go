package testgenanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/probe"
)

// Recording only the external process boundary keeps Analyzer coordination real.
type integerABIRecordingRunner struct {
	mu        sync.Mutex
	specs     []probe.Spec
	deadlines []time.Time
	hook      func(context.Context, probe.Spec, int) (probe.Result, error)
}

func (r *integerABIRecordingRunner) Run(ctx context.Context, spec probe.Spec) (probe.Result, error) {
	deadline, _ := ctx.Deadline()
	r.mu.Lock()
	index := len(r.specs)
	r.specs = append(r.specs, spec)
	r.deadlines = append(r.deadlines, deadline)
	r.mu.Unlock()
	if r.hook != nil {
		return r.hook(ctx, spec, index)
	}
	return integerABIResultForSpec(spec), nil
}
func integerABIResultForSpec(spec probe.Spec) probe.Result {
	for _, arg := range spec.Args {
		if arg == "-dM" {
			return probe.Result{ExitCode: 0, Stdout: integerABITestMacros(runtime.GOOS+"-x64", false)}
		}
	}
	return probe.Result{ExitCode: 0, Stdout: []byte(scalarAST)}
}

type integerABIControlledBundle struct {
	fixtureBundle
	check func() error
}

func (b *integerABIControlledBundle) Verify() error { return b.check() }

// A single AST launch cannot prove ABI facts from the pinned compiler.
func TestAnalyzeIntegerABIProcessBoundary(t *testing.T) {
	analyzer, request, _, _ := fixtureAnalysis(t)
	runner := &integerABIRecordingRunner{}
	analyzer.runner = runner
	program, err := analyzer.Analyze(context.Background(), request)
	specs := runner.specs
	if err != nil || len(specs) != 2 {
		t.Fatal("ABI and AST must share one analysis")
	}
	if specs[0].MaxOutput != maxIntegerABIBytes || specs[1].MaxOutput != maxASTBytes {
		t.Fatal("incorrect closed process bounds")
	}
	if program.Functions[0].ReturnType.BitWidth == 0 {
		t.Fatal("source-derived integer proof absent")
	}
	for _, mode := range []struct{ standard, language string }{{"-std=c11", "c"}, {"-std=c17", "c"}, {"-std=c++17", "c++"}, {"-std=c++20", "c++"}} {
		t.Run(mode.standard, func(t *testing.T) {
			a, req, _, _ := fixtureAnalysis(t)
			if os.Mkdir(filepath.Join(req.WorkspaceRoot, "include"), 0700) != nil {
				t.Fatal("test include root unavailable")
			}
			req.Arguments = []string{mode.standard, "-DPROJECT_FEATURE=1", "-Iinclude"}
			r := &integerABIRecordingRunner{}
			a.runner = r
			p, err := a.Analyze(context.Background(), req)
			if err != nil || len(r.specs) != 2 || len(p.Functions) != 1 {
				t.Fatal("standard analysis failed")
			}
			wantABI := []string{"--no-default-config", "-dM", "-E", "-x", mode.language, mode.standard, "-nostdinc", "-nostdinc++", "-resource-dir", a.bundle.ResourceDir(), "-"}
			wantAST := []string{"--no-default-config", "-Xclang", "-ast-dump=json", "-fsyntax-only", "-nostdinc", "-nostdinc++", "-isystem", filepath.Join(a.bundle.ResourceDir(), "include"), mode.standard, "-DPROJECT_FEATURE=1", "-I" + filepath.Join(req.WorkspaceRoot, "include"), "-resource-dir", a.bundle.ResourceDir(), filepath.Join(req.WorkspaceRoot, req.SourceRelative)}
			if !reflect.DeepEqual(r.specs[0].Args, wantABI) || !reflect.DeepEqual(r.specs[1].Args, wantAST) {
				t.Fatal("untrusted process arguments")
			}
			for _, spec := range r.specs {
				if spec.Executable != a.bundle.ClangPath() || spec.Dir != req.WorkspaceRoot || len(spec.Env) != 0 || spec.Timeout <= 0 || spec.Timeout > req.Timeout {
					t.Fatal("untrusted process boundary")
				}
			}
			f := p.Functions[0]
			if f.ReturnType.BitWidth != 32 || !f.ReturnType.Signed || !f.ReturnType.Proven || f.Parameters[0].Type.BitWidth != 32 || !f.Parameters[0].Type.Proven {
				t.Fatal("complete integer proof absent")
			}
		})
	}
}

// Accepting any consumed macro lets compile arguments impersonate compiler facts.
func TestAnalyzeIntegerABISpoofingBeforeLaunch(t *testing.T) {
	for _, name := range strings.Fields(integerABIMacroNames) {
		t.Run(name, func(t *testing.T) {
			analyzer, request, _, _ := fixtureAnalysis(t)
			runner := &integerABIRecordingRunner{}
			analyzer.runner = runner
			request.Arguments = []string{"-std=c11", "-D" + name + "=1"}
			_, err := analyzer.Analyze(context.Background(), request)
			if err == nil || len(runner.specs) != 0 {
				t.Fatal("compiler ABI spoof reached process boundary")
			}
		})
	}
	for index, args := range [][]string{{"@rsp"}, {"-target", "x86_64"}, {"-m32"}, {"-funsigned-char"}, {"-include", "header.h"}, {"-resource-dir", "resource"}} {
		t.Run([]string{"response", "target", "m32", "unsigned_char", "include", "resource"}[index], func(t *testing.T) {
			a, req, _, _ := fixtureAnalysis(t)
			r := &integerABIRecordingRunner{}
			a.runner = r
			req.Arguments = append([]string{"-std=c11"}, args...)
			if _, err := a.Analyze(context.Background(), req); err == nil || len(r.specs) != 0 {
				t.Fatal("untrusted option launched")
			}
		})
	}
	for _, define := range []string{"-DPROJECT_FEATURE=1", "-D_DEBUG"} {
		t.Run(define, func(t *testing.T) {
			a, req, _, _ := fixtureAnalysis(t)
			r := &integerABIRecordingRunner{}
			a.runner = r
			req.Arguments = []string{"-std=c11", define}
			if _, err := a.Analyze(context.Background(), req); err != nil || len(r.specs) != 2 {
				t.Fatal("ordinary define rejected")
			}
		})
	}
}

// Independent per-process timeouts would renew this request's budget.
func TestAnalyzeIntegerABISharedDeadline(t *testing.T) {
	analyzer, request, _, _ := fixtureAnalysis(t)
	request.Timeout = 250 * time.Millisecond
	runner := &integerABIRecordingRunner{}
	runner.hook = func(ctx context.Context, spec probe.Spec, index int) (probe.Result, error) {
		if index == 0 {
			time.Sleep(25 * time.Millisecond)
		}
		return integerABIResultForSpec(spec), nil
	}
	analyzer.runner = runner
	if _, err := analyzer.Analyze(context.Background(), request); err != nil {
		t.Fatal("shared deadline request failed")
	}
	if len(runner.specs) != 2 || runner.deadlines[0].IsZero() || runner.deadlines[0] != runner.deadlines[1] || runner.specs[1].Timeout >= runner.specs[0].Timeout || runner.specs[1].Timeout > 225*time.Millisecond {
		t.Fatal("analysis budget renewed")
	}
	t.Run("canceled_after_abi", func(t *testing.T) {
		a, req, _, _ := fixtureAnalysis(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := &integerABIRecordingRunner{}
		r.hook = func(_ context.Context, spec probe.Spec, _ int) (probe.Result, error) {
			cancel()
			return integerABIResultForSpec(spec), nil
		}
		a.runner = r
		p, err := a.Analyze(ctx, req)
		integerABIAssertRejected(t, p, err)
		if !errors.Is(err, context.Canceled) || len(r.specs) != 1 {
			t.Fatal("cancellation crossed AST boundary")
		}
	})
	t.Run("earlier_parent_deadline", func(t *testing.T) {
		a, req, _, _ := fixtureAnalysis(t)
		req.Timeout = 30 * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		want, _ := ctx.Deadline()
		r := &integerABIRecordingRunner{}
		r.hook = func(ctx context.Context, _ probe.Spec, _ int) (probe.Result, error) {
			select {
			case <-ctx.Done():
				return probe.Result{}, ctx.Err()
			case <-time.After(25 * time.Millisecond):
				return probe.Result{}, errors.New("parent budget renewed")
			}
		}
		a.runner = r
		p, err := a.Analyze(ctx, req)
		integerABIAssertRejected(t, p, err)
		if !errors.Is(err, context.DeadlineExceeded) || len(r.specs) > 1 {
			t.Fatal("parent deadline ignored")
		}
		if len(r.specs) == 1 && (r.deadlines[0] != want || r.specs[0].Timeout > 10*time.Millisecond) {
			t.Fatal("parent deadline extended")
		}
	})
	t.Run("preflight_exhaustion", func(t *testing.T) {
		a, req, _, _ := fixtureAnalysis(t)
		req.Timeout = 5 * time.Millisecond
		a.bundle = &integerABIControlledBundle{fixtureBundle: a.bundle.(fixtureBundle), check: func() error { time.Sleep(10 * time.Millisecond); return nil }}
		r := &integerABIRecordingRunner{}
		a.runner = r
		p, err := a.Analyze(context.Background(), req)
		integerABIAssertRejected(t, p, err)
		if !errors.Is(err, context.DeadlineExceeded) || len(r.specs) != 0 {
			t.Fatal("expired preflight launched process")
		}
	})
}

func integerABIAssertRejected(t *testing.T, program Program, err error) {
	t.Helper()
	if err == nil || len(program.Functions) != 0 || program.Digest != "" {
		t.Fatal("unproven partial program escaped")
	}
	if strings.Contains(err.Error(), "secret-marker") {
		t.Fatal("tool output leaked")
	}
}

// Invalid ABI outputs must never reach AST, while AST failures remain closed.
func TestAnalyzeIntegerABIFailClosed(t *testing.T) {
	base := integerABITestMacros(runtime.GOOS+"-x64", false)
	cases := []struct {
		name   string
		result probe.Result
		err    error
	}{
		{"runner_error", probe.Result{Stderr: []byte("secret-marker")}, errors.New("secret-marker")},
		{"nonzero", probe.Result{ExitCode: 1, Stderr: []byte("secret-marker")}, nil},
		{"output_limit", probe.Result{Stderr: []byte("secret-marker")}, probe.ErrOutputLimit},
		{"wrong_macro", probe.Result{Stdout: bytes.Replace(base, []byte("__INT_WIDTH__ 32"), []byte("__INT_WIDTH__ (16+16)"), 1)}, nil},
		{"duplicate_macro", probe.Result{Stdout: append(append([]byte{}, base...), []byte("#define __INT_WIDTH__ 32\n")...)}, nil},
		{"missing_macro", probe.Result{Stdout: bytes.Replace(base, []byte("#define __INT_WIDTH__ 32\n"), nil, 1)}, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			a, req, _, _ := fixtureAnalysis(t)
			r := &integerABIRecordingRunner{}
			r.hook = func(context.Context, probe.Spec, int) (probe.Result, error) { return test.result, test.err }
			a.runner = r
			p, err := a.Analyze(context.Background(), req)
			integerABIAssertRejected(t, p, err)
			if len(r.specs) != 1 {
				t.Fatal("rejected ABI process count changed")
			}
			for _, spec := range r.specs {
				if slices.Contains(spec.Args, "-ast-dump=json") {
					t.Fatal("AST launched after rejected ABI")
				}
			}
		})
	}
	for _, kind := range []string{"nonzero", "runner_error", "foreign_source"} {
		t.Run("ast_"+kind, func(t *testing.T) {
			a, req, _, _ := fixtureAnalysis(t)
			r := &integerABIRecordingRunner{}
			r.hook = func(_ context.Context, spec probe.Spec, index int) (probe.Result, error) {
				if index == 0 {
					return integerABIResultForSpec(spec), nil
				}
				switch kind {
				case "nonzero":
					return probe.Result{ExitCode: 1, Stderr: []byte("secret-marker")}, nil
				case "runner_error":
					return probe.Result{Stderr: []byte("secret-marker")}, errors.New("secret-marker")
				default:
					return probe.Result{Stdout: []byte(strings.Replace(scalarAST, `"line":1,"col":1`, `"line":1,"col":1,"file":"header.h"`, 1))}, nil
				}
			}
			a.runner = r
			p, err := a.Analyze(context.Background(), req)
			integerABIAssertRejected(t, p, err)
			if len(r.specs) != 2 || !slices.Contains(r.specs[1].Args, "-ast-dump=json") {
				t.Fatal("AST rejection boundary missing")
			}
		})
	}
}

// Reverification must reject tool changes before AST and source changes before return.
func TestAnalyzeIntegerABIToolAndSourceChanges(t *testing.T) {
	t.Run("bundle_before_ast", func(t *testing.T) {
		a, req, _, _ := fixtureAnalysis(t)
		checks := 0
		a.bundle = &integerABIControlledBundle{fixtureBundle: a.bundle.(fixtureBundle), check: func() error {
			checks++
			if checks == 2 {
				return errors.New("changed")
			}
			return nil
		}}
		r := &integerABIRecordingRunner{}
		a.runner = r
		p, err := a.Analyze(context.Background(), req)
		integerABIAssertRejected(t, p, err)
		if checks != 2 || len(r.specs) != 1 {
			t.Fatal("changed bundle launched AST")
		}
	})
	for _, stage := range []struct {
		name  string
		index int
	}{{"after_abi", 0}, {"after_ast", 1}} {
		t.Run(stage.name, func(t *testing.T) {
			a, req, _, _ := fixtureAnalysis(t)
			r := &integerABIRecordingRunner{}
			r.hook = func(_ context.Context, spec probe.Spec, index int) (probe.Result, error) {
				if index == stage.index {
					if os.WriteFile(filepath.Join(req.WorkspaceRoot, req.SourceRelative), []byte("changed"), 0600) != nil {
						return probe.Result{}, errors.New("test source mutation failed")
					}
				}
				return integerABIResultForSpec(spec), nil
			}
			a.runner = r
			p, err := a.Analyze(context.Background(), req)
			integerABIAssertRejected(t, p, err)
			if len(r.specs) != 2 {
				t.Fatal("source change verification boundary changed")
			}
		})
	}
}

func TestAnalyzeIntegerABINoCrossRequestCache(t *testing.T) {
	a, req, _, _ := fixtureAnalysis(t)
	r := &integerABIRecordingRunner{}
	a.runner = r
	if p, err := a.Analyze(context.Background(), req); err != nil || len(p.Functions) != 1 || !p.Functions[0].Parameters[0].Type.Proven {
		t.Fatal("first request failed")
	}
	req.CompileSnapshotDigest = strings.Repeat("d", 64)
	if p, err := a.Analyze(context.Background(), req); err != nil || len(r.specs) != 4 || len(p.Functions) != 1 || !p.Functions[0].Parameters[0].Type.Proven {
		t.Fatal("cross-request cache or binding mix")
	}
}

func TestAnalyzeIntegerABIConcurrentIsolation(t *testing.T) {
	a, req, _, _ := fixtureAnalysis(t)
	r := &integerABIRecordingRunner{}
	a.runner = r
	var checks atomic.Int64
	a.bundle = &integerABIControlledBundle{fixtureBundle: a.bundle.(fixtureBundle), check: func() error { checks.Add(1); return nil }}
	type outcome struct {
		program Program
		err     error
	}
	results := make(chan outcome, 2)
	for _, digest := range []string{strings.Repeat("c", 64), strings.Repeat("d", 64)} {
		request := req
		request.CompileSnapshotDigest = digest
		go func(request AnalysisRequest) {
			p, err := a.Analyze(context.Background(), request)
			results <- outcome{p, err}
		}(request)
	}
	for range 2 {
		result := <-results
		if result.err != nil || len(result.program.Functions) != 1 || result.program.Functions[0].Parameters[0].Type.BitWidth != 32 || !result.program.Functions[0].Parameters[0].Type.Proven {
			t.Fatal("parallel request binding failed")
		}
	}
	if len(r.specs) != 4 || checks.Load() != 6 {
		t.Fatal("parallel proof/process isolation failed")
	}
}

func TestAnalyzeIntegerABIDigestContainsTypes(t *testing.T) {
	a, req, _, _ := fixtureAnalysis(t)
	r := &integerABIRecordingRunner{}
	a.runner = r
	p, err := a.Analyze(context.Background(), req)
	if err != nil || len(p.Functions) != 1 || len(p.Digest) != 64 {
		t.Fatal("final program missing")
	}
	digest := p.Digest
	if sealProgram(&p) != nil || p.Digest != digest {
		t.Fatal("final digest not stable")
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal("test IR copy failed")
	}
	var copy Program
	if json.Unmarshal(data, &copy) != nil {
		t.Fatal("test IR decode failed")
	}
	copy.Functions[0].Parameters[0].Type.BitWidth = 0
	if sealProgram(&copy) != nil || copy.Digest == digest {
		t.Fatal("integer facts absent from digest")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rejected, err := a.Analyze(ctx, req)
	integerABIAssertRejected(t, rejected, err)
	if !errors.Is(err, context.Canceled) || len(r.specs) != 2 {
		t.Fatal("canceled request returned successful program")
	}
}
