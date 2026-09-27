package testgenassert

import (
	"testing"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

const hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const hashC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
const hashD = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

func fixture() (analysis.Program, solver.InputVector, Observation) {
	v := solver.InputVector{ID: hashB, Inputs: []solver.Input{{Name: "x", Value: solver.Value{Kind: analysis.TypeInteger, Integer: "2"}}}}
	p := analysis.Program{Version: analysis.IRVersion, Digest: hashA, Functions: []analysis.Function{{
		SymbolID: hashC, Name: "choose", ReturnType: analysis.Type{Kind: analysis.TypeInteger, BitWidth: 32, Signed: true},
		Parameters: []analysis.Parameter{{Name: "x", Type: analysis.Type{Kind: analysis.TypeInteger, BitWidth: 32, Signed: true}}},
		Excerpt:    analysis.SourceExcerpt{Digest: hashD}, Decision: analysis.Decision{Kind: analysis.DecisionSupported, Reason: analysis.ReasonNone},
	}}}
	o := Observation{SymbolID: hashC, CandidateID: hashB, Evidence: []Evidence{{
		Kind: EvidenceReturnContract, Target: TargetReturn, TargetDigest: hashC, SourceDigest: hashD,
		Expected: solver.Value{Kind: analysis.TypeInteger, Integer: "7"}, Rule: RuleEqual,
		Stability: StabilityDeterministic,
	}}}
	return p, v, o
}

func TestDeriveRequiresIndependentBoundProof(t *testing.T) {
	p, v, o := fixture()
	// A proof missing from the safe IR must never become verified merely because
	// an observation labels itself "contract".
	if _, _, err := Derive(p, v, o); err == nil {
		t.Fatal("unproven claimed contract accepted")
	}
	p.Functions[0].OracleProofs = []analysis.OracleProof{{Kind: string(EvidenceReturnContract), CandidateID: hashB, TargetDigest: hashC, SourceDigest: hashD, ExpectedDigest: ValueDigest(o.Evidence[0].Expected), Rule: string(RuleEqual)}}
	got, kind, err := Derive(p, v, o)
	if err != nil || kind != KindVerified || len(got) != 1 || got[0].TargetDigest != hashC || got[0].Expected.Integer != "7" {
		t.Fatalf("got=%+v kind=%s err=%v", got, kind, err)
	}
	o.Evidence[0].Expected.Integer = "8"
	if _, _, err := Derive(p, v, o); err == nil {
		t.Fatal("self-fulfilling replacement accepted")
	}
}

func TestDeriveObservationOnlyIsCharacterization(t *testing.T) {
	p, v, o := fixture()
	o.Evidence[0].Kind = EvidenceObservedOutput
	o.Evidence[0].StabilityDigest = hashA
	o.Evidence[0].RepeatCount = 2
	got, kind, err := Derive(p, v, o)
	if err != nil || kind != KindCharacterization || len(got) != 1 || got[0].Provenance != EvidenceObservedOutput {
		t.Fatalf("got=%+v kind=%s err=%v", got, kind, err)
	}
	o.Evidence = nil
	if _, _, err := Derive(p, v, o); err == nil {
		t.Fatal("assertionless probe accepted")
	}
}

func TestObservedOutputRequiresRepeatStabilityEvidence(t *testing.T) {
	p, v, o := fixture()
	o.Evidence[0].Kind = EvidenceObservedOutput
	if _, _, err := Derive(p, v, o); err == nil {
		t.Fatal("single observation accepted")
	}
	o.Evidence[0].StabilityDigest = hashA
	o.Evidence[0].RepeatCount = 1
	if _, _, err := Derive(p, v, o); err == nil {
		t.Fatal("one repeat accepted")
	}
}

func TestDeriveRejectsMixedProvenance(t *testing.T) {
	p, v, o := fixture()
	p.Functions[0].OracleProofs = []analysis.OracleProof{{Kind: string(EvidenceReturnContract), CandidateID: hashB, TargetDigest: hashC, SourceDigest: hashD, ExpectedDigest: ValueDigest(o.Evidence[0].Expected), Rule: string(RuleEqual)}}
	observed := o.Evidence[0]
	observed.Kind = EvidenceObservedOutput
	observed.StabilityDigest = hashA
	observed.RepeatCount = 2
	o.Evidence = append(o.Evidence, observed)
	if _, _, err := Derive(p, v, o); err == nil {
		t.Fatal("mixed verified and characterization evidence accepted")
	}
}

func TestDeriveRejectsUnstableAndInputEcho(t *testing.T) {
	for name, mutate := range map[string]func(*Observation){
		"input-echo": func(o *Observation) { o.Evidence[0].Expected.Integer = "2"; o.Evidence[0].Target = TargetInput },
		"pointer-address": func(o *Observation) {
			o.Evidence[0].Expected = solver.Value{Kind: analysis.TypePointer, Pointee: &solver.Value{Kind: analysis.TypeInteger, Integer: "1"}}
		},
		"timestamp":    func(o *Observation) { o.Evidence[0].Stability = StabilityTimestamp },
		"random":       func(o *Observation) { o.Evidence[0].Stability = StabilityRandom },
		"nonfinite":    func(o *Observation) { o.Evidence[0].Expected = solver.Value{Kind: analysis.TypeFloating, Float: "NaN"} },
		"wrong-symbol": func(o *Observation) { o.SymbolID = hashA },
		"wrong-input":  func(o *Observation) { o.CandidateID = hashA },
		"wrong-source": func(o *Observation) { o.Evidence[0].SourceDigest = hashA },
	} {
		t.Run(name, func(t *testing.T) {
			p, v, o := fixture()
			mutate(&o)
			if _, _, err := Derive(p, v, o); err == nil {
				t.Fatal("unsafe evidence accepted")
			}
		})
	}
}

func TestDeriveTypedBooleanEnumAndBoundedOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		typ    analysis.Type
		value  solver.Value
		target TargetKind
	}{
		{"bool", analysis.Type{Kind: analysis.TypeBoolean}, solver.Value{Kind: analysis.TypeBoolean, Boolean: true}, TargetReturn},
		{"enum", analysis.Type{Kind: analysis.TypeEnum, EnumValues: []string{"Ok", "Err"}}, solver.Value{Kind: analysis.TypeEnum, Enum: "Err"}, TargetReturn},
		{"buffer", analysis.Type{Kind: analysis.TypeArray, Bound: 2, Element: &analysis.Type{Kind: analysis.TypeInteger, BitWidth: 8, Signed: false}}, solver.Value{Kind: analysis.TypeArray, Elements: []solver.Value{{Kind: analysis.TypeInteger, Integer: "1"}, {Kind: analysis.TypeInteger, Integer: "2"}}}, TargetOutput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, v, o := fixture()
			if tc.target == TargetReturn {
				p.Functions[0].ReturnType = tc.typ
			} else {
				p.Functions[0].Parameters = append(p.Functions[0].Parameters, analysis.Parameter{Name: "out", Type: tc.typ})
				v.Inputs = append(v.Inputs, solver.Input{Name: "out", Value: solver.Value{Kind: analysis.TypeArray, Elements: []solver.Value{{Kind: analysis.TypeInteger, Integer: "0"}, {Kind: analysis.TypeInteger, Integer: "0"}}}})
				o.Evidence[0].TargetName = "out"
			}
			o.Evidence[0].Target = tc.target
			o.Evidence[0].Expected = tc.value
			p.Functions[0].OracleProofs = []analysis.OracleProof{{Kind: string(EvidenceReturnContract), CandidateID: hashB, TargetDigest: hashC, SourceDigest: hashD, ExpectedDigest: ValueDigest(tc.value), Rule: string(RuleEqual)}}
			a, k, err := Derive(p, v, o)
			if err != nil || k != KindVerified || len(a) != 1 {
				t.Fatalf("%v %v %v", a, k, err)
			}
		})
	}
}

