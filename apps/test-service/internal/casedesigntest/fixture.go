// Package casedesigntest provides fixed typed data for case-design tests. It
// supplies no product mock, source binding, generator, or execution authority.
package casedesigntest

import (
	"strings"
	"testing"
	"unit-test-ide.local/test-service/internal/casedesign"
	"unit-test-ide.local/test-service/internal/testgenanalysis"
	"unit-test-ide.local/test-service/internal/testgenassert"
	"unit-test-ide.local/test-service/internal/testgensolver"
)

func Definition(t testing.TB) casedesign.Definition {
	t.Helper()
	b := casedesign.Binding{ProjectID: "demo", SourceRelativePath: "src/math.c",
		FileID: strings.Repeat("a", 32), FunctionID: strings.Repeat("b", 32), SymbolID: strings.Repeat("c", 64),
		SourceDigest: strings.Repeat("d", 64), WorkspaceGeneration: strings.Repeat("e", 64), ToolchainID: "clang-test",
		TargetID: strings.Repeat("f", 64), ProgramDigest: strings.Repeat("1", 64), BuildConfigDigest: strings.Repeat("2", 64), Framework: "unity"}
	d, err := casedesign.NewManual(b, strings.Repeat("3", 32),
		[]testgensolver.Input{{Name: "x", Value: testgensolver.Value{Kind: testgenanalysis.TypeInteger, Integer: "0"}}},
		[]casedesign.Expectation{{Target: testgenassert.TargetReturn, Value: testgensolver.Value{Kind: testgenanalysis.TypeInteger, Integer: "1"}, Rule: testgenassert.RuleEqual, Provenance: "user"}})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
