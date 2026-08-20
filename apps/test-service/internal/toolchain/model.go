package toolchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

type Family string

const (
	FamilyMSVC    Family = "msvc"
	FamilyClangCL Family = "clang-cl"
	FamilyGCC     Family = "gcc"
	FamilyClang   Family = "clang"
)

type ExecutableEvidence struct {
	FileIdentity string
	SHA256       string
}

type CoverageCapability struct {
	LLVMProfdata     string
	LLVMCov          string
	GCov             string
	CompilerEvidence ExecutableEvidence
	ProfdataEvidence ExecutableEvidence
	CovEvidence      ExecutableEvidence
	ToolsetIdentity  string
}

// LLVMToolsetIdentity binds a discovery version and the three exact executable
// snapshots into one stable configure/storage identity.
func LLVMToolsetIdentity(version string, paths []string, evidence []ExecutableEvidence) string {
	if version == "" || len(paths) != 3 || len(evidence) != 3 {
		return ""
	}
	parts := []string{"llvm-toolset-v1", version}
	for index := range paths {
		if paths[index] == "" || !validExecutableEvidence(evidence[index]) {
			return ""
		}
		parts = append(parts, identityPath(paths[index]), evidence[index].FileIdentity, evidence[index].SHA256)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func validExecutableEvidence(value ExecutableEvidence) bool {
	if !strings.HasPrefix(value.FileIdentity, "windows:") || len(value.FileIdentity) != len("windows:")+8+1+16 ||
		len(value.SHA256) != 64 || value.SHA256 != strings.ToLower(value.SHA256) {
		return false
	}
	for _, value := range []string{strings.ReplaceAll(strings.TrimPrefix(value.FileIdentity, "windows:"), ":", ""), value.SHA256} {
		if _, err := hex.DecodeString(value); err != nil {
			return false
		}
	}
	return true
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
