package testgenassert

import (
	"testing"
	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

// These tests catch wrappers that expose legacy permissive fields or bypass
// type/rule checks. Legacy Derive itself deliberately remains unchanged.
func TestTypedValueRejectsUnrelatedPopulatedFields(t *testing.T) {
	typ := analysis.Type{Kind: analysis.TypeInteger, BitWidth: 8, Signed: true}
	valid := solver.Value{Kind: analysis.TypeInteger, Integer: "1"}
	if !ValidateTypedValue(valid, typ) {
		t.Fatal("valid integer rejected")
	}
	for name, mutate := range map[string]func(*solver.Value){
		"boolean":  func(v *solver.Value) { v.Boolean = true },
		"float":    func(v *solver.Value) { v.Float = "1" },
		"enum":     func(v *solver.Value) { v.Enum = "Ok" },
		"string":   func(v *solver.Value) { v.String = "hidden" },
		"null":     func(v *solver.Value) { v.Null = true },
		"pointee":  func(v *solver.Value) { v.Pointee = &solver.Value{Kind: analysis.TypeBoolean} },
		"elements": func(v *solver.Value) { v.Elements = []solver.Value{{Kind: analysis.TypeBoolean}} },
		"fields": func(v *solver.Value) {
			v.Fields = []solver.FieldValue{{Name: "x", Value: solver.Value{Kind: analysis.TypeBoolean}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			v := valid
			mutate(&v)
			if ValidateTypedValue(v, typ) || ValidateTypedRule(v, typ, RuleEqual, "") {
				t.Fatal("unrelated field accepted")
			}
		})
	}
	nested := solver.Value{Kind: analysis.TypeArray, Elements: []solver.Value{{Kind: analysis.TypeInteger, Integer: "1", String: "hidden"}}}
	if ValidateTypedValue(nested, analysis.Type{Kind: analysis.TypeArray, Bound: 1, Element: &typ}) {
		t.Fatal("nested unrelated field accepted")
	}
}

func TestTypedValuePreservesTypeAndRuleLimits(t *testing.T) {
	integer := analysis.Type{Kind: analysis.TypeInteger, BitWidth: 8, Signed: true}
	for _, raw := range []string{"128", "-129", "01", "+1", ""} {
		if ValidateTypedValue(solver.Value{Kind: analysis.TypeInteger, Integer: raw}, integer) {
			t.Fatalf("bad integer %q accepted", raw)
		}
	}
	if ValidateTypedRule(solver.Value{Kind: analysis.TypeInteger, Integer: "1"}, integer, RuleNear, "0.1") {
		t.Fatal("integer near accepted")
	}
	floating := analysis.Type{Kind: analysis.TypeFloating, BitWidth: 64}
	value := solver.Value{Kind: analysis.TypeFloating, Float: "0.1"}
	if !ValidateTypedRule(value, floating, RuleNear, "0.001") {
		t.Fatal("valid near rejected")
	}
	for _, tolerance := range []string{"", "0", "-1", "NaN", "+Inf", "1.01"} {
		if ValidateTypedRule(value, floating, RuleNear, tolerance) {
			t.Fatalf("bad tolerance %q accepted", tolerance)
		}
	}
	if ValidateTypedRule(value, floating, RuleEqual, "") {
		t.Fatal("float equality accepted")
	}
	if ValidateTypedRule(solver.Value{Kind: analysis.TypeFloating, Float: "NaN"}, floating, RuleNear, "0.1") {
		t.Fatal("nonfinite rule value accepted")
	}
	pointer := solver.Value{Kind: analysis.TypePointer, Null: true}
	if ValidateTypedValue(pointer, analysis.Type{Kind: analysis.TypePointer, Owned: true, Element: &integer}) {
		t.Fatal("legacy unsupported pointer admitted by typed wrapper")
	}
	if ValidateTypedValue(solver.Value{Kind: analysis.TypeString, String: "/private"}, analysis.Type{Kind: analysis.TypeString, MaxLength: 256}) {
		t.Fatal("host path accepted")
	}
}

func TestTypedValueShapeDoesNotAuthorizePointerOrIntegerType(t *testing.T) {
	value := solver.Value{Kind: analysis.TypePointer, Pointee: &solver.Value{Kind: analysis.TypeInteger, Integer: "18446744073709551616"}}
	if !ValidateValueShape(value) {
		t.Fatal("finite pointer shape or canonical integer rejected before type binding")
	}
	value.Null = true
	if ValidateValueShape(value) {
		t.Fatal("ambiguous pointer accepted")
	}
}
