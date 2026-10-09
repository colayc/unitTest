package testgenassert

import (
	"math"
	"math/big"
	"strconv"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

// ValidateTypedValue enforces closed fields in addition to the existing strict
// type rules. It does not widen Derive's supported type set.
func ValidateTypedValue(v solver.Value, typ analysis.Type) bool {
	return ValidateValueShape(v) && validValue(v, typ, 0)
}
func ValidateTypedRule(v solver.Value, typ analysis.Type, rule ComparisonRule, tolerance string) bool {
	return ValidateTypedValue(v, typ) && validRule(Evidence{Expected: v, Rule: rule, Tolerance: tolerance}, typ)
}

// ValidateValueShape checks finite, closed data only. In particular, pointer
// shape does not establish ownership or dereference/execute authority, and
// canonical integer syntax does not establish a parameter's numeric range.
func ValidateValueShape(v solver.Value) bool {
	budget := 128 * 1024
	return valueShape(v, 0, &budget)
}

func valueShape(v solver.Value, depth int, budget *int) bool {
	*budget -= 64 + len(v.Integer) + len(v.Float) + len(v.Enum) + len(v.String)
	if depth > 4 || *budget < 0 ||
		v.Kind != analysis.TypeBoolean && v.Boolean ||
		v.Kind != analysis.TypeInteger && v.Integer != "" ||
		v.Kind != analysis.TypeFloating && v.Float != "" ||
		v.Kind != analysis.TypeEnum && v.Enum != "" ||
		v.Kind != analysis.TypeString && v.String != "" ||
		v.Kind != analysis.TypePointer && (v.Null || v.Pointee != nil) ||
		v.Kind != analysis.TypeArray && len(v.Elements) != 0 ||
		v.Kind != analysis.TypeRecord && len(v.Fields) != 0 {
		return false
	}
	switch v.Kind {
	case analysis.TypeBoolean:
		return true
	case analysis.TypeInteger:
		if len(v.Integer) > 128 {
			return false
		}
		n, ok := new(big.Int).SetString(v.Integer, 10)
		return ok && n.String() == v.Integer
	case analysis.TypeFloating:
		if len(v.Float) > 128 {
			return false
		}
		n, err := strconv.ParseFloat(v.Float, 64)
		return err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	case analysis.TypeEnum:
		return identifier(v.Enum)
	case analysis.TypeString:
		return len(v.String) <= 256 && !hostPath(v.String)
	case analysis.TypePointer:
		if v.Null {
			return v.Pointee == nil
		}
		return v.Pointee != nil && valueShape(*v.Pointee, depth+1, budget)
	case analysis.TypeArray:
		if len(v.Elements) < 1 || len(v.Elements) > 16 {
			return false
		}
		for _, e := range v.Elements {
			if !valueShape(e, depth+1, budget) {
				return false
			}
		}
		return true
	case analysis.TypeRecord:
		if len(v.Fields) < 1 || len(v.Fields) > 16 {
			return false
		}
		names := map[string]bool{}
		for _, f := range v.Fields {
			*budget -= len(f.Name)
			if !identifier(f.Name) || names[f.Name] || !valueShape(f.Value, depth+1, budget) {
				return false
			}
			names[f.Name] = true
		}
		return true
	default:
		return false
	}
}
