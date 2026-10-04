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

const scalarAST = `{"kind":"TranslationUnitDecl","inner":[{"kind":"FunctionDecl","name":"choose","type":{"qualType":"int (int)"},"loc":{"line":1,"col":1},"range":{"begin":{"offset":0},"end":{"offset":37,"tokLen":1}},"inner":[{"kind":"ParmVarDecl","id":"param-x","name":"x","type":{"qualType":"int"}},{"kind":"CompoundStmt","inner":[{"kind":"IfStmt","inner":[{"kind":"BinaryOperator","opcode":">","inner":[{"kind":"ImplicitCastExpr","inner":[{"kind":"DeclRefExpr","referencedDecl":{"id":"param-x","kind":"ParmVarDecl","name":"x"}}]},{"kind":"IntegerLiteral","value":"0"}]},{"kind":"ReturnStmt","inner":[{"kind":"IntegerLiteral","value":"1"}]},{"kind":"ReturnStmt","inner":[{"kind":"IntegerLiteral","value":"0"}]}]}]}]}]}`

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

func TestDecodeASTBuildsClosedReturnRulesForScalarIf(t *testing.T) {
	program, err := decodeAST(strings.NewReader(scalarAST), 1<<20, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if len(program.Functions) != 1 || len(program.Functions[0].ReturnRules) != 2 {
		t.Fatalf("return rules=%+v", program.Functions)
	}
	first, second := program.Functions[0].ReturnRules[0], program.Functions[0].ReturnRules[1]
	if len(first.Conditions) != 1 || first.Conditions[0].Operator != ">" || first.Conditions[0].Left.Name != "x" || first.Result.Value != "1" {
		t.Fatalf("first rule=%+v", first)
	}
	if len(second.Conditions) != 1 || second.Conditions[0].Operator != "!" || second.Result.Value != "0" {
		t.Fatalf("second rule=%+v", second)
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
		{"safe/temp-file.cpp", "write_temporary", DecisionUnsupported},
		{"unsupported/macro-side-effect.cpp", "hidden_effect", DecisionUnsupported},
		{"unsupported/template-instantiation.cpp", "use_template", DecisionUnsupported},
		{"unsupported/system-api.c", "unsafe_api", DecisionUnsupported},
		{"unsupported/persistent-local.cpp", "static_counter", DecisionUnsupported},
		{"unsupported/persistent-local.cpp", "thread_counter", DecisionUnsupported},
		{"unsupported/persistent-local.cpp", "external_counter_write", DecisionUnsupported},
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

func TestNativeSwitchIncludesCaseAndDefaultEdges(t *testing.T) {
	clang := os.Getenv("UTIDE_TESTGEN_TEST_CLANG")
	if clang == "" {
		t.Skip("requires local Clang 22")
	}
	path := filepath.Join("testdata", "safe", "scalar-branches.c")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(source)
	r, err := probe.NewRunner().Run(context.Background(), probe.Spec{Executable: clang, Args: []string{"-Xclang", "-ast-dump=json", "-fsyntax-only", "-nostdinc", "-nostdinc++", "-std=c11", path}, Env: []string{}, Timeout: 10 * time.Second, MaxOutput: maxASTBytes})
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("clang: %v/%d", err, r.ExitCode)
	}
	p, err := decodeAST(strings.NewReader(string(r.Stdout)), maxASTBytes, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	var cases, defaults int
	for _, b := range p.Functions[0].Branches {
		if b.Kind == BranchCase {
			cases++
		}
		if b.Kind == BranchDefault {
			defaults++
		}
	}
	if cases != 1 || defaults != 1 {
		t.Fatalf("switch edges case=%d default=%d: %#v", cases, defaults, p.Functions[0].Branches)
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

func TestDecodeASTRejectsIncompletePredicatesAndUnmodeledLocalEffects(t *testing.T) {
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for name, statement := range map[string]string{
		"arithmetic if":     `{"kind":"IfStmt","inner":[{"kind":"BinaryOperator","opcode":">","inner":[{"kind":"BinaryOperator","opcode":"+","inner":[{"kind":"DeclRefExpr","referencedDecl":{"name":"x"}},{"kind":"IntegerLiteral","value":"1"}]},{"kind":"IntegerLiteral","value":"0"}]},{"kind":"ReturnStmt"}]}`,
		"arithmetic switch": `{"kind":"SwitchStmt","inner":[{"kind":"BinaryOperator","opcode":"+","inner":[{"kind":"DeclRefExpr","referencedDecl":{"name":"x"}},{"kind":"IntegerLiteral","value":"1"}]},{"kind":"CompoundStmt","inner":[{"kind":"CaseStmt","inner":[{"kind":"ConstantExpr","inner":[{"kind":"IntegerLiteral","value":"1"}]},{"kind":"ReturnStmt"}]}]}]}`,
		"conditional":       `{"kind":"ConditionalOperator","inner":[{"kind":"BinaryOperator","opcode":">","inner":[{"kind":"DeclRefExpr","referencedDecl":{"name":"x"}},{"kind":"IntegerLiteral","value":"0"}]},{"kind":"IntegerLiteral","value":"1"},{"kind":"IntegerLiteral","value":"0"}]}`,
		"volatile local":    `{"kind":"DeclStmt","inner":[{"kind":"VarDecl","name":"v","type":{"qualType":"volatile int"},"inner":[{"kind":"IntegerLiteral","value":"0"}]}]}`,
		"constructor":       `{"kind":"DeclStmt","inner":[{"kind":"VarDecl","name":"v","type":{"qualType":"Danger"},"inner":[{"kind":"CXXConstructExpr"}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			input := `{"kind":"TranslationUnitDecl","inner":[{"kind":"FunctionDecl","name":"f","type":{"qualType":"int (int)"},"loc":{"line":1,"col":1},"inner":[{"kind":"ParmVarDecl","name":"x","type":{"qualType":"int"}},{"kind":"CompoundStmt","inner":[` + statement + `]}]}]}`
			p, err := decodeAST(strings.NewReader(input), 1<<20, digest)
			if err != nil {
				t.Fatal(err)
			}
			if p.Functions[0].Decision.Kind == DecisionSupported {
				t.Fatalf("unsafe %s accepted: %#v", name, p.Functions[0])
			}
		})
	}
}

func TestDecodeASTRejectsConstructorAndUnsafeMethodReceiver(t *testing.T) {
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for name, decl := range map[string]string{
		"orphan method":   `{"kind":"CXXMethodDecl","name":"read","type":{"qualType":"int ()"},"loc":{"line":1,"col":1},"inner":[{"kind":"CompoundStmt"}]}`,
		"constructor":     `{"kind":"CXXConstructorDecl","name":"Danger","type":{"qualType":"void ()"},"loc":{"line":1,"col":1},"inner":[{"kind":"CompoundStmt"}]}`,
		"unsafe receiver": `{"kind":"CXXRecordDecl","name":"Danger","completeDefinition":true,"definitionData":{"isPOD":false,"isTrivial":false},"inner":[{"kind":"CXXMethodDecl","name":"read","type":{"qualType":"int ()"},"loc":{"line":1,"col":1},"inner":[{"kind":"CompoundStmt","inner":[{"kind":"ReturnStmt","inner":[{"kind":"IntegerLiteral","value":"1"}]}]}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			input := `{"kind":"TranslationUnitDecl","inner":[` + decl + `]}`
			p, err := decodeAST(strings.NewReader(input), 1<<20, digest)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Functions) != 1 || p.Functions[0].Decision.Kind == DecisionSupported {
				t.Fatalf("unmodeled receiver/constructor accepted: %#v", p.Functions)
			}
		})
	}
}

func TestDecodeASTRejectsGlobalAndUnprovenArrayWrites(t *testing.T) {
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for name, statement := range map[string]string{
		"global increment": `{"kind":"ReturnStmt","inner":[{"kind":"UnaryOperator","opcode":"++","inner":[{"kind":"DeclRefExpr","referencedDecl":{"id":"global-g","kind":"VarDecl","name":"g"}}]}]}`,
		"array write":      `{"kind":"BinaryOperator","opcode":"=","inner":[{"kind":"ArraySubscriptExpr","inner":[{"kind":"DeclRefExpr","referencedDecl":{"id":"local-a","kind":"VarDecl","name":"a"}},{"kind":"IntegerLiteral","value":"0"}]},{"kind":"IntegerLiteral","value":"1"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			input := `{"kind":"TranslationUnitDecl","inner":[{"kind":"VarDecl","id":"global-g","name":"g","type":{"qualType":"int"}},{"kind":"FunctionDecl","name":"f","type":{"qualType":"int ()"},"loc":{"line":1,"col":1},"inner":[{"kind":"CompoundStmt","inner":[{"kind":"DeclStmt","inner":[{"kind":"VarDecl","id":"local-a","name":"a","type":{"qualType":"int[2]"}}]},` + statement + `]}]}]}`
			p, err := decodeAST(strings.NewReader(input), 1<<20, digest)
			if err != nil {
				t.Fatal(err)
			}
			if p.Functions[0].Decision.Kind == DecisionSupported || p.Functions[0].Effect == EffectLocalMemory {
				t.Fatalf("unproven effect accepted: %#v", p.Functions[0])
			}
		})
	}
}

func TestDecodeASTRejectsNonAutomaticLocalStorage(t *testing.T) {
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name, attributes string
		want             DecisionKind
	}{
		{"implicit auto", "", DecisionSupported},
		{"explicit auto", `,"storageClass":"auto"`, DecisionSupported},
		{"static", `,"storageClass":"static"`, DecisionUnsupported},
		{"extern", `,"storageClass":"extern"`, DecisionUnsupported},
		{"thread local", `,"tls":"dynamic"`, DecisionUnsupported},
		{"thread local alternate", `,"tlsKind":"dynamic"`, DecisionUnsupported},
		{"unknown storage", `,"storageClass":"register"`, DecisionUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := `{"kind":"TranslationUnitDecl","inner":[{"kind":"FunctionDecl","name":"f","type":{"qualType":"int ()"},"loc":{"line":1,"col":1},"inner":[{"kind":"CompoundStmt","inner":[{"kind":"DeclStmt","inner":[{"kind":"VarDecl","id":"local-v","name":"v","type":{"qualType":"int"}` + tc.attributes + `,"inner":[{"kind":"IntegerLiteral","value":"0"}]}]},{"kind":"BinaryOperator","opcode":"=","inner":[{"kind":"DeclRefExpr","referencedDecl":{"id":"local-v","kind":"VarDecl","name":"v"}},{"kind":"IntegerLiteral","value":"1"}]},{"kind":"ReturnStmt","inner":[{"kind":"DeclRefExpr","referencedDecl":{"id":"local-v","kind":"VarDecl","name":"v"}}]}]}]}]}`
			p, err := decodeAST(strings.NewReader(input), 1<<20, digest)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Functions) != 1 || p.Functions[0].Decision.Kind != tc.want {
				t.Fatalf("storage %q: decision %#v", tc.name, p.Functions)
			}
			if tc.want == DecisionUnsupported && p.Functions[0].Effect == EffectLocalMemory {
				t.Fatalf("persistent local falsely classified as local-memory: %#v", p.Functions[0])
			}
		})
	}
}

func TestDecodeASTKeepsNestedSwitchEdgesSeparate(t *testing.T) {
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	input := `{"kind":"TranslationUnitDecl","inner":[{"kind":"FunctionDecl","name":"f","type":{"qualType":"int (int)"},"loc":{"line":1,"col":1},"inner":[{"kind":"ParmVarDecl","name":"x","type":{"qualType":"int"}},{"kind":"CompoundStmt","inner":[{"kind":"SwitchStmt","range":{"begin":{"line":2,"col":1}},"inner":[{"kind":"DeclRefExpr","referencedDecl":{"name":"x"}},{"kind":"CompoundStmt","inner":[{"kind":"CaseStmt","range":{"begin":{"line":3,"col":1}},"inner":[{"kind":"IntegerLiteral","value":"1"},{"kind":"SwitchStmt","range":{"begin":{"line":4,"col":1}},"inner":[{"kind":"DeclRefExpr","referencedDecl":{"name":"x"}},{"kind":"CompoundStmt","inner":[{"kind":"CaseStmt","range":{"begin":{"line":5,"col":1}},"inner":[{"kind":"IntegerLiteral","value":"2"},{"kind":"ReturnStmt"}]}]}]}]},{"kind":"DefaultStmt","range":{"begin":{"line":6,"col":1}},"inner":[{"kind":"ReturnStmt"}]}]}]}]}]}]}`
	p, err := decodeAST(strings.NewReader(input), 1<<20, digest)
	if err != nil {
		t.Fatal(err)
	}
	if p.Functions[0].Decision.Kind == DecisionSupported {
		t.Fatalf("inner switch with no default accepted: %#v", p.Functions[0].Branches)
	}
}

func TestArrayReadRejectsShadowedLoopIndexIdentity(t *testing.T) {
	node := &astNode{Kind: "ArraySubscriptExpr", Inner: []*astNode{{Kind: "DeclRefExpr", ReferencedDecl: &astReference{ID: "local-array", Kind: "VarDecl", Name: "values"}}, {Kind: "DeclRefExpr", ReferencedDecl: &astReference{ID: "shadow-index", Kind: "VarDecl", Name: "i"}}}}
	if arrayReadProven(node, map[string]int{"local-array": 4}, map[string]int{"i": 4}) {
		t.Fatal("name-only loop proof accepted a shadowed index")
	}
}
