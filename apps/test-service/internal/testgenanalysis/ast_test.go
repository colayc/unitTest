package testgenanalysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/probe"
)

const scalarAST = `{"kind":"TranslationUnitDecl","inner":[{"kind":"FunctionDecl","name":"choose","type":{"qualType":"int (int)"},"loc":{"line":1,"col":1},"inner":[{"kind":"ParmVarDecl","name":"x","type":{"qualType":"int"}},{"kind":"CompoundStmt","inner":[{"kind":"IfStmt","inner":[{"kind":"BinaryOperator","opcode":">","inner":[{"kind":"ImplicitCastExpr","inner":[{"kind":"DeclRefExpr","referencedDecl":{"name":"x"}}]},{"kind":"IntegerLiteral","value":"0"}]},{"kind":"ReturnStmt","inner":[{"kind":"IntegerLiteral","value":"1"}]},{"kind":"ReturnStmt","inner":[{"kind":"IntegerLiteral","value":"0"}]}]}]}]}]}`

func TestDecodeASTCreatesStablePathFreeBranchIR(t *testing.T) {
	first, err := decodeAST(strings.NewReader(scalarAST), 1<<20, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Functions) != 1 || first.Functions[0].Name != "choose" || len(first.Functions[0].Parameters) != 1 || len(first.Functions[0].Branches) != 1 {
		t.Fatalf("unexpected model: %#v", first)
	}
	if first.Functions[0].Branches[0].Kind != BranchIf || first.Functions[0].Branches[0].Predicate.Operator != ">" {
		t.Fatalf("lost branch predicate: %#v", first.Functions[0].Branches)
	}
	second, err := decodeAST(strings.NewReader(strings.ReplaceAll(scalarAST, `"line":1`, `"line":1,"file":"C:\\\\other\\\\source.c","presumedFile":"/tmp/source.c"`)), 1<<20, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("host path changed IR digest: %s != %s", first.Digest, second.Digest)
	}
	if strings.Contains(first.Digest, "source") || strings.Contains(first.Functions[0].LocationDigest, "source") {
		t.Fatal("path leaked")
	}
}

func TestNativeClangASTFixtures(t *testing.T) {
	clang := os.Getenv("UTIDE_TESTGEN_TEST_CLANG")
	if clang == "" {
		t.Skip("set UTIDE_TESTGEN_TEST_CLANG to a locally inspected Clang 22 executable")
	}
	fixtures := []struct {
		path, name string
		want       DecisionKind
	}{
		{"safe/scalar-branches.c", "choose", DecisionSupported},
		{"safe/aggregate-methods.cpp", "sum", DecisionSupported},
		{"safe/bounded-loop.c", "sum_first_four", DecisionSupported},
		{"safe/temp-file.cpp", "write_temporary", DecisionRequiresConfirmation},
		{"unsupported/macro-side-effect.cpp", "hidden_effect", DecisionUnsupported},
		{"unsupported/template-instantiation.cpp", "use_template", DecisionUnsupported},
		{"unsupported/system-api.c", "unsafe_api", DecisionUnsupported},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.path, func(t *testing.T) {
			path := filepath.Join("testdata", filepath.FromSlash(fixture.path))
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(source)
			standard := "-std=c11"
			if strings.HasSuffix(path, ".cpp") {
				standard = "-std=c++20"
			}
			result, err := probe.NewRunner().Run(context.Background(), probe.Spec{Executable: clang, Args: []string{"-Xclang", "-ast-dump=json", "-fsyntax-only", standard, path}, Env: []string{}, Timeout: 10 * time.Second, MaxOutput: maxASTBytes})
			if err != nil || result.ExitCode != 0 {
				t.Fatalf("Clang fixture failed: %v, exit=%d", err, result.ExitCode)
			}
			program, err := decodeAST(strings.NewReader(string(result.Stdout)), maxASTBytes, hex.EncodeToString(sum[:]))
			if err != nil {
				t.Fatal(err)
			}
			for _, function := range program.Functions {
				if function.Name == fixture.name {
					if function.Decision.Kind != fixture.want {
						t.Fatalf("decision %#v for %#v", function.Decision, function)
					}
					return
				}
			}
			t.Fatalf("function %s absent from %#v", fixture.name, program.Functions)
		})
	}
}

func TestDecodeASTRejectsMalformedTruncatedAndOversized(t *testing.T) {
	for _, input := range []string{`{`, `{}`, `{"kind":"TranslationUnitDecl","inner":[{"kind":"FunctionDecl"}]}`, scalarAST + `{}`} {
		if _, err := decodeAST(strings.NewReader(input), 1<<20, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	if _, err := decodeAST(strings.NewReader(scalarAST), 20, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("oversized AST accepted")
	}
}

func TestDecodeASTUsesStatementRangesAndRejectsMacroExpansion(t *testing.T) {
	input := `{"kind":"TranslationUnitDecl","inner":[{"kind":"FunctionDecl","name":"f","type":{"qualType":"int (int)"},"loc":{"line":1,"col":5},"inner":[{"kind":"ParmVarDecl","name":"x","type":{"qualType":"int"}},{"kind":"CompoundStmt","inner":[{"kind":"IfStmt","range":{"begin":{"line":2,"col":3}},"inner":[{"kind":"BinaryOperator","opcode":"<","inner":[{"kind":"DeclRefExpr","referencedDecl":{"name":"x"}},{"kind":"IntegerLiteral","value":"3"}]},{"kind":"ReturnStmt"}]},{"kind":"IfStmt","range":{"begin":{"line":3,"col":3}},"inner":[{"kind":"BinaryOperator","opcode":">","inner":[{"kind":"DeclRefExpr","referencedDecl":{"name":"x"}},{"kind":"IntegerLiteral","value":"8"}]},{"kind":"ReturnStmt"}]}]}]}]}`
	p, err := decodeAST(strings.NewReader(input), 1<<20, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Functions[0].Branches) != 2 || p.Functions[0].Branches[0].LocationDigest == p.Functions[0].Branches[1].LocationDigest {
		t.Fatalf("statement locations collapsed: %#v", p.Functions[0].Branches)
	}
	macro := strings.Replace(input, `"line":2,"col":3`, `"expansionLoc":{"line":2,"col":3},"spellingLoc":{"line":20,"col":1}`, 1)
	p, err = decodeAST(strings.NewReader(macro), 1<<20, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if p.Functions[0].Decision.Reason != ReasonMacroExpansion {
		t.Fatalf("macro accepted: %#v", p.Functions[0].Decision)
	}
}
