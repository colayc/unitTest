package unityrunner

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseSourcesFindsStandardUnityGrammar(t *testing.T) {
	manifest, err := ParseSources(".", []string{"testdata/basic.c"}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SetUp == nil || manifest.SetUp.Path != "testdata/basic.c" || manifest.SetUp.Line != 3 {
		t.Fatalf("SetUp = %#v", manifest.SetUp)
	}
	if manifest.TearDown == nil || manifest.TearDown.Path != "testdata/basic.c" || manifest.TearDown.Line != 7 {
		t.Fatalf("TearDown = %#v", manifest.TearDown)
	}
	want := []TestCase{
		{
			Name: "test_adds_numbers", Identity: "test_adds_numbers",
			Parameters: "void", Location: SourceLocation{Path: "testdata/basic.c", Line: 16},
		},
		{
			Name: "test_handles_zero", Identity: "test_handles_zero",
			Parameters: "void", Location: SourceLocation{Path: "testdata/basic.c", Line: 21},
		},
	}
	if !reflect.DeepEqual(manifest.Cases, want) {
		t.Fatalf("Cases = %#v, want %#v", manifest.Cases, want)
	}
}

func TestParseSourcesExpandsOfficialParameterForms(t *testing.T) {
	manifest, err := ParseSources(".", []string{"testdata/parameterized.c"}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var identities []string
	for _, testCase := range manifest.Cases {
		identities = append(identities, testCase.Identity)
		if testCase.Name == "test_named_case" && testCase.Parameters != "int value, const char * label" {
			t.Fatalf("named case Parameters = %q", testCase.Parameters)
		}
	}
	want := []string{
		`test_decimal_range(-0.5)`,
		`test_decimal_range(-1)`,
		`test_decimal_range(-1.5)`,
		`test_descending_range(1)`,
		`test_descending_range(2)`,
		`test_descending_range(3)`,
		`test_exclusive_range(0)`,
		`test_exclusive_range(1)`,
		`test_exclusive_range(2)`,
		`test_inclusive_range(1)`,
		`test_inclusive_range(2)`,
		`test_inclusive_range(3)`,
		`test_named_case(1, "one")`,
		`test_named_case(2, "two")`,
		`test_range_product(1, 10)`,
		`test_range_product(1, 20)`,
		`test_range_product(2, 10)`,
		`test_range_product(2, 20)`,
	}
	if !reflect.DeepEqual(identities, want) {
		t.Fatalf("identities = %#v, want %#v", identities, want)
	}
	for _, testCase := range manifest.Cases {
		if testCase.Location.Path != "testdata/parameterized.c" || testCase.Location.Line <= 0 {
			t.Fatalf("Location = %#v", testCase.Location)
		}
		if len(testCase.Arguments) == 0 {
			t.Fatalf("%q has no arguments", testCase.Identity)
		}
	}
}

func TestParseSourcesRejectsUnsupportedOrAmbiguousGrammar(t *testing.T) {
	if _, err := ParseSources(".", []string{"testdata/unsupported.c"}, DefaultLimits()); !errors.Is(err, ErrUnsupportedSyntax) {
		t.Fatalf("unsupported fixture error = %v, want ErrUnsupportedSyntax", err)
	}

	tests := map[string]string{
		"conditional preprocessor": "#if FEATURE\nvoid test_feature(void) {}\n#endif\n",
		"unannotated parameters":   "void test_needs_argument(int value) { (void)value; }\n",
		"unsupported matrix":       "TEST_MATRIX([1, 2])\nvoid test_matrix(int value) {}\n",
		"annotated no parameters":  "TEST_CASE(1)\nvoid test_bad(void) {}\n",
		"malformed range":          "TEST_RANGE([1, 3, 0])\nvoid test_bad(int value) {}\n",
		"qualified test":           "static inline void test_hidden(void) {}\n",
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeSource(t, root, "test.c", source)
			_, err := ParseSources(root, []string{"test.c"}, DefaultLimits())
			if !errors.Is(err, ErrUnsupportedSyntax) {
				t.Fatalf("error = %v, want ErrUnsupportedSyntax", err)
			}
			var diagnostic *SourceDiagnostic
			if !errors.As(err, &diagnostic) || diagnostic.Path != "test.c" || diagnostic.Line <= 0 {
				t.Fatalf("diagnostic = %#v", diagnostic)
			}
		})
	}
}

func TestParseSourcesPreservesCArgumentTokens(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "test.c", `
TEST_CASE(-1, 1e-3, value->field == 7)
void test_tokens(int negative, double decimal, int comparison) {}
`)
	manifest, err := ParseSources(root, []string{"test.c"}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-1", "1e-3", "value -> field == 7"}
	if got := manifest.Cases[0].Arguments; !reflect.DeepEqual(got, want) {
		t.Fatalf("Arguments = %#v, want %#v", got, want)
	}
}

