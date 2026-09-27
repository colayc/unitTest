// Package testgenassert derives assertions only from closed, digest-bound
// oracle evidence. Observation-only values remain characterization candidates.
package testgenassert

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

type CandidateKind string

const (
	KindVerified         CandidateKind = "verified"
	KindCharacterization CandidateKind = "characterization"
)

type EvidenceKind string

const (
	EvidenceReturnContract EvidenceKind = "return-contract"
	EvidenceErrorContract  EvidenceKind = "error-contract"
	EvidenceInvariant      EvidenceKind = "invariant"
	EvidenceObservedOutput EvidenceKind = "observed-output"
)

type TargetKind string

const (
	TargetReturn TargetKind = "return"
	TargetOutput TargetKind = "output"
	TargetInput  TargetKind = "input"
)

type ComparisonRule string

const (
	RuleEqual ComparisonRule = "equal"
	RuleNear  ComparisonRule = "near"
)

type StabilityKind string

const (
	StabilityDeterministic StabilityKind = "deterministic"
	StabilityTimestamp     StabilityKind = "timestamp"
	StabilityRandom        StabilityKind = "random"
)

type Evidence struct {
	Kind            EvidenceKind
	Target          TargetKind
	TargetName      string
	TargetDigest    string
	SourceDigest    string
	Expected        solver.Value
	Rule            ComparisonRule
	Tolerance       string
	Stability       StabilityKind
	StabilityDigest string
	RepeatCount     int
}
type Observation struct {
	SymbolID    string
	CandidateID string
	Evidence    []Evidence
}
type Assertion struct {
	Provenance      EvidenceKind
	Target          TargetKind
	TargetName      string
	TargetDigest    string
	EvidenceDigest  string
	Expected        solver.Value
	Rule            ComparisonRule
	Tolerance       string
	Stability       StabilityKind
	StabilityDigest string
}

var ErrInvalidEvidence = errors.New("invalid or unproven assertion evidence")

