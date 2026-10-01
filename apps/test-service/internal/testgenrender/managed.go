package testgenrender

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/managedtest"
	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	assert "unit-test-ide.local/test-service/internal/testgenassert"
)

type ManagedRenderInput struct {
	Program                                     analysis.Program
	ProjectID, SourceRelativePath, SourceFileID string
	Framework, GeneratorVersion                 string
	Language                                    Language
	HeaderPath                                  string
	Target                                      TargetMetadata
	Functions                                   []ManagedFunctionInput
}

type ManagedFunctionInput struct {
	FunctionID string
	SymbolID   string
	Cases      []Case
}

type ManagedFile = managedtest.ManagedFile

type managedCase struct {
	functionID string
	caseID     string
	function   analysis.Function
	derived    derivedCase
	behavior   string
}

// RenderManagedFile creates one bounded, deterministic, source-derived file.
// It does not publish, edit CMake, or read existing workspace test bytes.
func RenderManagedFile(in ManagedRenderInput) (ManagedFile, error) {
	if in.Program.Version != analysis.IRVersion || !validID(in.Program.Digest) || !validPath(in.HeaderPath) || len(in.Functions) == 0 || len(in.Functions) > 1000 || !validGeneratorVersion(in.GeneratorVersion) || !validID32(in.SourceFileID) {
		return ManagedFile{}, ErrInvalidRender
	}
	if in.Language == LanguageC && (in.Framework != "unity" || !strings.HasSuffix(in.SourceRelativePath, ".c")) || in.Language == LanguageCPP && (in.Framework != "cpputest" || !cppSource(in.SourceRelativePath)) || in.Language != LanguageC && in.Language != LanguageCPP {
		return ManagedFile{}, ErrInvalidRender
	}
	ext := ".cpp"
	if in.Language == LanguageC {
		ext = ".c"
	}
	// Retaining the complete source filename makes this mapping injective:
	// foo.cpp, foo.cc and even foo.cpp_test.cpp cannot alias one target.
	path := "tests/generated/" + in.SourceRelativePath + "_test" + ext
	if !managedtest.ValidTestPath(path) || in.Target.TestPath != "" && in.Target.TestPath != path {
		return ManagedFile{}, ErrInvalidRender
	}
	seenFunctions := map[string]bool{}
	seenSymbols := map[string]bool{}
	seenCases := map[string]bool{}
	var all []managedCase
	for _, item := range in.Functions {
		if !validID32(item.FunctionID) || !validID(item.SymbolID) || seenFunctions[item.FunctionID] || seenSymbols[item.SymbolID] || len(item.Cases) == 0 || len(item.Cases) > 1000 {
			return ManagedFile{}, ErrInvalidRender
		}
		seenFunctions[item.FunctionID], seenSymbols[item.SymbolID] = true, true
		var fn *analysis.Function
		for i := range in.Program.Functions {
			if in.Program.Functions[i].SymbolID == item.SymbolID {
				if fn != nil {
					return ManagedFile{}, ErrInvalidRender
				}
				fn = &in.Program.Functions[i]
			}
		}
		if fn == nil || fn.Decision.Kind != analysis.DecisionSupported || !identifierPattern.MatchString(fn.Name) {
			return ManagedFile{}, ErrInvalidRender
		}
		for _, c := range item.Cases {
			if !validID(c.Vector.ID) || c.Observation.SymbolID != item.SymbolID {
				return ManagedFile{}, ErrInvalidRender
			}
			id, err := managedtest.StableCaseID(in.ProjectID, in.SourceRelativePath, item.FunctionID, c.Vector.ID)
			if err != nil || seenCases[id] {
				return ManagedFile{}, ErrInvalidRender
			}
			seenCases[id] = true
			assertions, kind, err := assert.Derive(in.Program, c.Vector, c.Observation)
			if err != nil || len(assertions) == 0 {
				return ManagedFile{}, ErrInvalidRender
			}
			all = append(all, managedCase{functionID: item.FunctionID, caseID: id, function: *fn, derived: derivedCase{vector: c.Vector, assertions: assertions, kind: kind}, behavior: managedBehavior(assertions)})
			if len(all) > 4096 {
				return ManagedFile{}, ErrInvalidRender
			}
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].functionID != all[j].functionID {
			return all[i].functionID < all[j].functionID
		}
		return all[i].caseID < all[j].caseID
	})
	var out strings.Builder
	if in.Language == LanguageCPP {
		fmt.Fprintf(&out, "#include \"CppUTest/TestHarness.h\"\n#include \"%s\"\n#include <stdint.h>\n", in.HeaderPath)
	} else {
		fmt.Fprintf(&out, "#include \"unity.h\"\n#include \"%s\"\n#include <stdint.h>\n#include <stdbool.h>\n\nvoid setUp(void) {}\nvoid tearDown(void) {}\n", in.HeaderPath)
	}
	for index, c := range all {
		var body strings.Builder
		name := managedName(c.function.Name, c.behavior, c.caseID)
		if c.derived.kind == assert.KindCharacterization {
			body.WriteString("// characterization: requires separate confirmation\n")
		}
		if in.Language == LanguageCPP {
			fmt.Fprintf(&body, "TEST_GROUP(%s) {};\nTEST(%s, %s) {\n", name, name, strings.ToUpper(c.behavior[:1])+c.behavior[1:])
		} else {
			fmt.Fprintf(&body, "void %s(void) {\n", name)
		}
		if err := renderBodySections(&body, c.function, c.derived, in.Language, true); err != nil {
			return ManagedFile{}, err
		}
		body.WriteString("}\n")
		block, err := managedtest.RenderMarkers(c.caseID, c.functionID, c.function.Name, []byte(body.String()), "\n")
		if err != nil {
			return ManagedFile{}, err
		}
		if index == 0 {
			out.WriteByte('\n')
		}
		out.Write(block)
	}
	if in.Language == LanguageC {
		out.WriteString("\nint main(void) {\n  UNITY_BEGIN();\n")
		for _, c := range all {
			fmt.Fprintf(&out, "  RUN_TEST(%s);\n", managedName(c.function.Name, c.behavior, c.caseID))
		}
		out.WriteString("  return UNITY_END();\n}\n")
	}
	content := []byte(out.String())
	if len(content) > 8*1024*1024 {
		return ManagedFile{}, ErrInvalidRender
	}
	if _, err := managedtest.ParseDocument(content, int64(len(content)), 4096); err != nil {
		return ManagedFile{}, err
	}
	return ManagedFile{Path: path, Content: content}, nil
}

