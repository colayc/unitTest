package toolchain

import (
	"context"
	"errors"
)

type Family string

const (
	FamilyMSVC    Family = "msvc"
	FamilyClangCL Family = "clang-cl"
	FamilyGCC     Family = "gcc"
	FamilyClang   Family = "clang"
)

type CoverageCapability struct {
	LLVMProfdata string
	LLVMCov      string
	GCov         string
}

type Instance struct {
	ID                 string
	Family             Family
	CCompiler          string
	CXXCompiler        string
	Version            string
	TargetTriple       string
	HostArchitecture   string
	TargetArchitecture string
	Sysroot            string
	Environment        []string
	Generators         []string
	Coverage           CoverageCapability
}

type Candidate struct {
	ID          string
	Family      Family
	CCompiler   string
	CXXCompiler string
	Manual      bool
	Ninja       string
	Make        string
}

type Issue struct {
	Code     string
	Message  string
	Blocking bool
}

type Adapter interface {
	Discover(context.Context) ([]Instance, error)
	Probe(context.Context, Candidate) (Instance, error)
}

var ErrInvalidRegistry = errors.New("invalid toolchain registry")
