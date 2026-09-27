package testgenanalysis

import "strings"

type SafetyClassifier struct{}

var bodyKinds = map[string]bool{"CompoundStmt": true, "IfStmt": true, "SwitchStmt": true, "CaseStmt": true, "DefaultStmt": true, "BreakStmt": true, "ReturnStmt": true, "BinaryOperator": true, "CompoundAssignOperator": true, "UnaryOperator": true, "ConditionalOperator": true, "DeclStmt": true, "VarDecl": true, "IntegerLiteral": true, "FloatingLiteral": true, "CXXBoolLiteralExpr": true, "DeclRefExpr": true, "ImplicitCastExpr": true, "ConstantExpr": true, "ParenExpr": true, "ArraySubscriptExpr": true, "MemberExpr": true, "InitListExpr": true, "CStyleCastExpr": true, "CXXStaticCastExpr": true, "CallExpr": true, "ForStmt": true, "NullStmt": true, "CharacterLiteral": true, "StringLiteral": true, "MaterializeTemporaryExpr": true, "CXXConstructExpr": true, "CXXThisExpr": true, "CXXMemberCallExpr": true}
var pureCalls = map[string]bool{"abs": true, "labs": true, "llabs": true, "strlen": true, "memcmp": true, "strcmp": true, "strncmp": true}
var temporaryIOCalls = map[string]bool{"fopen": true, "fclose": true, "fread": true, "fwrite": true, "fprintf": true, "fputs": true, "fgets": true}
var externalCalls = map[string]bool{"socket": true, "connect": true, "send": true, "recv": true, "system": true, "popen": true, "fork": true, "execve": true, "CreateProcess": true, "rand": true, "srand": true, "time": true, "gettimeofday": true, "clock_gettime": true, "sleep": true, "GetTickCount": true, "open": true, "remove": true, "unlink": true, "sqlite3_open": true, "mysql_init": true, "PQconnectdb": true}

func (SafetyClassifier) Classify(f Function) Decision {
	unsupported := func(r ReasonCode) Decision { return Decision{DecisionUnsupported, r} }
	if f.Name == "" || f.LocationDigest == "" && len(f.BodyKinds) > 0 && f.ReturnType.Kind == TypeUnknown {
		return unsupported(ReasonIncompleteAST)
	}
	if !safeType(f.ReturnType) {
		return unsupported(ReasonUnsupportedType)
	}
	for _, p := range f.Parameters {
		if !safeType(p.Type) {
			return unsupported(ReasonUnsupportedType)
		}
	}
	for _, kind := range f.BodyKinds {
		if kind == "MacroExpansion" {
			return unsupported(ReasonMacroExpansion)
		}
		if kind == "WhileStmt" || kind == "DoStmt" {
			return unsupported(ReasonUnboundedLoop)
		}
		if !bodyKinds[kind] {
			return unsupported(ReasonUnsupportedSyntax)
		}
	}
	for _, branch := range f.Branches {
		if branch.Kind == BranchLoop && !branch.BoundVerified {
			return unsupported(ReasonUnboundedLoop)
		}
	}
	temporary := false
	for _, call := range f.Calls {
		if call == f.Name {
			return unsupported(ReasonRecursion)
		}
		if externalCalls[call] {
			return unsupported(ReasonExternalCall)
		}
		if temporaryIOCalls[call] {
			temporary = true
			continue
		}
		if !pureCalls[call] {
			return unsupported(ReasonUnknownCall)
		}
	}
	if temporary {
		return Decision{DecisionRequiresConfirmation, ReasonTemporaryIO}
	}
	return Decision{DecisionSupported, ReasonNone}
}

func safeType(t Type) bool {
	if strings.ContainsAny(t.Spelling, "\r\n\x00") || strings.Contains(t.Spelling, "volatile") || strings.Contains(t.Spelling, "(*") || strings.Contains(t.Spelling, "&") || strings.Contains(t.Spelling, "...") || strings.Contains(t.Spelling, "/") || strings.Contains(t.Spelling, "\\") {
		return false
	}
	switch t.Kind {
	case TypeVoid, TypeBoolean, TypeInteger, TypeFloating, TypeEnum, TypeRecord:
		return true
	case TypeArray:
		return t.Bound > 0 && t.Bound <= 1024
	case TypePointer:
		return t.Spelling == "char *" || t.Spelling == "const char *" || t.Spelling == "void *"
	default:
		return false
	}
}
