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
)

type Type struct {
	Kind     TypeKind `json:"kind"`
	Spelling string   `json:"spelling,omitempty"`
	Bound    int      `json:"bound,omitempty"`
	Proven   bool     `json:"proven,omitempty"`
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
	Kind           BranchKind `json:"kind"`
	Predicate      Predicate  `json:"predicate"`
	OwnerSwitchID  string     `json:"ownerSwitchId,omitempty"`
	BoundVerified  bool       `json:"boundVerified,omitempty"`
	LocationDigest string     `json:"locationDigest"`
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
