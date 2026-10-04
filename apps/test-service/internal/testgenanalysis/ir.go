package testgenanalysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

const IRVersion = 1

type TypeKind string

const (
	TypeUnknown  TypeKind = "unknown"
	TypeVoid     TypeKind = "void"
	TypeBoolean  TypeKind = "boolean"
	TypeInteger  TypeKind = "integer"
	TypeFloating TypeKind = "floating"
	TypeEnum     TypeKind = "enum"
	TypeArray    TypeKind = "array"
	TypeRecord   TypeKind = "record"
	TypePointer  TypeKind = "pointer"
	TypeString   TypeKind = "string"
)

// Field is a bounded, independently proven record member. The analyzer does
// not populate it until it can prove layout and construction safety.
type Field struct {
	Name string `json:"name"`
	Type Type   `json:"type"`
}

type Type struct {
	Kind           TypeKind `json:"kind"`
	Spelling       string   `json:"spelling,omitempty"`
	Bound          int      `json:"bound,omitempty"`
	Proven         bool     `json:"proven,omitempty"`
	BitWidth       int      `json:"bitWidth,omitempty"`
	Signed         bool     `json:"signed,omitempty"`
	EnumValues     []string `json:"enumValues,omitempty"`
	Element        *Type    `json:"element,omitempty"`
	Fields         []Field  `json:"fields,omitempty"`
	Owned          bool     `json:"owned,omitempty"`
	MaxLength      int      `json:"maxLength,omitempty"`
	AllowNonFinite bool     `json:"allowNonFinite,omitempty"`
}
type Parameter struct {
	Name string `json:"name"`
	Type Type   `json:"type"`
}
type BranchKind string

const (
	BranchIf           BranchKind = "if"
	BranchSwitch       BranchKind = "switch"
	BranchCase         BranchKind = "case"
	BranchDefault      BranchKind = "default"
	BranchShortCircuit BranchKind = "short-circuit"
	BranchLoop         BranchKind = "loop"
)

type Predicate struct {
	Operator string `json:"operator"`
	Left     string `json:"left,omitempty"`
	Right    string `json:"right,omitempty"`
}
type Branch struct {
	Kind           BranchKind  `json:"kind"`
	Predicate      Predicate   `json:"predicate"`
	OwnerSwitchID  string      `json:"ownerSwitchId,omitempty"`
	BoundVerified  bool        `json:"boundVerified,omitempty"`
	PathVerified   bool        `json:"pathVerified,omitempty"`
	PathPredicates []Predicate `json:"pathPredicates,omitempty"`
	LocationDigest string      `json:"locationDigest"`
}
type DecisionKind string

const (
	DecisionSupported            DecisionKind = "supported"
	DecisionUnsupported          DecisionKind = "unsupported"
	DecisionRequiresConfirmation DecisionKind = "requires-confirmation"
)

type ReasonCode string

const (
	ReasonNone              ReasonCode = "none"
	ReasonUnknownCall       ReasonCode = "unknown-call"
	ReasonExternalCall      ReasonCode = "external-call"
	ReasonUnsupportedSyntax ReasonCode = "unsupported-syntax"
	ReasonUnsupportedType   ReasonCode = "unsupported-type"
	ReasonUnboundedLoop     ReasonCode = "unbounded-loop"
	ReasonRecursion         ReasonCode = "recursion"
	ReasonMacroExpansion    ReasonCode = "macro-expansion"
	ReasonIncompleteAST     ReasonCode = "incomplete-ast"
	ReasonTemporaryIO       ReasonCode = "temporary-io"
)

type Decision struct {
	Kind   DecisionKind `json:"kind"`
	Reason ReasonCode   `json:"reason"`
}
type EffectKind string

const (
	EffectLocalMemory EffectKind = "local-memory"
	EffectExternal    EffectKind = "external"
	EffectUnknown     EffectKind = "unknown"
)

type SourceExcerpt struct {
	Digest         string `json:"digest,omitempty"`
	LocationDigest string `json:"locationDigest"`
	StartByte      int    `json:"startByte"`
	EndByte        int    `json:"endByte"`
}
type Diagnostic struct {
	SymbolID string     `json:"symbolId"`
	Reason   ReasonCode `json:"reason"`
}

// ClosedExpression is a deliberately small, path-free expression model used
// only when the analyzer can preserve the complete scalar return semantics.
// Unsupported statements leave ReturnRules empty and therefore cannot create
// an oracle proof.
type ExpressionKind string