func validID32(s string) bool {
	return len(s) == 32 && strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' && (r < 'a' || r > 'f') }) < 0
}

func validGeneratorVersion(s string) bool {
	if s == "" || len(s) > 128 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < ' ' || r == 127 {
			return false
		}
	}
	return true
}

func cppSource(path string) bool {
	return strings.HasSuffix(path, ".cpp") || strings.HasSuffix(path, ".cc") || strings.HasSuffix(path, ".cxx")
}

func managedName(functionName, behavior, caseID string) string {
	if len(functionName) > 48 {
		functionName = functionName[:48]
	}
	return "test_" + functionName + "_" + behavior + "_" + caseID[4:]
}

func managedBehavior(assertions []assert.Assertion) string {
	hasReturn := false
	for _, a := range assertions {
		if a.Target != assert.TargetReturn {
			continue
		}
		hasReturn = true
		switch a.Expected.Kind {
		case analysis.TypeBoolean:
			if a.Expected.Boolean {
				return "returns_true"
			}
			return "returns_false"
		case analysis.TypeInteger:
			value := a.Expected.Integer
			if strings.HasPrefix(value, "-") {
				value = "minus_" + strings.TrimPrefix(value, "-")
			}
			if len(value) > 0 && len(value) <= 24 {
				return "returns_" + value
			}
		case analysis.TypeEnum:
			if identifierPattern.MatchString(a.Expected.Enum) {
				value := a.Expected.Enum
				if len(value) > 24 {
					value = value[:24]
				}
				return "returns_" + value
			}
		case analysis.TypeFloating:
			return "returns_float"
		case analysis.TypeString:
			return "returns_string"
		}
	}
	if hasReturn {
		return "returns_value"
	}
	return "updates_output"
}
