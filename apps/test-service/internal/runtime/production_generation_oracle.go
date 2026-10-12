package runtime

import (
	"context"
	"errors"
	"math/big"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	assertion "unit-test-ide.local/test-service/internal/testgenassert"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

var errProductionOracleUnavailable = errors.New("production static oracle is unavailable")

type productionStaticOracle struct{}

type productionOracleScalar struct {
	kind    analysis.TypeKind
	integer *big.Int
	boolean bool
	enum    string
}

func productionOracleInput(value solver.Value) (productionOracleScalar, bool) {
	switch value.Kind {
	case analysis.TypeInteger:
		integer, ok := new(big.Int).SetString(value.Integer, 10)
		return productionOracleScalar{kind: value.Kind, integer: integer}, ok && integer.String() == value.Integer
	case analysis.TypeBoolean:
		return productionOracleScalar{kind: value.Kind, boolean: value.Boolean}, true
	case analysis.TypeEnum:
		return productionOracleScalar{kind: value.Kind, enum: value.Enum}, value.Enum != ""
	default:
		return productionOracleScalar{}, false
	}
}

func productionOracleTruth(value productionOracleScalar) (bool, bool) {
	switch value.kind {
	case analysis.TypeBoolean:
		return value.boolean, true
	case analysis.TypeInteger:
		return value.integer.Sign() != 0, true
	default:
		return false, false
	}
}

func productionOracleCompare(operator string, left, right productionOracleScalar) (bool, bool) {
	if left.kind != right.kind {
		return false, false
	}
	comparison := 0
	switch left.kind {
	case analysis.TypeInteger:
		comparison = left.integer.Cmp(right.integer)
	case analysis.TypeBoolean:
		if left.boolean != right.boolean {
			if left.boolean {
				comparison = 1
			} else {
				comparison = -1
			}
		}
	case analysis.TypeEnum:
		if operator != "==" && operator != "!=" {
			return false, false
		}
		if left.enum != right.enum {
			comparison = -1
		}
	default:
		return false, false
	}
	switch operator {
	case "<":
		return comparison < 0, true
	case "<=":
		return comparison <= 0, true
	case ">":
		return comparison > 0, true
	case ">=":
		return comparison >= 0, true
	case "==":
		return comparison == 0, true
	case "!=":
		return comparison != 0, true
	default:
		return false, false
	}
}

func productionOracleEvaluate(expression analysis.ClosedExpression, inputs map[string]productionOracleScalar, depth int) (productionOracleScalar, bool) {
	if depth > 32 {
		return productionOracleScalar{}, false
	}
	switch expression.Kind {
	case analysis.ExpressionParameter:
		value, ok := inputs[expression.Name]
		return value, ok && expression.Value == "" && expression.Operator == "" && expression.Left == nil && expression.Right == nil
	case analysis.ExpressionInteger:
		integer, ok := new(big.Int).SetString(expression.Value, 10)
		return productionOracleScalar{kind: analysis.TypeInteger, integer: integer}, ok && integer.String() == expression.Value && expression.Name == "" && expression.Operator == "" && expression.Left == nil && expression.Right == nil
	case analysis.ExpressionBoolean:
		if expression.Value != "true" && expression.Value != "false" || expression.Name != "" || expression.Operator != "" || expression.Left != nil || expression.Right != nil {
			return productionOracleScalar{}, false
		}
		return productionOracleScalar{kind: analysis.TypeBoolean, boolean: expression.Value == "true"}, true
	case analysis.ExpressionEnum:
		if expression.Name == "" || expression.Value != "" || expression.Operator != "" || expression.Left != nil || expression.Right != nil {
			return productionOracleScalar{}, false
		}
		return productionOracleScalar{kind: analysis.TypeEnum, enum: expression.Name}, true
	case analysis.ExpressionUnary:
		if expression.Left == nil || expression.Right != nil || expression.Name != "" || expression.Value != "" {
			return productionOracleScalar{}, false
		}
		left, ok := productionOracleEvaluate(*expression.Left, inputs, depth+1)
		if !ok {
			return productionOracleScalar{}, false
		}
		switch expression.Operator {
		case "!":
			truth, ok := productionOracleTruth(left)
			return productionOracleScalar{kind: analysis.TypeBoolean, boolean: !truth}, ok
		case "+":
			return left, left.kind == analysis.TypeInteger
		case "-":
			if left.kind != analysis.TypeInteger {
				return productionOracleScalar{}, false
			}
			return productionOracleScalar{kind: analysis.TypeInteger, integer: new(big.Int).Neg(left.integer)}, true
		default:
			return productionOracleScalar{}, false
		}
	case analysis.ExpressionBinary:
		if expression.Left == nil || expression.Right == nil || expression.Name != "" || expression.Value != "" {
			return productionOracleScalar{}, false
		}
		left, leftOK := productionOracleEvaluate(*expression.Left, inputs, depth+1)
		right, rightOK := productionOracleEvaluate(*expression.Right, inputs, depth+1)
		if !leftOK || !rightOK {
			return productionOracleScalar{}, false
		}
		if expression.Operator == "&&" || expression.Operator == "||" {
			leftTruth, leftOK := productionOracleTruth(left)
			rightTruth, rightOK := productionOracleTruth(right)
			if !leftOK || !rightOK {
				return productionOracleScalar{}, false
			}
			if expression.Operator == "&&" {
				return productionOracleScalar{kind: analysis.TypeBoolean, boolean: leftTruth && rightTruth}, true
			}
			return productionOracleScalar{kind: analysis.TypeBoolean, boolean: leftTruth || rightTruth}, true
		}
		value, ok := productionOracleCompare(expression.Operator, left, right)
		return productionOracleScalar{kind: analysis.TypeBoolean, boolean: value}, ok
	default:
		return productionOracleScalar{}, false
	}
}

func productionOracleExpected(value productionOracleScalar, target analysis.Type) (solver.Value, bool) {
	switch target.Kind {
	case analysis.TypeInteger:
		integer := value.integer
		if value.kind == analysis.TypeBoolean {
			integer = big.NewInt(0)
			if value.boolean {
				integer.SetInt64(1)
			}
		}
		if integer == nil || target.BitWidth != 8 && target.BitWidth != 16 && target.BitWidth != 32 && target.BitWidth != 64 {
			return solver.Value{}, false
		}
		minimum := big.NewInt(0)
		maximum := new(big.Int).Lsh(big.NewInt(1), uint(target.BitWidth))
		if target.Signed {
			minimum.Neg(new(big.Int).Rsh(new(big.Int).Set(maximum), 1))
			maximum.Rsh(maximum, 1).Sub(maximum, big.NewInt(1))
		} else {
			maximum.Sub(maximum, big.NewInt(1))
		}
		if integer.Cmp(minimum) < 0 || integer.Cmp(maximum) > 0 {
			return solver.Value{}, false
		}
		return solver.Value{Kind: analysis.TypeInteger, Integer: integer.String()}, true
	case analysis.TypeBoolean:
		truth, ok := productionOracleTruth(value)
		return solver.Value{Kind: analysis.TypeBoolean, Boolean: truth}, ok
	case analysis.TypeEnum:
		if value.kind != analysis.TypeEnum {
			return solver.Value{}, false
		}
		for _, member := range target.EnumValues {
			if member == value.enum {
				return solver.Value{Kind: analysis.TypeEnum, Enum: value.enum}, true
			}
		}
	}
	return solver.Value{}, false
}

func (productionStaticOracle) Bind(ctx context.Context, program analysis.Program, symbolID string, vectors []solver.InputVector) (analysis.Program, []assertion.Observation, error) {
	if ctx == nil || program.Version != analysis.IRVersion || !validProductionDigest(program.Digest) || !validProductionDigest(symbolID) || len(vectors) == 0 || len(vectors) > 1000 {
		return analysis.Program{}, nil, errProductionOracleUnavailable
	}
	functionIndex := -1
	for index := range program.Functions {
		if program.Functions[index].SymbolID == symbolID {
			if functionIndex != -1 {
				return analysis.Program{}, nil, errProductionOracleUnavailable
			}
			functionIndex = index
		}
	}
	if functionIndex == -1 || program.Functions[functionIndex].Decision.Kind != analysis.DecisionSupported || len(program.Functions[functionIndex].ReturnRules) == 0 || len(program.Functions[functionIndex].ReturnRules) > 256 {
		return analysis.Program{}, nil, errProductionOracleUnavailable
	}
	program.Functions = append([]analysis.Function(nil), program.Functions...)
	function := &program.Functions[functionIndex]
	function.OracleProofs = append([]analysis.OracleProof(nil), function.OracleProofs...)
	observations := make([]assertion.Observation, 0, len(vectors))
	seen := map[string]bool{}
	for _, vector := range vectors {
		if err := ctx.Err(); err != nil {
			return analysis.Program{}, nil, err
		}
		if !validProductionDigest(vector.ID) || seen[vector.ID] || len(vector.Inputs) != len(function.Parameters) {
			return analysis.Program{}, nil, errProductionOracleUnavailable
		}
		seen[vector.ID] = true
		inputs := make(map[string]productionOracleScalar, len(vector.Inputs))
		for index, parameter := range function.Parameters {
			input := vector.Inputs[index]
			value, ok := productionOracleInput(input.Value)
			if !ok || input.Name != parameter.Name || value.kind != parameter.Type.Kind || inputs[input.Name].kind != "" {
				return analysis.Program{}, nil, errProductionOracleUnavailable
			}
			inputs[input.Name] = value
		}
		matched := 0
		var expected solver.Value
		for _, rule := range function.ReturnRules {
			applies := true
			for _, condition := range rule.Conditions {
				value, ok := productionOracleEvaluate(condition, inputs, 0)
				truth, truthOK := productionOracleTruth(value)
				if !ok || !truthOK {
					return analysis.Program{}, nil, errProductionOracleUnavailable
				}
				if !truth {
					applies = false
					break
				}
			}
			if !applies {
				continue
			}
			value, ok := productionOracleEvaluate(rule.Result, inputs, 0)
			if !ok {
				return analysis.Program{}, nil, errProductionOracleUnavailable
			}
			expected, ok = productionOracleExpected(value, function.ReturnType)
			if !ok {
				return analysis.Program{}, nil, errProductionOracleUnavailable
			}
			matched++
		}
		if matched != 1 {
			return analysis.Program{}, nil, errProductionOracleUnavailable
		}
		evidence := assertion.Evidence{
			Kind: assertion.EvidenceReturnContract, Target: assertion.TargetReturn,
			TargetDigest: symbolID, SourceDigest: function.Excerpt.Digest, Expected: expected,
			Rule: assertion.RuleEqual, Stability: assertion.StabilityDeterministic,
		}
		function.OracleProofs = append(function.OracleProofs, analysis.OracleProof{
			Kind: string(evidence.Kind), CandidateID: vector.ID, InputDigest: assertion.InputDigest(vector.Inputs),
			TargetDigest: symbolID, SourceDigest: evidence.SourceDigest, ExpectedDigest: assertion.ValueDigest(expected), Rule: string(evidence.Rule),
		})
		observations = append(observations, assertion.Observation{SymbolID: symbolID, CandidateID: vector.ID, Evidence: []assertion.Evidence{evidence}})
	}
	return program, observations, nil
}

var _ productionOracle = productionStaticOracle{}
