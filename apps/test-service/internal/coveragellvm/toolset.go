package coveragellvm

import (
	"errors"
	"os"
	"sync"

	"unit-test-ide.local/test-service/internal/coveragerun"
)

var (
	ErrInvalidToolset      = errors.New("invalid LLVM toolset")
	ErrUnsupportedPlatform = errors.New("LLVM coverage is unsupported on this platform")
)

type pinnedTool struct {
	path   string
	file   *os.File
	info   os.FileInfo
	sha256 string
	native nativeFileIdentity
}

type Toolset struct {
	compiler pinnedTool
	profdata pinnedTool
	cov      pinnedTool
	version  string
	identity string

	installationPath   string
	installationFile   *os.File
	installationInfo   os.FileInfo
	installationNative nativeFileIdentity

	mu        sync.Mutex
	claimed   bool
	closeOnce sync.Once
	closeErr  error
}

type OwnershipClaim struct {
	toolset *Toolset
	mu      sync.Mutex
	done    bool
}

type toolRole uint8

const (
	compilerRole toolRole = iota
	profdataRole
	covRole
)

type trustedTool struct {
	owner *Toolset
	role  toolRole
}

func (tool trustedTool) Path() string {
	if tool.owner == nil {
		return ""
	}
	tool.owner.mu.Lock()
	defer tool.owner.mu.Unlock()
	return tool.owner.tool(tool.role).path
}

func (tool trustedTool) Verify() error {
	if tool.owner == nil {
		return ErrInvalidToolset
	}
	tool.owner.mu.Lock()
	defer tool.owner.mu.Unlock()
	return tool.owner.verifyToolLocked(tool.role)
}

func (t *Toolset) Compiler() coveragerun.TrustedPath {
	return trustedTool{owner: t, role: compilerRole}
}

func (t *Toolset) Profdata() coveragerun.TrustedPath {
	return trustedTool{owner: t, role: profdataRole}
}

func (t *Toolset) Cov() coveragerun.TrustedPath {
	return trustedTool{owner: t, role: covRole}
}

func (t *Toolset) Version() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.version
}

func (t *Toolset) Identity() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.identity
}

func (t *Toolset) ClaimOwnership() (*OwnershipClaim, error) {
	if t == nil {
		return nil, ErrInvalidToolset
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.claimed || t.installationFile == nil || t.compiler.file == nil || t.profdata.file == nil || t.cov.file == nil {
		return nil, ErrInvalidToolset
	}
	t.claimed = true
	return &OwnershipClaim{toolset: t}, nil
}

func (claim *OwnershipClaim) Rollback() {
	if claim == nil {
		return
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	if claim.done {
		return
	}
	claim.done = true
	if claim.toolset != nil {
		claim.toolset.mu.Lock()
		claim.toolset.claimed = false
		claim.toolset.mu.Unlock()
	}
}

func (claim *OwnershipClaim) Commit() {
	if claim == nil {
		return
	}
	claim.mu.Lock()
	claim.done = true
	claim.mu.Unlock()
}

func (t *Toolset) Verify() error {
	if t == nil {
		return ErrInvalidToolset
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.installationFile == nil || t.compiler.file == nil || t.profdata.file == nil || t.cov.file == nil {
		return ErrInvalidToolset
	}
	if err := verifyPinnedDirectory(
		t.installationPath, t.installationFile, t.installationInfo, t.installationNative,
	); err != nil {
		return errors.Join(ErrInvalidToolset, err)
	}
	for _, role := range []toolRole{compilerRole, profdataRole, covRole} {
		if err := t.verifyToolLocked(role); err != nil {
			return err
		}
	}
	return nil
}

func (t *Toolset) verifyToolLocked(role toolRole) error {
	if t == nil || t.installationFile == nil {
		return ErrInvalidToolset
	}
	tool := t.tool(role)
	if err := verifyPinnedTool(tool); err != nil {
		return errors.Join(ErrInvalidToolset, err)
	}
	return nil
}

func (t *Toolset) tool(role toolRole) *pinnedTool {
	switch role {
	case compilerRole:
		return &t.compiler
	case profdataRole:
		return &t.profdata
	default:
		return &t.cov
	}
}

func (t *Toolset) Close() error {
	if t == nil {
		return nil
	}
	t.closeOnce.Do(func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		var result error
		for _, tool := range []*pinnedTool{&t.cov, &t.profdata, &t.compiler} {
			if tool.file != nil {
				result = errors.Join(result, tool.file.Close())
				tool.file = nil
				tool.info = nil
			}
		}
		if t.installationFile != nil {
			result = errors.Join(result, t.installationFile.Close())
			t.installationFile = nil
			t.installationInfo = nil
		}
		t.closeErr = result
	})
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeErr
}
