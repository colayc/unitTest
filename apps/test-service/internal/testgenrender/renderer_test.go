package testgenrender

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	assert "unit-test-ide.local/test-service/internal/testgenassert"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

const idA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const idB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const idC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func request(lang Language) RenderRequest {
	inputs := []solver.Input{{Name: "x", Value: solver.Value{Kind: analysis.TypeInteger, Integer: "2"}}}
	p := analysis.Program{Version: analysis.IRVersion, Digest: idA, Functions: []analysis.Function{{SymbolID: idB, Name: "choose", ReturnType: analysis.Type{Kind: analysis.TypeInteger, BitWidth: 32, Signed: true}, Parameters: []analysis.Parameter{{Name: "x", Type: analysis.Type{Kind: analysis.TypeInteger, BitWidth: 32, Signed: true}}}, Excerpt: analysis.SourceExcerpt{Digest: idA}, OracleProofs: []analysis.OracleProof{{Kind: string(assert.EvidenceReturnContract), CandidateID: idC, InputDigest: assert.InputDigest(inputs), TargetDigest: idB, SourceDigest: idA, ExpectedDigest: assert.ValueDigest(solver.Value{Kind: analysis.TypeInteger, Integer: "7"}), Rule: string(assert.RuleEqual)}}, Decision: analysis.Decision{Kind: analysis.DecisionSupported, Reason: analysis.ReasonNone}}}}
	ext := "cpp"
	if lang == LanguageC {
		ext = "c"
	}
	framework := "CppUTest"
	if lang == LanguageC {
		framework = "unity"
	}
	return RenderRequest{Program: p, SymbolID: idB, Language: lang, HeaderPath: "include/choose.h", Target: TargetMetadata{TestTarget: "unit_tests", ProductionTarget: "core", FrameworkTarget: framework, CMakePath: "tests/CMakeLists.txt", TestPath: "tests/generated/choose_test." + ext, ExistingCMake: "add_executable(unit_tests existing.cpp)\ntarget_link_libraries(unit_tests PRIVATE core " + framework + ")\n"}, Cases: []Case{{Vector: solver.InputVector{ID: idC, Inputs: inputs}, Observation: assert.Observation{SymbolID: idB, CandidateID: idC, Evidence: []assert.Evidence{{Kind: assert.EvidenceReturnContract, Target: assert.TargetReturn, TargetDigest: idB, SourceDigest: idA, Expected: solver.Value{Kind: analysis.TypeInteger, Integer: "7"}, Rule: assert.RuleEqual, Stability: assert.StabilityDeterministic}}}}}}
}

func TestGoldenCppUTestAndUnity(t *testing.T) {
	for _, tc := range []struct {
		lang   Language
		source string
	}{{LanguageCPP, "cpputest.cpp"}, {LanguageC, "unity.c"}} {
		t.Run(string(tc.lang), func(t *testing.T) {
			r := request(tc.lang)
			got, err := Render(r)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Files) != 2 || got.Files[0].Path != r.Target.TestPath || got.Files[1].Path != r.Target.CMakePath {
				t.Fatalf("unexpected edits: %+v", got.Files)
			}
			want, err := os.ReadFile("testdata/golden/" + tc.source)
			if err != nil {
				t.Fatal(err)
			}
			if string(got.Files[0].Content) != string(want) {
				t.Errorf("source mismatch\ngot:\n%s\nwant:\n%s", got.Files[0].Content, want)
			}
			cmakeFile := "CMakeLists.txt"
			if tc.lang == LanguageC {
				cmakeFile = "CMakeLists-unity.txt"
			}
			cmake, err := os.ReadFile("testdata/golden/" + cmakeFile)
			if err != nil {
				t.Fatal(err)
			}
			if string(got.Files[1].Content) != string(cmake) {
				t.Errorf("CMake mismatch\ngot:\n%s\nwant:\n%s", got.Files[1].Content, cmake)
			}
			for _, name := range []string{"CppUMock", "CMock", "Mock", "Stub"} {
				if strings.Contains(string(got.Files[0].Content), name) {
					t.Fatalf("forbidden dependency %s", name)
				}
			}
			again, err := Render(r)
			if err != nil || string(again.Files[0].Content) != string(got.Files[0].Content) || again.Diff != got.Diff {
				t.Fatal("nondeterministic output")
			}
		})
	}
}

