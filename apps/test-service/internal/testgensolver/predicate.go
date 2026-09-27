package testgensolver

import (
	"errors"
	"math/big"
	"strconv"
	"strings"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
)

var errPredicate = errors.New("unsupported predicate")

type exprNode struct {
	op, value   string
	left, right *exprNode
}

type goalPredicate struct {
	expr   *exprNode
	wanted bool
}

func normalizeGoal(branch analysis.Branch, all []analysis.Branch, outcome Outcome) (goalPredicate, error) {
	if !branch.PathVerified || len(branch.PathPredicates) > 16 {
		return goalPredicate{}, errPredicate
	}
	p := branch.Predicate
	switch branch.Kind {
	case analysis.BranchIf, analysis.BranchShortCircuit:
		if outcome != OutcomeTrue && outcome != OutcomeFalse {
			return goalPredicate{}, errPredicate
		}
	case analysis.BranchLoop:
		// A bound proves termination, not the iteration count or reachability
		// of an outcome. Task 7 has no iteration-state proof in its IR.
		return goalPredicate{}, errPredicate
	case analysis.BranchCase:
		if outcome != OutcomeCase || !hasSwitch(all, branch.OwnerSwitchID) {
			return goalPredicate{}, errPredicate
		}
	case analysis.BranchDefault:
		if outcome != OutcomeDefault || !hasSwitch(all, branch.OwnerSwitchID) || p.Operator != "default" || !identifier(p.Left) {
			return goalPredicate{}, errPredicate
		}
		var root *exprNode
		seen := map[string]bool{}
		for _, edge := range all {
			if edge.Kind != analysis.BranchCase || edge.OwnerSwitchID != branch.OwnerSwitchID {
				continue
			}
			if edge.Predicate.Operator != "==" || edge.Predicate.Left != p.Left || seen[edge.Predicate.Right] {
				return goalPredicate{}, errPredicate
			}
			seen[edge.Predicate.Right] = true
			if len(seen) > 64 {
				return goalPredicate{}, errPredicate
			}
			atom, err := predicateFromIR(edge.Predicate)
			if err != nil {
				return goalPredicate{}, err
			}
			negated := &exprNode{op: "!", left: atom}
			if root == nil {
				root = negated
			} else {
				root = &exprNode{op: "&&", left: root, right: negated}
			}
		}
		if root == nil {
			return goalPredicate{}, errPredicate
		}
		return withPath(root, branch)
	default:
		return goalPredicate{}, errPredicate
	}
	if branch.Kind == analysis.BranchCase && p.Operator != "==" {
		return goalPredicate{}, errPredicate
	}
	expr, err := predicateFromIR(p)
	if err != nil {
		return goalPredicate{}, err
	}
	wanted := outcome == OutcomeTrue || outcome == OutcomeCase
	if !wanted {
		expr = &exprNode{op: "!", left: expr}
	}
	return withPath(expr, branch)
}

func withPath(goal *exprNode, branch analysis.Branch) (goalPredicate, error) {
	for _, predicate := range branch.PathPredicates {
		constraint, err := predicateFromIR(predicate)
		if err != nil {
			return goalPredicate{}, errPredicate
		}
		goal = &exprNode{op: "&&", left: goal, right: constraint}
	}
	return goalPredicate{expr: goal, wanted: true}, nil
}

func (g goalPredicate) validReferences(parameters []analysis.Parameter) bool {
	known := map[string]bool{}
	for _, parameter := range parameters {
		known[parameter.Name] = true
		if parameter.Type.Kind == analysis.TypeEnum {
			for _, member := range parameter.Type.EnumValues {
				known[member] = true
			}
		}
	}
	var walk func(*exprNode) bool
	walk = func(n *exprNode) bool {
		if n == nil {
			return true
		}
		if n.op == "atom" && identifier(n.value) && !known[n.value] {
			return false
		}
		return walk(n.left) && walk(n.right)
	}
	return walk(g.expr)
}