const (
	ExpressionParameter ExpressionKind = "parameter"
	ExpressionInteger   ExpressionKind = "integer"
	ExpressionBoolean   ExpressionKind = "boolean"
	ExpressionEnum      ExpressionKind = "enum"
	ExpressionUnary     ExpressionKind = "unary"
	ExpressionBinary    ExpressionKind = "binary"
)

type ClosedExpression struct {
	Kind     ExpressionKind    `json:"kind"`
	Name     string            `json:"name,omitempty"`
	Value    string            `json:"value,omitempty"`
	Operator string            `json:"operator,omitempty"`
	Left     *ClosedExpression `json:"left,omitempty"`
	Right    *ClosedExpression `json:"right,omitempty"`
}

type ReturnRule struct {
	Conditions []ClosedExpression `json:"conditions,omitempty"`
	Result     ClosedExpression   `json:"result"`
}

// OracleProof is a closed, source-bound fact emitted only by a trusted
// analyzer/contract importer. The current analyzer emits none; absence keeps
// verified assertion generation closed rather than trusting runtime output.
type OracleProof struct {
	Kind           string `json:"kind"`
	CandidateID    string `json:"candidateId"`
	InputDigest    string `json:"inputDigest"`
	TargetDigest   string `json:"targetDigest"`
	SourceDigest   string `json:"sourceDigest"`
	ExpectedDigest string `json:"expectedDigest"`
	Rule           string `json:"rule"`
	Tolerance      string `json:"tolerance,omitempty"`
}
type Function struct {
	SymbolID       string        `json:"symbolId"`
	Name           string        `json:"name"`
	ReturnType     Type          `json:"returnType"`
	Parameters     []Parameter   `json:"parameters"`
	LocalTypes     []Type        `json:"localTypes"`
	Branches       []Branch      `json:"branches"`
	Calls          []string      `json:"calls"`
	BodyKinds      []string      `json:"bodyKinds"`
	LocationDigest string        `json:"locationDigest"`
	Excerpt        SourceExcerpt `json:"excerpt"`
	Effect         EffectKind    `json:"effect"`
	Decision       Decision      `json:"decision"`
	ReturnRules    []ReturnRule  `json:"returnRules,omitempty"`
	OracleProofs   []OracleProof `json:"oracleProofs,omitempty"`
	originVerified bool
}
type TranslationUnit struct {
	ID           string   `json:"id"`
	SourceDigest string   `json:"sourceDigest"`
	Symbols      []string `json:"symbols"`
}
type Program struct {
	Version          int               `json:"version"`
	TranslationUnits []TranslationUnit `json:"translationUnits"`
	Functions        []Function        `json:"functions"`
	Diagnostics      []Diagnostic      `json:"diagnostics"`
	Digest           string            `json:"digest"`
}

func digestBytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func bindSourceExcerpts(program *Program, source []byte) error {
	for i := range program.Functions {
		ref := &program.Functions[i].Excerpt
		if !program.Functions[i].originVerified || ref.StartByte < 0 || ref.EndByte <= ref.StartByte || ref.EndByte > len(source) || ref.LocationDigest != program.Functions[i].LocationDigest {
			return errors.New("invalid source excerpt")
		}
		ref.Digest = digestBytes(source[ref.StartByte:ref.EndByte])
	}
	return sealProgram(program)
}
func sealProgram(program *Program) error {
	sort.Slice(program.Functions, func(i, j int) bool { return program.Functions[i].SymbolID < program.Functions[j].SymbolID })
	for i := range program.Functions {
		sort.Strings(program.Functions[i].Calls)
		sort.Strings(program.Functions[i].BodyKinds)
		sort.Slice(program.Functions[i].Branches, func(a, b int) bool {
			return program.Functions[i].Branches[a].LocationDigest < program.Functions[i].Branches[b].LocationDigest
		})
	}
	sort.Slice(program.Diagnostics, func(i, j int) bool {
		if program.Diagnostics[i].SymbolID == program.Diagnostics[j].SymbolID {
			return program.Diagnostics[i].Reason < program.Diagnostics[j].Reason
		}
		return program.Diagnostics[i].SymbolID < program.Diagnostics[j].SymbolID
	})
	for i := range program.TranslationUnits {
		sort.Strings(program.TranslationUnits[i].Symbols)
	}
	bytes, err := json.Marshal(struct {
		Version          int               `json:"version"`
		TranslationUnits []TranslationUnit `json:"translationUnits"`
		Functions        []Function        `json:"functions"`
		Diagnostics      []Diagnostic      `json:"diagnostics"`
	}{program.Version, program.TranslationUnits, program.Functions, program.Diagnostics})
	if err != nil {
		return err
	}
	program.Digest = digestBytes(bytes)
	return nil
}
