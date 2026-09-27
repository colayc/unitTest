package testgenanalysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const maxASTNodes = 100000
const maxASTDepth = 128
const maxFunctions = 4096
const maxBranches = 16384

type astLocation struct {
	Line         int          `json:"line"`
	Col          int          `json:"col"`
	SpellingLoc  *astLocation `json:"spellingLoc"`
	ExpansionLoc *astLocation `json:"expansionLoc"`
}
type astRange struct {
	Begin astLocation `json:"begin"`
	End   astLocation `json:"end"`
}
type astType struct {
	QualType string `json:"qualType"`
}
type astReference struct {
	Name string `json:"name"`
}
type astNode struct {
	Kind           string        `json:"kind"`
	Name           string        `json:"name"`
	Type           astType       `json:"type"`
	Loc            astLocation   `json:"loc"`
	Range          astRange      `json:"range"`
	Opcode         string        `json:"opcode"`
	Value          string        `json:"value"`
	ReferencedDecl *astReference `json:"referencedDecl"`
	Inner          []*astNode    `json:"inner"`
}

func decodeAST(reader io.Reader, limit int64, sourceDigest string) (Program, error) {
	if !validSHA(sourceDigest) || limit <= 0 || limit > 32<<20 {
		return Program{}, errors.New("invalid AST decode budget or source identity")
	}
	limited := &io.LimitedReader{R: reader, N: limit + 1}
	decoder := json.NewDecoder(limited)
	var root astNode
	if err := decoder.Decode(&root); err != nil {
		return Program{}, errors.New("malformed AST JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Program{}, errors.New("trailing AST JSON")
	}
	if limited.N <= 0 || root.Kind != "TranslationUnitDecl" {
		return Program{}, errors.New("invalid or oversized Clang AST")
	}
	nodes := 0
	var check func(*astNode, int) error
	check = func(n *astNode, depth int) error {
		if n == nil || n.Kind == "" || depth > maxASTDepth {
			return errors.New("incomplete or deeply nested AST")
		}
		nodes++
		if nodes > maxASTNodes {
			return errors.New("AST node budget exceeded")
		}
		for _, child := range n.Inner {
			if n.Kind == "ForStmt" && (child == nil || child.Kind == "") {
				continue
			}
			if err := check(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(&root, 0); err != nil {
		return Program{}, err
	}
	declared := map[string]TypeKind{}
	var collect func(*astNode)
	collect = func(n *astNode) {
		if (n.Kind == "RecordDecl" || n.Kind == "CXXRecordDecl") && safeIdentifier(n.Name) {
			declared[n.Name] = TypeRecord
		}
		if n.Kind == "EnumDecl" && safeIdentifier(n.Name) {
			declared[n.Name] = TypeEnum
		}
		for _, child := range n.Inner {
			if child != nil {
				collect(child)
			}
		}
	}
	collect(&root)
	program := Program{Version: IRVersion, TranslationUnits: []TranslationUnit{{ID: digestBytes([]byte("translation-unit:" + sourceDigest)), SourceDigest: sourceDigest, Symbols: []string{}}}, Functions: []Function{}, Diagnostics: []Diagnostic{}}
	var visit func(*astNode) error
	visit = func(n *astNode) error {
		if n.Kind == "FunctionDecl" || n.Kind == "CXXMethodDecl" || n.Kind == "CXXConstructorDecl" {
			if n.Name == "" || n.Type.QualType == "" {
				return errors.New("partial function declaration in AST")
			}
			body := findDirectBody(n)
			if body != nil {
				if len(program.Functions) >= maxFunctions {
					return errors.New("AST function budget exceeded")
				}
				f, err := functionFromAST(n, body, sourceDigest, declared)
				if err != nil {
					return err
				}
				program.Functions = append(program.Functions, f)
				program.TranslationUnits[0].Symbols = append(program.TranslationUnits[0].Symbols, f.SymbolID)
				if f.Decision.Kind != DecisionSupported {
					program.Diagnostics = append(program.Diagnostics, Diagnostic{f.SymbolID, f.Decision.Reason})
				}
			}
			return nil
		}
		if n.Kind == "FunctionTemplateDecl" {
			for _, child := range n.Inner {
				if child.Kind == "FunctionDecl" {
					if child.Name == "" {
						return errors.New("partial template AST")
					}
					id := digestBytes([]byte(sourceDigest + ":template:" + child.Name))
					program.Diagnostics = append(program.Diagnostics, Diagnostic{id, ReasonUnsupportedSyntax})
				}
			}
			return nil
		}
		for _, child := range n.Inner {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, child := range root.Inner {
		if err := visit(child); err != nil {
			return Program{}, err
		}
	}
	if err := sealProgram(&program); err != nil {
		return Program{}, err
	}
	return program, nil
}

func findDirectBody(n *astNode) *astNode {
	for _, child := range n.Inner {
		if child != nil && child.Kind == "CompoundStmt" {
			return child
		}
	}
	return nil
}
func functionFromAST(n, body *astNode, sourceDigest string, declared map[string]TypeKind) (Function, error) {
	if !safeIdentifier(n.Name) || n.Loc.Line < 0 || n.Loc.Col < 0 {
		return Function{}, errors.New("unusable function identity")
	}
	location := locationDigest(sourceDigest, effectiveLocation(n))
	f := Function{SymbolID: digestBytes([]byte("symbol:" + sourceDigest + ":" + n.Kind + ":" + n.Name + ":" + location)), Name: n.Name, ReturnType: parseType(strings.SplitN(n.Type.QualType, " (", 2)[0], declared), Parameters: []Parameter{}, Branches: []Branch{}, Calls: []string{}, BodyKinds: []string{}, LocationDigest: location}
	for _, child := range n.Inner {
		if child.Kind == "ParmVarDecl" {
			if !safeIdentifier(child.Name) {
				return Function{}, errors.New("unusable parameter identity")
			}
			f.Parameters = append(f.Parameters, Parameter{child.Name, parseType(child.Type.QualType, declared)})
		}
	}
	seenKinds := map[string]bool{}
	var walk func(*astNode, int) error
	walk = func(node *astNode, depth int) error {
		if depth > maxASTDepth {
			return errors.New("function AST too deep")
		}
		if !seenKinds[node.Kind] {
			f.BodyKinds = append(f.BodyKinds, node.Kind)
			seenKinds[node.Kind] = true
		}
		if hasMacroLocation(node.Loc) || hasMacroLocation(node.Range.Begin) || hasMacroLocation(node.Range.End) {
			if !seenKinds["MacroExpansion"] {
				f.BodyKinds = append(f.BodyKinds, "MacroExpansion")
				seenKinds["MacroExpansion"] = true
			}
		}
		var kind BranchKind
		switch node.Kind {
		case "IfStmt":
			kind = BranchIf
		case "SwitchStmt":
			kind = BranchSwitch
		case "ForStmt":
			kind = BranchLoop
		case "BinaryOperator":
			if node.Opcode == "&&" || node.Opcode == "||" {
				kind = BranchShortCircuit
			}
		}
		if kind != "" {
			if len(f.Branches) >= maxBranches {
				return errors.New("AST branch budget exceeded")
			}
			pred := extractBranchPredicate(node, kind)
			bound := kind == BranchLoop && verifiedForLoop(node, pred)
			f.Branches = append(f.Branches, Branch{kind, pred, bound, locationDigest(sourceDigest, effectiveLocation(node))})
			if pred.Operator == "unknown" && kind != BranchSwitch {
				if !seenKinds["UnknownPredicate"] {
					f.BodyKinds = append(f.BodyKinds, "UnknownPredicate")
					seenKinds["UnknownPredicate"] = true
				}
			}
		}
		if node.Kind == "CallExpr" || node.Kind == "CXXMemberCallExpr" {
			name := calledName(node)
			if name == "" {
				name = "<unknown>"
			}
			f.Calls = append(f.Calls, name)
		}
		for _, child := range node.Inner {
			if node.Kind == "ForStmt" && (child == nil || child.Kind == "") {
				continue
			}
			if err := walk(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(body, 0); err != nil {
		return Function{}, err
	}
	f.Decision = (SafetyClassifier{}).Classify(f)
	return f, nil
}
func validSHA(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]{0,127}$`)

func safeIdentifier(v string) bool { return identifierPattern.MatchString(v) }
func locationDigest(source string, loc astLocation) string {
	return digestBytes([]byte(fmt.Sprintf("location:%s:%d:%d", source, loc.Line, loc.Col)))
}
func effectiveLocation(n *astNode) astLocation {
	if n.Loc.Line > 0 || n.Loc.Col > 0 {
		return n.Loc
	}
	return n.Range.Begin
}
func hasMacroLocation(loc astLocation) bool { return loc.SpellingLoc != nil || loc.ExpansionLoc != nil }
func parseType(v string, declared map[string]TypeKind) Type {
	v = strings.TrimSpace(v)
	t := Type{Kind: TypeUnknown}
	if len(v) > 128 || strings.ContainsAny(v, "/\\:\r\n\x00") {
		return t
	}
	if !regexp.MustCompile(`^[A-Za-z_0-9 *\[\]]+$`).MatchString(v) {
		return t
	}
	t.Spelling = v
	if strings.HasSuffix(v, "*") {
		t.Kind = TypePointer
		return t
	}
	if strings.Contains(v, "[") && strings.HasSuffix(v, "]") {
		left := strings.LastIndex(v, "[")
		n, err := strconv.Atoi(v[left+1 : len(v)-1])
		if err == nil && n > 0 && n <= 1024 {
			t.Kind = TypeArray
			t.Bound = n
		}
		return t
	}
	switch v {
	case "void":
		t.Kind = TypeVoid
	case "bool", "_Bool":
		t.Kind = TypeBoolean
	case "float", "double", "long double":
		t.Kind = TypeFloating
	case "char", "signed char", "unsigned char", "short", "unsigned short", "int", "unsigned int", "long", "unsigned long", "long long", "unsigned long long", "size_t":
		t.Kind = TypeInteger
	default:
		if strings.HasPrefix(v, "enum ") {
			t.Kind = TypeEnum
		} else if strings.HasPrefix(v, "struct ") || strings.HasPrefix(v, "class ") {
			t.Kind = TypeRecord
		} else if kind, ok := declared[v]; ok {
			t.Kind = kind
		}
	}
	return t
}
func extractBranchPredicate(node *astNode, kind BranchKind) Predicate {
	if kind == BranchShortCircuit {
		return Predicate{Operator: node.Opcode, Left: expression(node.Inner, 0), Right: expression(node.Inner, 1)}
	}
	for _, child := range node.Inner {
		if child == nil {
			continue
		}
		if child.Kind == "BinaryOperator" && (child.Opcode == "<" || child.Opcode == "<=" || child.Opcode == ">" || child.Opcode == ">=" || child.Opcode == "==" || child.Opcode == "!=" || child.Opcode == "&&" || child.Opcode == "||") {
			return Predicate{Operator: child.Opcode, Left: expression(child.Inner, 0), Right: expression(child.Inner, 1)}
		}
		if child.Kind == "CompoundStmt" {
			break
		}
	}
	for _, child := range node.Inner {
		if child != nil && child.Kind == "UnaryOperator" && child.Opcode == "!" {
			return Predicate{Operator: "!", Left: expression(child.Inner, 0)}
		}
	}
	if kind == BranchSwitch {
		return Predicate{Operator: "switch", Left: expression(node.Inner, 0)}
	}
	return Predicate{Operator: "unknown"}
}
func expression(nodes []*astNode, index int) string {
	if index >= len(nodes) || nodes[index] == nil {
		return ""
	}
	n := nodes[index]
	switch n.Kind {
	case "IntegerLiteral", "FloatingLiteral", "CharacterLiteral":
		return n.Value
	case "DeclRefExpr":
		if n.ReferencedDecl != nil && safeIdentifier(n.ReferencedDecl.Name) {
			return n.ReferencedDecl.Name
		}
	case "ImplicitCastExpr", "ParenExpr":
		return expression(n.Inner, 0)
	}
	return ""
}
func verifiedForLoop(node *astNode, p Predicate) bool {
	if node.Kind != "ForStmt" || len(node.Inner) < 5 || p.Operator != "<" || !safeIdentifier(p.Left) {
		return false
	}
	upper, err := strconv.ParseInt(p.Right, 10, 32)
	if err != nil || upper < 0 || upper > 1024 {
		return false
	}
	init := node.Inner[0]
	if init == nil || init.Kind != "DeclStmt" || len(init.Inner) != 1 || init.Inner[0].Kind != "VarDecl" {
		return false
	}
	variable := init.Inner[0]
	if variable.Name != p.Left || len(variable.Inner) != 1 || variable.Inner[0].Kind != "IntegerLiteral" {
		return false
	}
	start, err := strconv.ParseInt(variable.Inner[0].Value, 10, 32)
	if err != nil || start < 0 || start > upper {
		return false
	}
	step := node.Inner[3]
	if step == nil || step.Kind != "UnaryOperator" || step.Opcode != "++" || expression(step.Inner, 0) != p.Left {
		return false
	}
	body := node.Inner[len(node.Inner)-1]
	if body == nil || body.Kind == "" {
		return false
	}
	return !mutatesVariable(body, p.Left)
}
func mutatesVariable(node *astNode, name string) bool {
	if node == nil {
		return false
	}
	if node.Kind == "BinaryOperator" && node.Opcode == "=" && expression(node.Inner, 0) == name {
		return true
	}
	if node.Kind == "CompoundAssignOperator" && expression(node.Inner, 0) == name {
		return true
	}
	if node.Kind == "UnaryOperator" && (node.Opcode == "++" || node.Opcode == "--") && expression(node.Inner, 0) == name {
		return true
	}
	for _, child := range node.Inner {
		if mutatesVariable(child, name) {
			return true
		}
	}
	return false
}
func calledName(n *astNode) string {
	if n.ReferencedDecl != nil && safeIdentifier(n.ReferencedDecl.Name) {
		return n.ReferencedDecl.Name
	}
	for _, child := range n.Inner {
		if name := calledName(child); name != "" {
			return name
		}
	}
	return ""
}