// The IR does not prove literal types/ranks or ABI promotions. Accept only
// comparisons whose result is invariant under the known C/C++ conversions.
func (g goalPredicate) validArithmetic(parameters []analysis.Parameter) bool {
	types := make(map[string]analysis.Type, len(parameters))
	for _, parameter := range parameters {
		types[parameter.Name] = parameter.Type
	}
	var walk func(*exprNode) bool
	walk = func(n *exprNode) bool {
		if n == nil {
			return true
		}
		switch n.op {
		case "&&", "||":
			return walk(n.left) && walk(n.right)
		case "!":
			return walk(n.left)
		case "atom":
			if typ, exists := types[n.value]; exists {
				return typ.Kind == analysis.TypeBoolean
			}
			return false
		case "==", "!=", "<", "<=", ">", ">=":
			if n.left == nil || n.right == nil || n.left.op != "atom" || n.right.op != "atom" {
				return false
			}
			a, aParam := types[n.left.value]
			b, bParam := types[n.right.value]
			if aParam && !branchScalar(a.Kind) || bParam && !branchScalar(b.Kind) {
				return false
			}
			if aParam && a.Kind == analysis.TypeFloating || bParam && b.Kind == analysis.TypeFloating {
				return false // target precision and adjacent values are not proven
			}
			if aParam && a.Kind == analysis.TypeEnum || bParam && b.Kind == analysis.TypeEnum {
				if n.op != "==" && n.op != "!=" {
					return false // enum numeric ordinals are absent from this IR
				}
				if aParam && bParam {
					return false // distinct enum type/value identity is unproven
				}
				if aParam {
					return enumMember(a, n.right.value)
				}
				return enumMember(b, n.left.value)
			}
			if aParam && bParam {
				if a.Kind != b.Kind {
					return false
				}
				if a.Kind == analysis.TypeInteger && a.Signed != b.Signed {
					return false
				}
				return a.Kind == analysis.TypeInteger || a.Kind == analysis.TypeBoolean && (n.op == "==" || n.op == "!=")
			}
			if aParam {
				return safeLiteralComparison(a, n.right.value, n.op)
			}
			if bParam {
				return safeLiteralComparison(b, n.left.value, n.op)
			}
			// A constant-only expression has no input-derived reachability.
			return false
		default:
			return false
		}
	}
	return walk(g.expr)
}

func branchScalar(kind analysis.TypeKind) bool {
	return kind == analysis.TypeBoolean || kind == analysis.TypeInteger || kind == analysis.TypeEnum
}

func enumMember(typ analysis.Type, member string) bool {
	for _, value := range typ.EnumValues {
		if value == member {
			return true
		}
	}
	return false
}

func safeLiteralComparison(typ analysis.Type, literal, operator string) bool {
	if typ.Kind != analysis.TypeInteger {
		return false
	}
	n, ok := new(big.Int).SetString(literal, 10)
	if !ok || n.Cmp(big.NewInt(-1<<31)) < 0 || n.Cmp(big.NewInt(1<<31-1)) > 0 {
		return false
	}
	if !typ.Signed && n.Sign() < 0 {
		return false
	}
	return true
}

func hasSwitch(all []analysis.Branch, id string) bool {
	if !digest(id) {
		return false
	}
	for _, branch := range all {
		if branch.Kind == analysis.BranchSwitch && branch.LocationDigest == id {
			return true
		}
	}
	return false
}

func predicateFromIR(p analysis.Predicate) (*exprNode, error) {
	var expression string
	switch p.Operator {
	case "==", "!=", "<", "<=", ">", ">=", "&&", "||":
		if p.Left == "" || p.Right == "" {
			return nil, errPredicate
		}
		expression = "(" + p.Left + ")" + p.Operator + "(" + p.Right + ")"
	case "!":
		if p.Left == "" || p.Right != "" {
			return nil, errPredicate
		}
		expression = "!(" + p.Left + ")"
	default:
		return nil, errPredicate
	}
	return parsePredicateExpression(expression)
}

type parser struct {
	input      string
	pos, nodes int
}

