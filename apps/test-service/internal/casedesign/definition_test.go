package casedesign

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	assertion "unit-test-ide.local/test-service/internal/testgenassert"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

func bindingFixture() Binding {
	return Binding{ProjectID: "demo", SourceRelativePath: "src/math.c", FileID: strings.Repeat("a", 32), FunctionID: strings.Repeat("b", 32), SymbolID: strings.Repeat("c", 64), SourceDigest: strings.Repeat("d", 64), WorkspaceGeneration: strings.Repeat("e", 64), ToolchainID: "clang-test", TargetID: strings.Repeat("f", 64), ProgramDigest: strings.Repeat("1", 64), BuildConfigDigest: strings.Repeat("2", 64), Framework: "unity"}
}
func manualFixture(t *testing.T) Definition {
	t.Helper()
	d, err := NewManual(bindingFixture(), strings.Repeat("3", 32), []solver.Input{{Name: "x", Value: solver.Value{Kind: analysis.TypeInteger, Integer: "0"}}}, []Expectation{{Target: assertion.TargetReturn, Value: solver.Value{Kind: analysis.TypeInteger, Integer: "1"}, Rule: assertion.RuleEqual, Provenance: "user"}})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Catches identity derived from mutable content and digests including revision.
func TestCaseIdentitySurvivesInputEdit(t *testing.T) {
	first := manualFixture(t)
	edited := first
	edited.Inputs = append([]solver.Input(nil), first.Inputs...)
	edited.Inputs[0].Value.Integer = "2"
	edited, err := Seal(edited)
	if err != nil || edited.CaseID != first.CaseID || edited.Digest == first.Digest {
		t.Fatal("identity/content not separated", err)
	}
	if first.Inputs[0].Value.Integer != "0" {
		t.Fatal("caller mutation changed sealed value")
	}
	revision := first
	revision.Revision = 42
	revision.Digest = strings.Repeat("0", 64)
	resealed, err := Seal(revision)
	if err != nil || resealed.Digest != first.Digest {
		t.Fatal("revision or old digest affected content digest", err)
	}
}

// Catches shallow copy at every Value container (not merely slice headers).
func TestCaseIdentitySealRecursivelyCopiesValues(t *testing.T) {
	leaf := solver.Value{Kind: analysis.TypeInteger, Integer: "7"}
	value := solver.Value{Kind: analysis.TypeRecord, Fields: []solver.FieldValue{{Name: "items", Value: solver.Value{Kind: analysis.TypeArray, Elements: []solver.Value{{Kind: analysis.TypePointer, Pointee: &leaf}}}}}}
	inputs := []solver.Input{{Name: "x", Value: value}}
	expectations := []Expectation{{Target: assertion.TargetOutput, Name: "x", Value: value, Rule: assertion.RuleEqual, Provenance: "user"}}
	sealed, err := NewManual(bindingFixture(), strings.Repeat("3", 32), inputs, expectations)
	if err != nil {
		t.Fatal(err)
	}
	leaf.Integer = "9"
	inputs[0].Name = "changed"
	value.Fields[0].Name = "changed"
	value.Fields[0].Value.Elements[0].Null = true
	expectations[0].Name = "changed"
	for _, got := range []solver.Value{sealed.Inputs[0].Value, sealed.Expectations[0].Value} {
		if got.Fields[0].Name != "items" || got.Fields[0].Value.Elements[0].Null || got.Fields[0].Value.Elements[0].Pointee.Integer != "7" {
			t.Fatal("recursive input alias retained")
		}
	}
	sealed.Inputs[0].Value.Fields[0].Value.Elements[0].Pointee.Integer = "10"
	if sealed.Expectations[0].Value.Fields[0].Value.Elements[0].Pointee.Integer != "7" {
		t.Fatal("input and expectation retain shared alias")
	}
}

func TestCaseIdentityRejectsTamperingAndMalformedBinding(t *testing.T) {
	for name, mutate := range map[string]func(*Definition){
		"case":      func(d *Definition) { d.CaseID = "utc_" + strings.Repeat("0", 32) },
		"scenario":  func(d *Definition) { d.ScenarioID = "manual.other" },
		"schema":    func(d *Definition) { d.SchemaVersion = 2 },
		"origin":    func(d *Definition) { d.Origin = "admin" },
		"digest":    func(d *Definition) { d.Digest = "bad" },
		"project":   func(d *Definition) { d.Binding.ProjectID = "../demo" },
		"path":      func(d *Definition) { d.Binding.SourceRelativePath = "../math.c" },
		"file":      func(d *Definition) { d.Binding.FileID = strings.Repeat("A", 32) },
		"function":  func(d *Definition) { d.Binding.FunctionID = "bad" },
		"symbol":    func(d *Definition) { d.Binding.SymbolID = "bad" },
		"source":    func(d *Definition) { d.Binding.SourceDigest = "bad" },
		"workspace": func(d *Definition) { d.Binding.WorkspaceGeneration = "bad" },
		"toolchain": func(d *Definition) { d.Binding.ToolchainID = "clang\nsecret" },
		"target":    func(d *Definition) { d.Binding.TargetID = "bad" },
		"program":   func(d *Definition) { d.Binding.ProgramDigest = "bad" },
		"build":     func(d *Definition) { d.Binding.BuildConfigDigest = "bad" },
		"framework": func(d *Definition) { d.Binding.Framework = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			d := manualFixture(t)
			mutate(&d)
			if _, err := Seal(d); err != ErrInvalid {
				t.Fatalf("malformed definition accepted: %v", err)
			}
		})
	}
	for _, nonce := range []string{"", "BAD", strings.Repeat("3", 33)} {
		if _, err := NewManual(bindingFixture(), nonce, nil, nil); err != ErrInvalid {
			t.Fatal("malformed nonce accepted", err)
		}
	}
}