func TestParseSourcesIgnoresParameterMacrosInsideOtherFunctions(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "test.c", `
typedef void (*callback)(void);
void *context;
int helper(void) {
    TEST_CASE(99);
    return 0;
}
void test_real(void) {}
`)
	manifest, err := ParseSources(root, []string{"test.c"}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Cases) != 1 || manifest.Cases[0].Identity != "test_real" {
		t.Fatalf("Cases = %#v", manifest.Cases)
	}
}

func TestParseSourcesRejectsDuplicateLogicalIdentity(t *testing.T) {
	_, err := ParseSources(".", []string{"testdata/duplicate.c", "testdata/basic.c"}, DefaultLimits())
	if !errors.Is(err, ErrDuplicateIdentity) {
		t.Fatalf("error = %v, want ErrDuplicateIdentity", err)
	}
}

func TestParseSourcesRejectsEscapesAndSourceLinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.c")
	if err := os.WriteFile(outside, []byte("void test_outside(void) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSources(root, []string{outside}, DefaultLimits()); !errors.Is(err, ErrInvalidSourcePath) {
		t.Fatalf("absolute error = %v", err)
	}
	if _, err := ParseSources(root, []string{"../outside.c"}, DefaultLimits()); !errors.Is(err, ErrInvalidSourcePath) {
		t.Fatalf("escape error = %v", err)
	}

	link := filepath.Join(root, "linked.c")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating symlink requires an unavailable Windows privilege: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := ParseSources(root, []string{"linked.c"}, DefaultLimits()); !errors.Is(err, ErrInvalidSourcePath) {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestParseSourcesSupportsUnicodeAndSpacePaths(t *testing.T) {
	root := t.TempDir()
	relative := filepath.Join("测试 目录", "用例 文件.c")
	writeSource(t, root, relative, "void test_unicode_path(void) {}\n")
	manifest, err := ParseSources(root, []string{relative}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.Cases[0].Location.Path; got != "测试 目录/用例 文件.c" {
		t.Fatalf("Location.Path = %q", got)
	}
}

func TestParseSourcesEnforcesFiniteLimits(t *testing.T) {
	root := t.TempDir()
	writeSource(t, root, "large.c", strings.Repeat(" ", 64)+"\nvoid test_large(void) {}\n")
	limits := DefaultLimits()
	limits.MaxSourceBytes = 32
	if _, err := ParseSources(root, []string{"large.c"}, limits); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("large source error = %v", err)
	}

	writeSource(t, root, "long.c", "void test_name_that_is_too_long(void) {}\n")
	limits = DefaultLimits()
	limits.MaxCaseNameBytes = 8
	if _, err := ParseSources(root, []string{"long.c"}, limits); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("long case error = %v", err)
	}

	limits = DefaultLimits()
	limits.MaxParameterInstances = 2
	if _, err := ParseSources(".", []string{"testdata/parameterized.c"}, limits); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("parameter instances error = %v", err)
	}
}

func TestParseSourcesOrderAndHashDoNotDependOnInputOrder(t *testing.T) {
	forward, err := ParseSources(".", []string{"testdata/basic.c", "testdata/parameterized.c"}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := ParseSources(".", []string{"testdata/parameterized.c", "testdata/basic.c"}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("manifests differ:\nforward=%#v\nreverse=%#v", forward, reverse)
	}
}

func TestParseSourcesRejectsInvalidUTF8AndDuplicateSource(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "invalid.c"), []byte{0xff, '\n'}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSources(root, []string{"invalid.c"}, DefaultLimits()); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("UTF-8 error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nul.c"), []byte("void test_nul(void) {}\x00\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSources(root, []string{"nul.c"}, DefaultLimits()); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("NUL error = %v", err)
	}
	writeSource(t, root, "test.c", "void test_ok(void) {}\n")
	if _, err := ParseSources(root, []string{"test.c", "./test.c"}, DefaultLimits()); !errors.Is(err, ErrInvalidSourcePath) {
		t.Fatalf("duplicate source error = %v", err)
	}
}

func writeSource(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func FuzzParseSource(f *testing.F) {
	f.Add([]byte("void test_seed(void) {}\n"))
	f.Add([]byte("TEST_CASE(1)\nvoid test_seed(int value) { (void)value; }\n"))
	f.Add([]byte("/* void test_ghost(void) {} */\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64*1024 || !utf8.Valid(data) {
			return
		}
		state := parserState{limits: DefaultLimits()}
		_, _ = parseSource("fuzz.c", data, &state)
	})
}
