// Package coveragegcc retains the Linux GCC/gcov executable capabilities
// needed by the coverage execution boundary.
package coveragegcc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"sync"

	"unit-test-ide.local/test-service/internal/coverageplatform"
	"unit-test-ide.local/test-service/internal/coveragerun"
)

const instrumentationContract = "gcc-gcov-instrumentation-v1"

// InstrumentationFingerprint identifies the exact GCC coverage compilation
// contract, including the bytes published by WriteInstrumentation.
func InstrumentationFingerprint() string {
	sum := sha256.Sum256([]byte(instrumentationContract + "\x00" + InstrumentationSHA256()))
	return hex.EncodeToString(sum[:])
}

var (
	ErrInvalidToolset      = errors.New("invalid GCC coverage toolset")
	ErrUnsupportedPlatform = errors.New("GCC coverage is unsupported on this platform")
)

type pinnedTool struct {
	path   string
	file   *os.File
	info   os.FileInfo
	sha256 string
	native nativeFileIdentity
}

type Toolset struct {
	compiler    pinnedTool
	cxxCompiler pinnedTool
	gcov        pinnedTool
	version     string
	identity    string

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
	cxxCompilerRole
	gcovRole
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

func (t *Toolset) CCompiler() coveragerun.TrustedPath {
	return trustedTool{owner: t, role: compilerRole}
}
func (t *Toolset) CXXCompiler() coveragerun.TrustedPath {
	return trustedTool{owner: t, role: cxxCompilerRole}
}
func (t *Toolset) GCov() coveragerun.TrustedPath { return trustedTool{owner: t, role: gcovRole} }

func (t *Toolset) Tools() []coveragerun.TrustedPath {
	return []coveragerun.TrustedPath{t.CCompiler(), t.CXXCompiler(), t.GCov()}
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

func (t *Toolset) ClaimOwnership() (coverageplatform.OwnershipClaim, error) {
	if t == nil {
		return nil, ErrInvalidToolset
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.claimed || t.compiler.file == nil || t.cxxCompiler.file == nil || t.gcov.file == nil {
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
	for _, role := range []toolRole{compilerRole, cxxCompilerRole, gcovRole} {
		if err := t.verifyToolLocked(role); err != nil {
			return err
		}
	}
	return nil
}

func (t *Toolset) verifyToolLocked(role toolRole) error {
	if t == nil {
		return ErrInvalidToolset
	}
	if err := verifyPinnedTool(t.tool(role)); err != nil {
		return errors.Join(ErrInvalidToolset, err)
	}
	return nil
}

func (t *Toolset) tool(role toolRole) *pinnedTool {
	switch role {
	case compilerRole:
		return &t.compiler
	case cxxCompilerRole:
		return &t.cxxCompiler
	default:
		return &t.gcov
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
		for _, tool := range []*pinnedTool{&t.gcov, &t.cxxCompiler, &t.compiler} {
			if tool.file != nil {
				result = errors.Join(result, tool.file.Close())
				tool.file = nil
				tool.info = nil
			}
		}
		t.closeErr = result
	})
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeErr
}

var _ coverageplatform.Toolset = (*Toolset)(nil)