func TestCaseIdentityRejectsInvalidValueAndExpectationShapes(t *testing.T) {
	cycle := solver.Value{Kind: analysis.TypePointer}
	cycle.Pointee = &cycle
	deep := solver.Value{Kind: analysis.TypeInteger, Integer: "0"}
	for i := 0; i < 5; i++ {
		v := deep
		deep = solver.Value{Kind: analysis.TypePointer, Pointee: &v}
	}
	cases := map[string]solver.Value{
		"unrelated":       {Kind: analysis.TypeInteger, Integer: "1", String: "hidden"},
		"noncanonical":    {Kind: analysis.TypeInteger, Integer: "01"},
		"nonfinite":       {Kind: analysis.TypeFloating, Float: "NaN"},
		"host-path":       {Kind: analysis.TypeString, String: "C:/private"},
		"string-limit":    {Kind: analysis.TypeString, String: strings.Repeat("a", 257)},
		"unknown":         {Kind: "unknown"},
		"empty-array":     {Kind: analysis.TypeArray},
		"oversized-array": {Kind: analysis.TypeArray, Elements: make([]solver.Value, 17)},
		"duplicate-field": {Kind: analysis.TypeRecord, Fields: []solver.FieldValue{{Name: "x", Value: solver.Value{Kind: analysis.TypeBoolean}}, {Name: "x", Value: solver.Value{Kind: analysis.TypeBoolean}}}},
		"pointer-neither": {Kind: analysis.TypePointer},
		"pointer-both":    {Kind: analysis.TypePointer, Null: true, Pointee: &solver.Value{Kind: analysis.TypeBoolean}},
		"cycle":           cycle, "depth": deep,
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			d := manualFixture(t)
			d.Inputs[0].Value = v
			if _, err := Seal(d); err != ErrInvalid {
				t.Fatal("invalid value shape accepted", err)
			}
		})
	}
	for name, mutate := range map[string]func(*Definition){
		"input-name":         func(d *Definition) { d.Inputs[0].Name = "x;evil" },
		"duplicate-input":    func(d *Definition) { d.Inputs = append(d.Inputs, d.Inputs[0]) },
		"target":             func(d *Definition) { d.Expectations[0].Target = assertion.TargetInput },
		"return-name":        func(d *Definition) { d.Expectations[0].Name = "x" },
		"output-name":        func(d *Definition) { d.Expectations[0].Target = assertion.TargetOutput },
		"rule":               func(d *Definition) { d.Expectations[0].Rule = "unknown" },
		"tolerance":          func(d *Definition) { d.Expectations[0].Tolerance = "0.1" },
		"provenance":         func(d *Definition) { d.Expectations[0].Provenance = "observed" },
		"duplicate-target":   func(d *Definition) { d.Expectations = append(d.Expectations, d.Expectations[0]) },
		"input-budget":       func(d *Definition) { d.Inputs = make([]solver.Input, 129) },
		"expectation-budget": func(d *Definition) { d.Expectations = make([]Expectation, 129) },
	} {
		t.Run(name, func(t *testing.T) {
			d := manualFixture(t)
			mutate(&d)
			if _, err := Seal(d); err != ErrInvalid {
				t.Fatal("invalid shape accepted", err)
			}
		})
	}
}

func TestCaseIdentityDraftShapeIsNotProvenanceAuthority(t *testing.T) {
	d, err := NewManual(bindingFixture(), strings.Repeat("3", 32), nil, nil)
	if err != nil || !d.Enabled || d.SchemaVersion != 1 || d.Origin != "manual" || d.ScenarioID != "manual."+strings.Repeat("3", 32) {
		t.Fatal("empty draft defaults invalid", err)
	}
	proof := manualFixture(t)
	proof.Expectations[0].Provenance = "proof"
	if _, err := Seal(proof); err != nil {
		t.Fatal("shape validator attempted provenance authorization", err)
	}
	if _, err := NewManual(bindingFixture(), strings.Repeat("3", 32), proof.Inputs, proof.Expectations); err != ErrInvalid {
		t.Fatal("manual creation accepted caller-selected proof", err)
	}
}

