package testgenanalysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type nativeSeedSpec struct {
	framework, fileName, standard, signature, headerName, driverName string
}

func nativeSeedSpecs() []nativeSeedSpec {
	return []nativeSeedSpec{
		{"cpputest", "classifier.cpp", "-std=c++17", "int classify(int value) noexcept", "classifier.hpp", "classifier_test.cpp"},
		{"unity", "classifier.c", "-std=c11", "int classify(int value)", "classifier.h", "classifier_test.c"},
	}
}

func nativeSeedRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok || !filepath.IsAbs(file) {
		t.Fatal("native seed repository location unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	if _, err := os.Stat(filepath.Join(root, "apps", "test-service", "go.mod")); err != nil {
		t.Fatal("native seed repository root invalid")
	}
	return root
}

func nativeSeedBytes(t *testing.T, spec nativeSeedSpec) []byte {
	t.Helper()
	fixture := filepath.Join(nativeSeedRoot(t), "tools", "service-probe", "fixtures", "test-generation", spec.framework)
	source, err := os.ReadFile(filepath.Join(fixture, "src", spec.fileName))
	if err != nil {
		t.Fatal("native seed source unavailable")
	}
	text := strings.ReplaceAll(string(source), "\r\n", "\n")
	start := strings.Index(text, spec.signature)
	expectedBody := spec.signature + " {\n  if (value < 0) return -1;\n  if (value == 0) return 0;\n  return 1;\n}\n"
	if start < 0 || text[start:] != expectedBody {
		t.Fatal("native seed function contract changed")
	}
	header, err := os.ReadFile(filepath.Join(fixture, "include", spec.headerName))
	if err != nil || !strings.Contains(string(header), spec.signature+";") {
		t.Fatal("native seed public header contract changed")
	}
	driver, err := os.ReadFile(filepath.Join(fixture, "tests", spec.driverName))
	if err != nil || !strings.Contains(string(driver), "#include \""+spec.headerName+"\"") {
		t.Fatal("native seed test entry no longer uses its public header")
	}
	return source
}

func nativeSeedAnalysis(t *testing.T, spec nativeSeedSpec, source []byte) (Analyzer, AnalysisRequest, *fixtureRunner) {
	t.Helper()
	analyzer, request, runner, _ := fixtureAnalysis(t)
	request.SourceRelative = spec.fileName
	request.Arguments = []string{spec.standard}
	sum := sha256.Sum256(source)
	request.SourceDigest = hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(request.WorkspaceRoot, request.SourceRelative), source, 0600); err != nil {
		t.Fatal("native seed temporary source unavailable")
	}
	runner.err = errors.New("test stops immediately after analyzer preflight")
	return analyzer, request, runner
}

func TestNativeGenerationSeedMatchesFixedAnalyzer(t *testing.T) {
	for _, spec := range nativeSeedSpecs() {
		t.Run(spec.framework, func(t *testing.T) {
			source := nativeSeedBytes(t, spec)
			analyzer, request, runner := nativeSeedAnalysis(t, spec, source)
			_, err := analyzer.Analyze(context.Background(), request)
			if runner.calls != 1 {
				t.Fatalf("native seed blocked before runner: calls=%d; want 1", runner.calls)
			}
			if err == nil {
				t.Fatal("preflight observer must not fabricate successful analysis")
			}
			if hasSourceDirective(source) {
				t.Fatal("native seed contains a forbidden source directive")
			}
			if !strings.HasPrefix(string(source), spec.signature+" {") {
				t.Fatal("native seed has an unexpected prefix")
			}
			if runner.spec.Executable != analyzer.bundle.ClangPath() ||
				runner.spec.Dir != request.WorkspaceRoot || len(runner.spec.Env) != 0 ||
				runner.spec.Timeout != request.Timeout || runner.spec.MaxOutput != maxASTBytes {
				t.Fatal("native seed observer received an unexpected process boundary")
			}
		})
	}
}

func TestNativeGenerationSeedDirectiveReintroductionRejected(t *testing.T) {
	for _, spec := range nativeSeedSpecs() {
		source := nativeSeedBytes(t, spec)
		markers := []struct{ name, prefix string }{
			{"hash", "#include \"" + spec.headerName + "\"\n"},
			{"digraph", "%:include \"" + spec.headerName + "\"\n"},
			{"trigraph", "??=include \"" + spec.headerName + "\"\n"},
		}
		for _, marker := range markers {
			t.Run(spec.framework+"/"+marker.name, func(t *testing.T) {
				input := append([]byte(marker.prefix), source...)
				analyzer, request, runner := nativeSeedAnalysis(t, spec, input)
				_, err := analyzer.Analyze(context.Background(), request)
				if err == nil || runner.calls != 0 {
					t.Fatal("reinjected source directive reached the runner")
				}
				if !hasSourceDirective(input) {
					t.Fatal("directive reintroduction test input is invalid")
				}
			})
		}
	}
}