func parsePredicateExpression(input string) (*exprNode, error) {
	if len(input) == 0 || len(input) > 512 {
		return nil, errPredicate
	}
	p := &parser{input: input}
	node, err := p.parseOr(0)
	p.spaces()
	if err != nil || p.pos != len(input) {
		return nil, errPredicate
	}
	return node, nil
}

func (p *parser) spaces() {
	for p.pos < len(p.input) && p.input[p.pos] == ' ' {
		p.pos++
	}
}

func (p *parser) take(s string) bool {
	p.spaces()
	if strings.HasPrefix(p.input[p.pos:], s) {
		p.pos += len(s)
		return true
	}
	return false
}

func (p *parser) binary(next func(int) (*exprNode, error), depth int, operator string) (*exprNode, error) {
	left, err := next(depth + 1)
	if err != nil {
		return nil, err
	}
	for p.take(operator) {
		right, err := next(depth + 1)
		if err != nil {
			return nil, err
		}
		left = &exprNode{op: operator, left: left, right: right}
		p.nodes++
		if p.nodes > 64 {
			return nil, errPredicate
		}
	}
	return left, nil
}

func (p *parser) parseOr(depth int) (*exprNode, error) {
	return p.binary(p.parseAnd, depth, "||")
}
func (p *parser) parseAnd(depth int) (*exprNode, error) {
	return p.binary(p.parseCompare, depth, "&&")
}
func (p *parser) parseCompare(depth int) (*exprNode, error) {
	left, err := p.parseUnary(depth + 1)
	if err != nil {
		return nil, err
	}
	for _, op := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if p.take(op) {
			right, err := p.parseUnary(depth + 1)
			if err != nil {
				return nil, err
			}
			p.nodes++
			if p.nodes > 64 {
				return nil, errPredicate
			}
			return &exprNode{op: op, left: left, right: right}, nil
		}
	}
	return left, nil
}
func (p *parser) parseUnary(depth int) (*exprNode, error) {
	if depth > 24 {
		return nil, errPredicate
	}
	if p.take("!") {
		child, err := p.parseUnary(depth + 1)
		if err != nil {
			return nil, err
		}
		p.nodes++
		return &exprNode{op: "!", left: child}, nil
	}
	if p.take("(") {
		child, err := p.parseOr(depth + 1)
		if err != nil || !p.take(")") {
			return nil, errPredicate
		}
		return child, nil
	}
	return p.parseAtom()
}
func (p *parser) parseAtom() (*exprNode, error) {
	p.spaces()
	start := p.pos
	if p.pos < len(p.input) && p.input[p.pos] == '-' {
		p.pos++
	}
	for p.pos < len(p.input) {
		c := p.input[p.pos]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' {
			p.pos++
		} else {
			break
		}
	}
	value := p.input[start:p.pos]
	if value == "" || value == "-" || len(value) > 128 {
		return nil, errPredicate
	}
	if !identifier(value) {
		if _, ok := new(big.Int).SetString(value, 10); !ok {
			if _, err := strconv.ParseFloat(value, 64); err != nil || strings.ContainsAny(value, "NaInfnf") {
				return nil, errPredicate
			}
		}
	}
	p.nodes++
	if p.nodes > 64 {
		return nil, errPredicate
	}
	return &exprNode{op: "atom", value: value}, nil
}

func (g goalPredicate) constants(name string) []string {
	var result []string
	var walk func(*exprNode)
	walk = func(n *exprNode) {
		if n == nil {
			return
		}
		if n.left != nil && n.right != nil && n.left.op == "atom" && n.right.op == "atom" {
			other := ""
			if n.left.value == name {
				other = n.right.value
			} else if n.right.value == name {
				other = n.left.value
			}
			if _, ok := new(big.Int).SetString(other, 10); ok {
				result = append(result, other)
			}
		}
		walk(n.left)
		walk(n.right)
	}
	walk(g.expr)
	return result
}