func TestRenderRejectsDuplicateIDsAndUnsafePaths(t *testing.T) {
	r := request(LanguageC)
	r.Cases = append(r.Cases, r.Cases[0])
	if _, err := Render(r); err == nil {
		t.Fatal("duplicate case accepted")
	}
	for _, bad := range []string{"C:/secret.c", "../secret.c", "tests/../../secret.c", "tests/../generated.c", "tests\\bad.c"} {
		r = request(LanguageC)
		r.Target.TestPath = bad
		if _, err := Render(r); err == nil {
			t.Errorf("unsafe path accepted: %s", bad)
		}
	}
	r = request(LanguageC)
	r.Target.TestPath = "src/production.c"
	if _, err := Render(r); err == nil {
		t.Fatal("production edit accepted")
	}
}

func TestRenderCMakeStructuralIdempotenceAndLinkage(t *testing.T) {
	r := request(LanguageC)
	got, err := Render(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Target.ExistingCMake = string(got.Files[1].Content)
	twice, err := Render(r)
	if err != nil || string(twice.Files[1].Content) != string(got.Files[1].Content) {
		t.Fatalf("not idempotent: %v", err)
	}
	r = request(LanguageC)
	r.Target.ExistingCMake = "add_executable(unit_tests existing.cpp)\n"
	if _, err := Render(r); err == nil {
		t.Fatal("missing production linkage accepted")
	}
	r = request(LanguageC)
	r.Target.ExistingCMake = "add_executable(other existing.cpp)\ntarget_link_libraries(other PRIVATE core unity)\n"
	if _, err := Render(r); err == nil {
		t.Fatal("missing test target accepted")
	}
}

func TestCMakeSourcesResolveRelativeToContainingList(t *testing.T) {
	for _, lang := range []Language{LanguageC, LanguageCPP} {
		r := request(lang)
		got, err := Render(r)
		if err != nil {
			t.Fatal(err)
		}
		cmake := string(got.Files[1].Content)
		ext := ".cpp"
		if lang == LanguageC {
			ext = ".c"
		}
		if !strings.Contains(cmake, "\"generated/choose_test"+ext+"\"") || strings.Contains(cmake, "\"tests/generated/choose_test"+ext+"\"") {
			t.Fatalf("CMake source path resolves incorrectly: %s", cmake)
		}
	}
	r := request(LanguageC)
	r.Target.CMakePath = "tests/nested/CMakeLists.txt"
	if _, err := Render(r); err == nil {
		t.Fatal("source outside CMake directory accepted")
	}
}

func TestRenderTypeSafeLiteralAndTolerance(t *testing.T) {
	r := request(LanguageC)
	r.Program.Functions[0].ReturnType = analysis.Type{Kind: analysis.TypeFloating, BitWidth: 64}
	r.Cases[0].Observation.Evidence[0].Expected = solver.Value{Kind: analysis.TypeFloating, Float: "0.125"}
	r.Cases[0].Observation.Evidence[0].Rule = assert.RuleNear
	r.Cases[0].Observation.Evidence[0].Tolerance = "0.001"
	r.Program.Functions[0].OracleProofs = []analysis.OracleProof{{Kind: string(assert.EvidenceReturnContract), CandidateID: idC, InputDigest: assert.InputDigest(r.Cases[0].Vector.Inputs), TargetDigest: idB, SourceDigest: idA, ExpectedDigest: assert.ValueDigest(r.Cases[0].Observation.Evidence[0].Expected), Rule: string(assert.RuleNear), Tolerance: "0.001"}}
	got, err := Render(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got.Files[0].Content), "TEST_ASSERT_DOUBLE_WITHIN(0.001, 0.125, actual)") {
		t.Fatalf("float comparison wrong: %s", got.Files[0].Content)
	}
	r = request(LanguageCPP)
	r.Cases[0].Vector.Inputs[0].Value.Integer = "not-int"
	if _, err := Render(r); err == nil {
		t.Fatal("invalid integer literal accepted")
	}
}

