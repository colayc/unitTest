package casedesign

import (
	"encoding/json"

	"unit-test-ide.local/test-service/internal/managedtest"
	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	assertion "unit-test-ide.local/test-service/internal/testgenassert"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

// Leave room for the surrounding Record and frame. Wire callers must enforce
// a still lower effective ceiling when negotiated wrapper budgets require it.
const maxDefinitionBytes = 128 * 1024

// Seal validates bounded data shape and returns an independent snapshot. It
// does not authorize provenance, bind parameter types, assign revisions, or
// validate a caller's supplied digest against stored state.
func Seal(d Definition) (Definition, error) {
	if !validIdentity(d) || len(d.Inputs) > 128 || len(d.Expectations) > 128 {
		return Definition{}, ErrInvalid
	}
	total := 0
	inputs := map[string]bool{}
	for _, input := range d.Inputs {
		if !identifier(input.Name) || inputs[input.Name] || !validShape(input.Value, &total) {
			return Definition{}, ErrInvalid
		}
		inputs[input.Name] = true
	}
	targets := map[string]bool{}
	for _, e := range d.Expectations {
		if e.Provenance != "user" && e.Provenance != "proof" || !validShape(e.Value, &total) {
			return Definition{}, ErrInvalid
		}
		switch e.Target {
		case assertion.TargetReturn:
			if e.Name != "" {
				return Definition{}, ErrInvalid
			}
		case assertion.TargetOutput:
			if !identifier(e.Name) {
				return Definition{}, ErrInvalid
			}
		default:
			return Definition{}, ErrInvalid
		}
		key := string(e.Target) + "\x00" + e.Name
		if targets[key] || !validShapeRule(e.Value, e.Rule, e.Tolerance) {
			return Definition{}, ErrInvalid
		}
		targets[key] = true
	}
	d.Inputs = copyInputs(d.Inputs)
	if d.Expectations == nil {
		d.Expectations = []Expectation{}
	} else {
		d.Expectations = append([]Expectation{}, d.Expectations...)
		for i := range d.Expectations {
			d.Expectations[i].Value = copyValue(d.Expectations[i].Value)
		}
	}
	payload, err := contentBytes(d)
	if err != nil || len(payload) > maxDefinitionBytes {
		return Definition{}, ErrInvalid
	}
	d.Digest = contentDigest(payload)
	encoded, err := json.Marshal(d)
	if err != nil || len(encoded) > maxDefinitionBytes {
		return Definition{}, ErrInvalid
	}
	return d, nil
}

func NewManual(b Binding, nonce string, inputs []solver.Input, expectations []Expectation) (Definition, error) {
	if !lowerHex(nonce, 32) || len(inputs) > 128 || len(expectations) > 128 {
		return Definition{}, ErrInvalid
	}
	for _, e := range expectations {
		if e.Provenance != "user" {
			return Definition{}, ErrInvalid
		}
	}
	scenario := "manual." + nonce
	id, err := managedtest.StableCaseID(b.ProjectID, b.SourceRelativePath, b.FunctionID, scenario)
	if err != nil {
		return Definition{}, ErrInvalid
	}
	return Seal(Definition{SchemaVersion: SchemaVersion, CaseID: id, ScenarioID: scenario, Origin: OriginManual, Enabled: true, Binding: b, Inputs: inputs, Expectations: expectations})
}

func validShape(v solver.Value, total *int) bool {
	// Validate before encoding, including cyclic pointer/depth rejection.
	if !assertion.ValidateValueShape(v) {
		return false
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) > maxDefinitionBytes-*total {
		return false
	}
	*total += len(b)
	return true
}

func validShapeRule(v solver.Value, rule assertion.ComparisonRule, tolerance string) bool {
	// No source type is available at the draft boundary. Only the root float
	// rule is shape-checkable here; actual aggregate/type support is Task 5.
	if v.Kind == analysis.TypeFloating {
		return assertion.ValidateTypedRule(v, analysis.Type{Kind: analysis.TypeFloating, BitWidth: 64}, rule, tolerance)
	}
	return rule == assertion.RuleEqual && tolerance == ""
}

func copyInputs(inputs []solver.Input) []solver.Input {
	result := make([]solver.Input, len(inputs))
	copy(result, inputs)
	for i := range result {
		result[i].Value = copyValue(result[i].Value)
	}
	return result
}
func copyValue(v solver.Value) solver.Value {
	if v.Pointee != nil {
		p := copyValue(*v.Pointee)
		v.Pointee = &p
	}
	if v.Elements != nil {
		elements := make([]solver.Value, len(v.Elements))
		for i := range elements {
			elements[i] = copyValue(v.Elements[i])
		}
		v.Elements = elements
	}
	if v.Fields != nil {
		fields := make([]solver.FieldValue, len(v.Fields))
		for i := range fields {
			fields[i] = solver.FieldValue{Name: v.Fields[i].Name, Value: copyValue(v.Fields[i].Value)}
		}
		v.Fields = fields
	}
	return v
}