func ValueDigest(v solver.Value) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// InputDigest binds the complete ordered, typed vector independently of the
// solver's ID, which also incorporates gap and compile metadata unavailable here.
func InputDigest(inputs []solver.Input) string {
	b, _ := json.Marshal(inputs)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func digest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' && r < 'a' || r > 'f' {
			return false
		}
	}
	return true
}
func identifier(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func Derive(p analysis.Program, vector solver.InputVector, o Observation) ([]Assertion, CandidateKind, error) {
	if p.Version != analysis.IRVersion || !digest(p.Digest) || !digest(vector.ID) || o.CandidateID != vector.ID || !digest(o.SymbolID) || len(o.Evidence) < 1 || len(o.Evidence) > 128 {
		return nil, "", ErrInvalidEvidence
	}
	var fn *analysis.Function
	for i := range p.Functions {
		if p.Functions[i].SymbolID == o.SymbolID {
			if fn != nil {
				return nil, "", ErrInvalidEvidence
			}
			fn = &p.Functions[i]
		}
	}
	if fn == nil || fn.Decision.Kind != analysis.DecisionSupported || fn.Decision.Reason != analysis.ReasonNone || !digest(fn.Excerpt.Digest) || len(vector.Inputs) != len(fn.Parameters) {
		return nil, "", ErrInvalidEvidence
	}
	inputs := map[string]solver.Value{}
	for i, parameter := range fn.Parameters {
		input := vector.Inputs[i]
		if !identifier(parameter.Name) || parameter.Name != input.Name || !validValue(input.Value, parameter.Type, 0) {
			return nil, "", ErrInvalidEvidence
		}
		inputs[input.Name] = input.Value
	}
	assertions := make([]Assertion, 0, len(o.Evidence))
	kind := KindVerified
	for _, e := range o.Evidence {
		if !digest(e.TargetDigest) || e.SourceDigest != fn.Excerpt.Digest || e.Stability != StabilityDeterministic || e.Target == TargetInput {
			return nil, "", ErrInvalidEvidence
		}
		var typ analysis.Type
		switch e.Target {
		case TargetReturn:
			if e.TargetName != "" {
				return nil, "", ErrInvalidEvidence
			}
			typ = fn.ReturnType
		case TargetOutput:
			if !identifier(e.TargetName) {
				return nil, "", ErrInvalidEvidence
			}
			found := false
			for _, parameter := range fn.Parameters {
				if parameter.Name == e.TargetName {
					typ = parameter.Type
					found = true
					break
				}
			}
			if !found {
				return nil, "", ErrInvalidEvidence
			}
			if input, ok := inputs[e.TargetName]; ok && ValueDigest(input) == ValueDigest(e.Expected) {
				return nil, "", ErrInvalidEvidence
			}
		default:
			return nil, "", ErrInvalidEvidence
		}
		if !validValue(e.Expected, typ, 0) || !validRule(e, typ) {
			return nil, "", ErrInvalidEvidence
		}
		switch e.Kind {
		case EvidenceReturnContract, EvidenceErrorContract, EvidenceInvariant:
			proven := false
			for _, proof := range fn.OracleProofs {
				if proof.Kind == string(e.Kind) && proof.CandidateID == vector.ID && proof.InputDigest == InputDigest(vector.Inputs) && proof.TargetDigest == e.TargetDigest && proof.SourceDigest == e.SourceDigest && proof.ExpectedDigest == ValueDigest(e.Expected) && proof.Rule == string(e.Rule) && proof.Tolerance == e.Tolerance {
					proven = true
					break
				}
			}
			if !proven {
				return nil, "", ErrInvalidEvidence
			}
		case EvidenceObservedOutput:
			// A caller-supplied count/digest does not attest repeated execution.
			// Task 10 must provide a product-verified typed observation receipt
			// before this provenance can be classified or rendered.
			return nil, "", ErrInvalidEvidence
		default:
			return nil, "", ErrInvalidEvidence
		}
		b, _ := json.Marshal(struct {
			Program  string
			Vector   string
			Symbol   string
			Evidence Evidence
		}{p.Digest, vector.ID, o.SymbolID, e})
		sum := sha256.Sum256(b)
		stabilityDigest := e.StabilityDigest
		if stabilityDigest == "" {
			stabilityDigest = e.SourceDigest
		}
		assertions = append(assertions, Assertion{Provenance: e.Kind, Target: e.Target, TargetName: e.TargetName, TargetDigest: e.TargetDigest, EvidenceDigest: hex.EncodeToString(sum[:]), Expected: e.Expected, Rule: e.Rule, Tolerance: e.Tolerance, Stability: e.Stability, StabilityDigest: stabilityDigest})
	}
	return assertions, kind, nil
}

func validRule(e Evidence, t analysis.Type) bool {
	if (t.Kind == analysis.TypeArray || t.Kind == analysis.TypeRecord) && containsFloatingLeaf(t, 0) {
		return false
	}
	if t.Kind == analysis.TypeFloating {
		if e.Rule != RuleNear {
			return false
		}
		n, err := strconv.ParseFloat(e.Tolerance, 64)
		return err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) && n > 0 && n <= 1
	}
	return e.Rule == RuleEqual && e.Tolerance == ""
}
func containsFloatingLeaf(t analysis.Type, depth int) bool {
	if depth > 4 {
		return true
	}
	if t.Kind == analysis.TypeFloating {
		return true
	}
	if t.Kind == analysis.TypeArray && t.Element != nil {
		return containsFloatingLeaf(*t.Element, depth+1)
	}
	if t.Kind == analysis.TypeRecord {
		for _, f := range t.Fields {
			if containsFloatingLeaf(f.Type, depth+1) {
				return true
			}
		}
	}
	return false
}
func validValue(v solver.Value, t analysis.Type, depth int) bool {
	if depth > 4 || v.Kind != t.Kind {
		return false
	}
	switch t.Kind {
	case analysis.TypeBoolean:
		return true
	case analysis.TypeInteger:
		if t.BitWidth != 8 && t.BitWidth != 16 && t.BitWidth != 32 && t.BitWidth != 64 {
			return false
		}
		n, ok := new(big.Int).SetString(v.Integer, 10)
		if !ok || n.String() != v.Integer {
			return false
		}
		min := big.NewInt(0)
		max := new(big.Int).Lsh(big.NewInt(1), uint(t.BitWidth))
		if t.Signed {
			min.Neg(new(big.Int).Rsh(max, 1))
			max.Sub(new(big.Int).Rsh(max, 1), big.NewInt(1))
		} else {
			max.Sub(max, big.NewInt(1))
		}
		return n.Cmp(min) >= 0 && n.Cmp(max) <= 0
	case analysis.TypeFloating:
		if t.BitWidth != 32 && t.BitWidth != 64 {
			return false
		}
		n, err := strconv.ParseFloat(v.Float, t.BitWidth)
		return err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	case analysis.TypeEnum:
		for _, member := range t.EnumValues {
			if v.Enum == member && identifier(member) {
				return true
			}
		}
		return false
	case analysis.TypeString:
		return t.MaxLength > 0 && t.MaxLength <= 256 && len(v.String) <= t.MaxLength && !hostPath(v.String)
	case analysis.TypeArray:
		if t.Element == nil || t.Bound < 1 || t.Bound > 16 || len(v.Elements) != t.Bound {
			return false
		}
		for _, element := range v.Elements {
			if !validValue(element, *t.Element, depth+1) {
				return false
			}
		}
		return true
	case analysis.TypeRecord:
		if !t.Proven || len(t.Fields) < 1 || len(t.Fields) > 16 || len(v.Fields) != len(t.Fields) {
			return false
		}
		seen := map[string]bool{}
		for _, field := range v.Fields {
			if seen[field.Name] {
				return false
			}
			seen[field.Name] = true
			found := false
			for _, decl := range t.Fields {
				if decl.Name == field.Name && validValue(field.Value, decl.Type, depth+1) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	default:
		return false
	}
}
func hostPath(s string) bool {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "\\") || strings.Contains(s, "\\\\") || len(s) >= 2 && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) && s[1] == ':' {
		return true
	}
	return false
}