func TestDeriveRejectsHostPathAndUnstableFloat(t *testing.T) {
	p, v, o := fixture()
	o.Evidence[0].Expected = solver.Value{Kind: analysis.TypeString, String: `C:\\private\\secret`}
	p.Functions[0].ReturnType = analysis.Type{Kind: analysis.TypeString, MaxLength: 256}
	if _, _, err := Derive(p, v, o); err == nil {
		t.Fatal("host path accepted")
	}
	p, v, o = fixture()
	o.Evidence[0].Expected = solver.Value{Kind: analysis.TypeFloating, Float: "0.1"}
	p.Functions[0].ReturnType = analysis.Type{Kind: analysis.TypeFloating, BitWidth: 64}
	if _, _, err := Derive(p, v, o); err == nil {
		t.Fatal("floating equality without tolerance accepted")
	}
	o.Evidence[0].Rule = RuleNear
	o.Evidence[0].Tolerance = "0.001"
	p.Functions[0].OracleProofs = []analysis.OracleProof{{Kind: string(EvidenceReturnContract), CandidateID: hashB, TargetDigest: hashC, SourceDigest: hashD, ExpectedDigest: ValueDigest(o.Evidence[0].Expected), Rule: string(RuleNear), Tolerance: "0.001"}}
	if _, k, err := Derive(p, v, o); err != nil || k != KindVerified {
		t.Fatalf("valid near rejected: %v", err)
	}
	o.Evidence[0].Tolerance = "0.5"
	if _, _, err := Derive(p, v, o); err == nil {
		t.Fatal("unproved relaxed tolerance accepted")
	}
}
