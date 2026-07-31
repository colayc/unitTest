package ctest

import "errors"

var (
	ErrInvalidSnapshot = errors.New("invalid CTest show-only JSON snapshot")
	ErrLimitExceeded   = errors.New("CTest show-only JSON limit exceeded")
	ErrInvalidLimits   = errors.New("invalid CTest parser limits")
)

const maxSafeInteger = int64(9_007_199_254_740_991)

type Limits struct {
	MaxDocumentBytes     int
	MaxTests             int
	MaxCommandArguments  int
	MaxPropertiesPerTest int
	MaxBacktraceCommands int
	MaxBacktraceFiles    int
	MaxBacktraceNodes    int
	MaxPropertyStrings   int
	MaxStringBytes       int
}

func DefaultLimits() Limits {
	return Limits{
		MaxDocumentBytes:     128 * 1024 * 1024,
		MaxTests:             100_000,
		MaxCommandArguments:  1_024,
		MaxPropertiesPerTest: 256,
		MaxBacktraceCommands: 100_000,
		MaxBacktraceFiles:    100_000,
		MaxBacktraceNodes:    200_000,
		MaxPropertyStrings:   10_000,
		MaxStringBytes:       64 * 1024,
	}
}

func (limits Limits) Valid() bool {
	return limits.MaxDocumentBytes >= 0 &&
		limits.MaxTests >= 0 &&
		limits.MaxCommandArguments >= 0 &&
		limits.MaxPropertiesPerTest >= 0 &&
		limits.MaxBacktraceCommands >= 0 &&
		limits.MaxBacktraceFiles >= 0 &&
		limits.MaxBacktraceNodes >= 0 &&
		limits.MaxPropertyStrings >= 0 &&
		limits.MaxStringBytes >= 0
}

type Version struct {
	Major int
	Minor int
}

type Snapshot struct {
	Kind           string
	Version        Version
	BacktraceGraph BacktraceGraph
	Tests          []RawTest
}

type BacktraceGraph struct {
	Commands []string
	Files    []string
	Nodes    []BacktraceNode
}

type BacktraceNode struct {
	Command *int
	File    int
	Line    *int
	Parent  *int
}

type RawTest struct {
	Name       string
	Config     string
	Command    []string
	Backtrace  int
	Properties []Property
}

type Property struct {
	Name  string
	Value PropertyValue
}

type PropertyKind string

const (
	PropertyString  PropertyKind = "string"
	PropertyNumber  PropertyKind = "number"
	PropertyBoolean PropertyKind = "boolean"
	PropertyStrings PropertyKind = "strings"
)

type PropertyValue struct {
	Kind    PropertyKind
	String  string
	Number  string
	Boolean bool
	Strings []string
}
