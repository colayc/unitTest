package testgenanalysis

import "math/big"

const maxReturnRules = 256

type returnPath []ClosedExpression

func cloneReturnPath(value returnPath) returnPath {
	return append(returnPath(nil), value...)
}

func unwrapClosedExpression(node *astNode) *astNode {
	for node != nil {
		switch node.Kind {
		case "ImplicitCastExpr", "ConstantExpr", "ParenExpr", "CStyleCastExpr", "CXXStaticCastExpr":
			if len(node.Inner) != 1 {
				return nil
			}
			node = node.Inner[0]
		default:
			return node
		}
	}
	return nil
}

func extractClosedExpression(node *astNode, parameters map[string]string, depth int) (ClosedExpression, bool) {
	if depth > 32 {
		return ClosedExpression{}, false
	}
	node = unwrapClosedExpression(node)
	if node == nil {
		return ClosedExpression{}, false
	}
	switch node.Kind {
	case "IntegerLiteral", "CharacterLiteral":
		value, ok := new(big.Int).SetString(node.Value, 10)
		if !ok || value.String() != node.Value {
			return ClosedExpression{}, false
		}
		return ClosedExpression{Kind: ExpressionInteger, Value: node.Value}, true
	case "CXXBoolLiteralExpr":
		if node.Value != "true" && node.Value != "false" {
			return ClosedExpression{}, false
		}
		return ClosedExpression{Kind: ExpressionBoolean, Value: node.Value}, true
	case "DeclRefExpr":
		if node.ReferencedDecl == nil {
			return ClosedExpression{}, false
		}
		if node.ReferencedDecl.Kind == "ParmVarDecl" && parameters[node.ReferencedDecl.ID] == node.ReferencedDecl.Name && safeIdentifier(node.ReferencedDecl.Name) {
			return ClosedExpression{Kind: ExpressionParameter, Name: node.ReferencedDecl.Name}, true
		}
		if node.ReferencedDecl.Kind == "EnumConstantDecl" && safeIdentifier(node.ReferencedDecl.Name) {
			return ClosedExpression{Kind: ExpressionEnum, Name: node.ReferencedDecl.Name}, true
		}
		return ClosedExpression{}, false
	case "UnaryOperator":
		if node.Opcode != "!" && node.Opcode != "+" && node.Opcode != "-" || len(node.Inner) != 1 {
			return ClosedExpression{}, false
		}
		left, ok := extractClosedExpression(node.Inner[0], parameters, depth+1)
		if !ok {
			return ClosedExpression{}, false
		}
		return ClosedExpression{Kind: ExpressionUnary, Operator: node.Opcode, Left: &left}, true
	case "BinaryOperator":
		switch node.Opcode {
		case "<", "<=", ">", ">=", "==", "!=", "&&", "||":
		default:
			return ClosedExpression{}, false
		}
		if len(node.Inner) != 2 {
			return ClosedExpression{}, false
		}
		left, leftOK := extractClosedExpression(node.Inner[0], parameters, depth+1)
		right, rightOK := extractClosedExpression(node.Inner[1], parameters, depth+1)
		if !leftOK || !rightOK {
			return ClosedExpression{}, false
		}
		return ClosedExpression{Kind: ExpressionBinary, Operator: node.Opcode, Left: &left, Right: &right}, true
	default:
		return ClosedExpression{}, false
	}
}

func negatedClosedExpression(value ClosedExpression) ClosedExpression {
	return ClosedExpression{Kind: ExpressionUnary, Operator: "!", Left: &value}
}

func extractClosedReturnStatement(node *astNode, incoming []returnPath, parameters map[string]string, depth int) ([]ReturnRule, []returnPath, bool) {
	if node == nil || depth > 32 || len(incoming) > maxReturnRules {
		return nil, nil, false
	}
	switch node.Kind {
	case "CompoundStmt":
		return extractClosedReturnSequence(node.Inner, incoming, parameters, depth+1)
	case "NullStmt":
		return nil, incoming, true
	case "ReturnStmt":
		if len(node.Inner) != 1 {
			return nil, nil, false
		}
		result, ok := extractClosedExpression(node.Inner[0], parameters, 0)
		if !ok {
			return nil, nil, false
		}
		rules := make([]ReturnRule, 0, len(incoming))
		for _, path := range incoming {
			rules = append(rules, ReturnRule{Conditions: cloneReturnPath(path), Result: result})
		}
		return rules, nil, true
	case "IfStmt":
		if len(node.Inner) < 2 || len(node.Inner) > 3 {
			return nil, nil, false
		}
		condition, ok := extractClosedExpression(node.Inner[0], parameters, 0)
		if !ok {
			return nil, nil, false
		}
		positive := make([]returnPath, 0, len(incoming))
		negative := make([]returnPath, 0, len(incoming))
		for _, path := range incoming {
			positive = append(positive, append(cloneReturnPath(path), condition))
			negative = append(negative, append(cloneReturnPath(path), negatedClosedExpression(condition)))
		}
		thenRules, thenNext, ok := extractClosedReturnStatement(node.Inner[1], positive, parameters, depth+1)
		if !ok {
			return nil, nil, false
		}
		elseRules, elseNext := []ReturnRule(nil), negative
		if len(node.Inner) == 3 {
			elseRules, elseNext, ok = extractClosedReturnStatement(node.Inner[2], negative, parameters, depth+1)
			if !ok {
				return nil, nil, false
			}
		}
		return append(thenRules, elseRules...), append(thenNext, elseNext...), true
	default:
		return nil, nil, false
	}
}

func extractClosedReturnSequence(statements []*astNode, incoming []returnPath, parameters map[string]string, depth int) ([]ReturnRule, []returnPath, bool) {
	rules := []ReturnRule{}
	remaining := incoming
	for _, statement := range statements {
		if len(remaining) == 0 {
			return nil, nil, false
		}
		produced, next, ok := extractClosedReturnStatement(statement, remaining, parameters, depth+1)
		if !ok || len(rules)+len(produced) > maxReturnRules || len(next) > maxReturnRules {
			return nil, nil, false
		}
		rules = append(rules, produced...)
		remaining = next
	}
	return rules, remaining, true
}

func extractClosedReturnRules(body *astNode, parameters map[string]string) []ReturnRule {
	rules, remaining, ok := extractClosedReturnStatement(body, []returnPath{{}}, parameters, 0)
	if !ok || len(rules) == 0 || len(rules) > maxReturnRules || len(remaining) != 0 {
		return nil
	}
	return rules
}
