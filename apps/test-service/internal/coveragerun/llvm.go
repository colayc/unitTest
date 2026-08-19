package coveragerun

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"unit-test-ide.local/test-service/internal/processcontrol"
)

var ErrInvalidLLVMInvocation = errors.New("invalid llvm coverage invocation")

// TrustedPath is implemented by the capability objects owned by the runtime
// and coverage bundle layers. A collector never accepts a raw request path as
// a tool or binary identity.
type TrustedPath interface {
	Path() string
	Verify() error
}

type LLVMInputs struct {
	Profdata         TrustedPath
	Cov              TrustedPath
	Binary           TrustedPath
	ProfileDirectory TrustedPath
	ProfileFiles     []string
	MergedProfile    string
}

type LLVMInvocation struct {
	Merge  processcontrol.Spec
	Export processcontrol.Spec
}

var llvmEnvUnset = []string{
	"LLVM_PROFILE_FILE",
	"LLVM_PROFILE_MERGE_FILE",
	"GCOV_PREFIX",
	"GCOV_PREFIX_STRIP",
	"PYTHONPATH",
	"PYTHONHOME",
}

// BuildLLVMInvocation creates the fixed process specs for llvm-profdata merge
// and llvm-cov export. It intentionally leaves report serialization to the
// caller's captured stdout; no shell redirection or user-provided arguments
// are introduced.
func BuildLLVMInvocation(input LLVMInputs) (LLVMInvocation, error) {
	if input.Profdata == nil || input.Cov == nil || input.Binary == nil || input.ProfileDirectory == nil {
		return LLVMInvocation{}, ErrInvalidLLVMInvocation
	}
	profdata, err := verifiedPath(input.Profdata, "llvm-profdata", "llvm-profdata.exe")
	if err != nil {
		return LLVMInvocation{}, err
	}
	cov, err := verifiedPath(input.Cov, "llvm-cov", "llvm-cov.exe")
	if err != nil {
		return LLVMInvocation{}, err
	}
	binary, err := verifiedPath(input.Binary)
	if err != nil {
		return LLVMInvocation{}, err
	}
	profileDirectory, err := verifiedPath(input.ProfileDirectory)
	if err != nil {
		return LLVMInvocation{}, err
	}
	if !safeAbsolute(profileDirectory) || len(input.ProfileFiles) == 0 || !safeFileName(input.MergedProfile) {
		return LLVMInvocation{}, ErrInvalidLLVMInvocation
	}
	profileFiles := make([]string, len(input.ProfileFiles))
	seen := make(map[string]struct{}, len(input.ProfileFiles))
	for index, name := range input.ProfileFiles {
		if !safeFileName(name) {
			return LLVMInvocation{}, ErrInvalidLLVMInvocation
		}
		if _, duplicate := seen[name]; duplicate {
			return LLVMInvocation{}, ErrInvalidLLVMInvocation
		}
		seen[name] = struct{}{}
		profileFiles[index] = filepath.Join(profileDirectory, name)
	}
	merged := filepath.Join(profileDirectory, input.MergedProfile)
	envUnset := append([]string(nil), llvmEnvUnset...)
	mergeArgs := make([]string, 0, 3+len(profileFiles))
	mergeArgs = append(mergeArgs, "merge", "-sparse")
	mergeArgs = append(mergeArgs, profileFiles...)
	mergeArgs = append(mergeArgs, "-o", merged)
	exportArgs := []string{"export", "-format=text", "-instr-profile=" + merged, binary}
	return LLVMInvocation{
		Merge: processcontrol.Spec{
			Executable: profdata, Args: mergeArgs, Dir: profileDirectory,
			EnvUnset: append([]string(nil), envUnset...),
		},
		Export: processcontrol.Spec{
			Executable: cov, Args: exportArgs, Dir: profileDirectory,
			EnvUnset: append([]string(nil), envUnset...),
		},
	}, nil
}

func verifiedPath(value TrustedPath, executableNames ...string) (string, error) {
	if err := value.Verify(); err != nil {
		return "", fmt.Errorf("%w: capability verification: %v", ErrInvalidLLVMInvocation, err)
	}
	path := value.Path()
	if !safeAbsolute(path) {
		return "", ErrInvalidLLVMInvocation
	}
	if len(executableNames) != 0 {
		base := strings.ToLower(filepath.Base(path))
		matched := false
		for _, name := range executableNames {
			if base == strings.ToLower(name) {
				matched = true
				break
			}
		}
		if !matched {
			return "", ErrInvalidLLVMInvocation
		}
	}
	return path, nil
}

func safeAbsolute(path string) bool {
	return path != "" && filepath.IsAbs(path) && !strings.ContainsRune(path, '\x00')
}

func safeFileName(value string) bool {
	if value == "" || len(value) > 128 || value[0] == '.' {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