func TestCaseIdentityBoundsTotalDefinition(t *testing.T) {
	value := solver.Value{Kind: analysis.TypeString, String: strings.Repeat("a", 256)}
	for depth := 0; depth < 4; depth++ {
		elements := make([]solver.Value, 16)
		for i := range elements {
			elements[i] = value
		}
		value = solver.Value{Kind: analysis.TypeArray, Elements: elements}
	}
	d := manualFixture(t)
	d.Inputs[0].Value = value
	if _, err := Seal(d); err != ErrInvalid {
		t.Fatal("oversized aggregate accepted", err)
	}
}

func TestCaseIdentityReservesFrameSpaceAbove128KiB(t *testing.T) {
	d := manualFixture(t)
	elements := make([]solver.Value, 16)
	for i := range elements {
		elements[i] = solver.Value{Kind: analysis.TypeString, String: strings.Repeat("a", 256)}
	}
	d.Inputs = make([]solver.Input, 32)
	for i := range d.Inputs {
		d.Inputs[i] = solver.Input{Name: "x" + strconv.Itoa(i), Value: solver.Value{Kind: analysis.TypeArray, Elements: elements}}
	}
	if _, err := Seal(d); err != ErrInvalid {
		t.Fatal("definition exceeding 128 KiB accepted without frame headroom", err)
	}
}

func TestCaseIdentityDigestBindsAllIntent(t *testing.T) {
	for name, mutate := range map[string]func(*Definition){
		"enabled":    func(d *Definition) { d.Enabled = false },
		"binding":    func(d *Definition) { d.Binding.SourceDigest = strings.Repeat("0", 64) },
		"expected":   func(d *Definition) { d.Expectations[0].Value.Integer = "2" },
		"provenance": func(d *Definition) { d.Expectations[0].Provenance = "proof" },
	} {
		t.Run(name, func(t *testing.T) {
			d := manualFixture(t)
			prior := d.Digest
			mutate(&d)
			got, err := Seal(d)
			if err != nil || got.Digest == prior {
				t.Fatal("mutable intent missing from digest", err)
			}
		})
	}
}

func TestCaseIdentityRejectsInvalidUTF8Strings(t *testing.T) {
	for _, raw := range []string{"\xff", "\xfe"} {
		leaf := solver.Value{Kind: analysis.TypeString, String: raw}
		nested := solver.Value{Kind: analysis.TypeRecord, Fields: []solver.FieldValue{{Name: "text", Value: solver.Value{Kind: analysis.TypeArray, Elements: []solver.Value{{Kind: analysis.TypePointer, Pointee: &leaf}}}}}}
		for name, value := range map[string]solver.Value{"scalar": leaf, "nested": nested} {
			for _, target := range []string{"input", "expectation"} {
				t.Run(name+"/"+target+"/"+strconv.Itoa(int(raw[0])), func(t *testing.T) {
					d := manualFixture(t)
					if target == "input" {
						d.Inputs[0].Value = value
					} else {
						d.Expectations[0].Value = value
					}
					if _, err := Seal(d); err != ErrInvalid {
						t.Fatalf("invalid UTF-8 %x accepted: %v", []byte(raw), err)
					}
				})
			}
		}
	}
}

func TestCaseIdentityValidUTF8JSONRoundTripPreservesDigest(t *testing.T) {
	digests := map[string]bool{}
	for _, raw := range []string{"\ufffd", "text\ufffd", "你好", "plain"} {
		leaf := solver.Value{Kind: analysis.TypeString, String: raw}
		nested := solver.Value{Kind: analysis.TypeRecord, Fields: []solver.FieldValue{{Name: "text", Value: solver.Value{Kind: analysis.TypeArray, Elements: []solver.Value{{Kind: analysis.TypePointer, Pointee: &leaf}}}}}}
		d := manualFixture(t)
		d.Inputs[0].Value = nested
		d.Expectations[0].Value = leaf
		sealed, err := Seal(d)
		if err != nil {
			t.Fatalf("valid UTF-8 %q rejected: %v", raw, err)
		}
		encoded, err := json.Marshal(sealed)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Definition
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Inputs[0].Value.Fields[0].Value.Elements[0].Pointee.String != raw || decoded.Expectations[0].Value.String != raw {
			t.Fatal("accepted string bytes changed during JSON round-trip")
		}
		resealed, err := Seal(decoded)
		if err != nil || resealed.Digest != sealed.Digest {
			t.Fatal("JSON round-trip changed content digest", err)
		}
		if digests[sealed.Digest] {
			t.Fatal("distinct accepted strings share content digest")
		}
		digests[sealed.Digest] = true
	}
}
