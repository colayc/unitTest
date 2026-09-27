package testgenanalysis

import "testing"

func TestSafetyClassifierFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		fn     Function
		want   DecisionKind
		reason ReasonCode
	}{
		{"pure", Function{Name: "choose", ReturnType: Type{Kind: TypeInteger}, Parameters: []Parameter{{Name: "x", Type: Type{Kind: TypeInteger}}}, BodyKinds: []string{"CompoundStmt", "IfStmt", "BinaryOperator", "ReturnStmt", "IntegerLiteral", "DeclRefExpr", "ImplicitCastExpr"}}, DecisionSupported, ReasonNone},
		{"network", Function{Name: "dial", ReturnType: Type{Kind: TypeInteger}, Calls: []string{"socket"}}, DecisionUnsupported, ReasonExternalCall},
		{"random", Function{Name: "roll", ReturnType: Type{Kind: TypeInteger}, Calls: []string{"rand"}}, DecisionUnsupported, ReasonExternalCall},
		{"process", Function{Name: "launch", ReturnType: Type{Kind: TypeInteger}, Calls: []string{"system"}}, DecisionUnsupported, ReasonExternalCall},
		{"unknown call", Function{Name: "f", ReturnType: Type{Kind: TypeInteger}, Calls: []string{"custom"}}, DecisionUnsupported, ReasonUnknownCall},
		{"inline assembly", Function{Name: "f", ReturnType: Type{Kind: TypeInteger}, BodyKinds: []string{"CompoundStmt", "GCCAsmStmt"}}, DecisionUnsupported, ReasonUnsupportedSyntax},
		{"volatile", Function{Name: "f", ReturnType: Type{Kind: TypeUnknown, Spelling: "volatile int"}}, DecisionUnsupported, ReasonUnsupportedType},
		{"function pointer", Function{Name: "f", Parameters: []Parameter{{Name: "cb", Type: Type{Kind: TypeUnknown, Spelling: "int (*)(int)"}}}}, DecisionUnsupported, ReasonUnsupportedType},
		{"writable pointer argument", Function{Name: "f", ReturnType: Type{Kind: TypeVoid}, Parameters: []Parameter{{Name: "p", Type: Type{Kind: TypePointer, Spelling: "char *"}}}}, DecisionUnsupported, ReasonUnsupportedType},
		{"template", Function{Name: "f", ReturnType: Type{Kind: TypeInteger}, BodyKinds: []string{"FunctionTemplateDecl"}}, DecisionUnsupported, ReasonUnsupportedSyntax},
		{"unbounded loop", Function{Name: "f", ReturnType: Type{Kind: TypeInteger}, BodyKinds: []string{"CompoundStmt", "WhileStmt"}}, DecisionUnsupported, ReasonUnboundedLoop},
		{"recursion", Function{Name: "f", ReturnType: Type{Kind: TypeInteger}, Calls: []string{"f"}}, DecisionUnsupported, ReasonRecursion},
		{"callee spelling does not prove purity", Function{Name: "f", ReturnType: Type{Kind: TypeInteger}, Calls: []string{"abs"}}, DecisionUnsupported, ReasonUnknownCall},
		{"callee spelling does not prove temp IO", Function{Name: "f", ReturnType: Type{Kind: TypeInteger}, Calls: []string{"fopen"}}, DecisionUnsupported, ReasonUnknownCall},
		{"unmodeled constructor", Function{Name: "f", ReturnType: Type{Kind: TypeInteger}, BodyKinds: []string{"CompoundStmt", "CXXConstructExpr"}}, DecisionUnsupported, ReasonUnsupportedSyntax},
		{"condition-only loop", Function{Name: "f", ReturnType: Type{Kind: TypeInteger}, BodyKinds: []string{"CompoundStmt", "ForStmt"}, Branches: []Branch{{Kind: BranchLoop, Predicate: Predicate{Operator: "<", Left: "i", Right: "4"}}}}, DecisionUnsupported, ReasonUnboundedLoop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (SafetyClassifier{}).Classify(tt.fn)
			if got.Kind != tt.want || got.Reason != tt.reason {
				t.Fatalf("got %#v, want %s/%s", got, tt.want, tt.reason)
			}
		})
	}
}