func (g goalPredicate) accept(inputs []Input) bool {
	if g.expr == nil {
		return true
	}
	values := make(map[string]Value, len(inputs))
	for _, in := range inputs {
		values[in.Name] = in.Value
	}
	got, err := evaluateBool(g.expr, values)
	return err == nil && got == g.wanted
}

func evaluateBool(n *exprNode, inputs map[string]Value) (bool, error) {
	if n == nil {
		return false, errPredicate
	}
	switch n.op {
	case "!":
		v, err := evaluateBool(n.left, inputs)
		return !v, err
	case "&&", "||":
		left, e1 := evaluateBool(n.left, inputs)
		right, e2 := evaluateBool(n.right, inputs)
		if e1 != nil || e2 != nil {
			return false, errPredicate
		}
		if n.op == "&&" {
			return left && right, nil
		}
		return left || right, nil
	case "==", "!=", "<", "<=", ">", ">=":
		left, e1 := scalar(n.left, inputs)
		right, e2 := scalar(n.right, inputs)
		if e1 != nil || e2 != nil {
			return false, errPredicate
		}
		cmp, err := compare(left, right)
		if err != nil {
			return false, err
		}
		switch n.op {
		case "==":
			return cmp == 0, nil
		case "!=":
			return cmp != 0, nil
		case "<":
			return cmp < 0, nil
		case "<=":
			return cmp <= 0, nil
		case ">":
			return cmp > 0, nil
		default:
			return cmp >= 0, nil
		}
	case "atom":
		v, ok := inputs[n.value]
		if ok && v.Kind == analysis.TypeBoolean {
			return v.Boolean, nil
		}
	}
	return false, errPredicate
}

type scalarValue struct {
	kind analysis.TypeKind
	text string
}

func scalar(n *exprNode, inputs map[string]Value) (scalarValue, error) {
	if n == nil || n.op != "atom" {
		return scalarValue{}, errPredicate
	}
	if v, ok := inputs[n.value]; ok {
		switch v.Kind {
		case analysis.TypeInteger:
			return scalarValue{v.Kind, v.Integer}, nil
		case analysis.TypeFloating:
			return scalarValue{v.Kind, v.Float}, nil
		case analysis.TypeEnum:
			return scalarValue{v.Kind, v.Enum}, nil
		case analysis.TypeBoolean:
			if v.Boolean {
				return scalarValue{v.Kind, "true"}, nil
			}
			return scalarValue{v.Kind, "false"}, nil
		}
		return scalarValue{}, errPredicate
	}
	if _, ok := new(big.Int).SetString(n.value, 10); ok {
		return scalarValue{analysis.TypeInteger, n.value}, nil
	}
	if _, err := strconv.ParseFloat(n.value, 64); err == nil && strings.Contains(n.value, ".") {
		return scalarValue{analysis.TypeFloating, n.value}, nil
	}
	if identifier(n.value) {
		return scalarValue{analysis.TypeEnum, n.value}, nil
	}
	return scalarValue{}, errPredicate
}

func compare(a, b scalarValue) (int, error) {
	if a.kind == analysis.TypeInteger && b.kind == analysis.TypeInteger {
		x, okX := new(big.Int).SetString(a.text, 10)
		y, okY := new(big.Int).SetString(b.text, 10)
		if okX && okY {
			return x.Cmp(y), nil
		}
	}
	if a.kind == analysis.TypeFloating && (b.kind == analysis.TypeFloating || b.kind == analysis.TypeInteger) || b.kind == analysis.TypeFloating && a.kind == analysis.TypeInteger {
		x, ex := strconv.ParseFloat(a.text, 64)
		y, ey := strconv.ParseFloat(b.text, 64)
		if ex == nil && ey == nil && x == x && y == y {
			if x < y {
				return -1, nil
			}
			if x > y {
				return 1, nil
			}
			return 0, nil
		}
	}
	if a.kind == b.kind && (a.kind == analysis.TypeBoolean || a.kind == analysis.TypeEnum) {
		return strings.Compare(a.text, b.text), nil
	}
	return 0, errPredicate
}