func TestRenderDefersCharacterizationWithoutTrustedRepeatReceipt(t *testing.T) {
	r := request(LanguageC)
	r.Cases[0].Observation.Evidence[0].Kind = assert.EvidenceObservedOutput
	r.Cases[0].Observation.Evidence[0].StabilityDigest = idA
	r.Cases[0].Observation.Evidence[0].RepeatCount = 2
	if _, err := Render(r); err == nil {
		t.Fatal("forged repeat metadata accepted")
	}
}

func TestRenderRejectsFloatAggregateWithoutLeafTolerance(t *testing.T) {
	for _, lang := range []Language{LanguageC, LanguageCPP} {
		for _, typ := range []analysis.Type{
			{Kind: analysis.TypeArray, Bound: 1, Element: &analysis.Type{Kind: analysis.TypeFloating, BitWidth: 64}},
			{Kind: analysis.TypeRecord, Proven: true, Spelling: "Result", Fields: []analysis.Field{{Name: "value", Type: analysis.Type{Kind: analysis.TypeFloating, BitWidth: 64}}}},
		} {
			r := request(lang)
			r.Program.Functions[0].ReturnType = typ
			value := solver.Value{Kind: typ.Kind}
			if typ.Kind == analysis.TypeArray {
				value.Elements = []solver.Value{{Kind: analysis.TypeFloating, Float: "0.5"}}
			} else {
				value.Fields = []solver.FieldValue{{Name: "value", Value: solver.Value{Kind: analysis.TypeFloating, Float: "0.5"}}}
			}
			r.Cases[0].Observation.Evidence[0].Expected = value
			r.Program.Functions[0].OracleProofs[0].ExpectedDigest = assert.ValueDigest(value)
			if _, err := Render(r); err == nil {
				t.Fatalf("float aggregate accepted for %s/%s", lang, typ.Kind)
			}
		}
	}
}

func TestCStringLiteralTerminatesControlEscape(t *testing.T) {
	if got := cStringLiteral("\x01a"); got != "\"\\001a\"" {
		t.Fatalf("C escape changed byte meaning: %q", got)
	}
	for _, tc := range []struct {
		ext       string
		compilers []string
	}{{".c", []string{"gcc", "clang"}}, {".cpp", []string{"g++", "clang++"}}} {
		compiler := ""
		for _, name := range tc.compilers {
			if found, err := exec.LookPath(name); err == nil {
				compiler = found
				break
			}
		}
		if compiler == "" {
			t.Logf("compiler unavailable for %s; literal representation checked", tc.ext)
			continue
		}
		dir := t.TempDir()
		source := filepath.Join(dir, "literal"+tc.ext)
		binary := filepath.Join(dir, "literal")
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		body := fmt.Sprintf("int main(void) { const unsigned char *s = (const unsigned char *)%s; return s[0] == 1 && s[1] == 'a' && s[2] == 0 ? 0 : 1; }\n", cStringLiteral("\x01a"))
		if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(compiler, source, "-o", binary).CombinedOutput(); err != nil {
			t.Fatalf("%s compile: %v\n%s", tc.ext, err, output)
		}
		if output, err := exec.Command(binary).CombinedOutput(); err != nil {
			t.Fatalf("%s byte mismatch: %v\n%s", tc.ext, err, output)
		}
	}
}
