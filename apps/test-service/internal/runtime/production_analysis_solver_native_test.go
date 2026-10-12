//go:build scalarabi_integration

package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	"unit-test-ide.local/test-service/internal/testgenbundle"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

func TestProductionAnalysisToSolverIntegerABI(t *testing.T) {
	root := os.Getenv("UNIT_TEST_IDE_SCALAR_ABI_BUNDLE_ROOT")
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		t.Fatal("approved bundle required")
	}
	bundle, err := testgenbundle.Open(root)
	if err != nil {
		t.Fatal("approved bundle verification failed")
	}
	analyzer, err := analysis.NewAnalyzer(bundle)
	if err != nil {
		t.Fatal("trusted analyzer unavailable")
	}
	_, ownFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("test repository identity unavailable")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(ownFile), "../../../.."))
	specs := []struct{ framework, file, header, standard string }{
		{"cpputest", "classifier.cpp", "classifier.hpp", "-std=c++17"},
		{"unity", "classifier.c", "classifier.h", "-std=c11"},
	}
	for _, spec := range specs {
		t.Run(spec.framework, func(t *testing.T) {
			fixture := filepath.Join(repository, "tools/service-probe/fixtures/test-generation", spec.framework)
			source, err := os.ReadFile(filepath.Join(fixture, "src", spec.file))
			if err != nil {
				t.Fatal("real seed missing")
			}
			header, err := os.ReadFile(filepath.Join(fixture, "include", spec.header))
			if err != nil {
				t.Fatal("real seed header missing")
			}
			work := scalarABIWorkspaceForTest(t, filepath.Dir(ownFile),
				[]string{"src/" + spec.file, "include/" + spec.header})
			if os.MkdirAll(filepath.Join(work, "src"), 0700) != nil || os.MkdirAll(filepath.Join(work, "include"), 0700) != nil {
				t.Fatal("isolated seed root unavailable")
			}
			if os.WriteFile(filepath.Join(work, "src", spec.file), source, 0600) != nil || os.WriteFile(filepath.Join(work, "include", spec.header), header, 0600) != nil {
				t.Fatal("isolated seed copy unavailable")
			}
			sourceSHA := sha256.Sum256(source)
			args := []string{spec.standard, "-Iinclude"}
			descriptor, err := json.Marshal(struct {
				Source string
				Args   []string
			}{hex.EncodeToString(sourceSHA[:]), args})
			if err != nil {
				t.Fatal("compile input descriptor unavailable")
			}
			compileSHA := sha256.Sum256(descriptor)
			request := analysis.AnalysisRequest{WorkspaceRoot: work, SourceRelative: "src/" + spec.file, SourceDigest: hex.EncodeToString(sourceSHA[:]), CompileSnapshotDigest: hex.EncodeToString(compileSHA[:]), Arguments: args, Timeout: 30 * time.Second}
			program, err := analyzer.Analyze(context.Background(), request)
			if err != nil {
				t.Fatalf("stage=analyze_error category=%s functions=%s diagnostics=%s", scalarABIAnalysisErrorCategory(err), scalarABICountCategory(len(program.Functions)), scalarABICountCategory(len(program.Diagnostics)))
			}
			if len(program.Functions) != 1 {
				t.Fatalf("stage=analyze_function_count category=unexpected_count functions=%s diagnostics=%s reasons=%v", scalarABICountCategory(len(program.Functions)), scalarABICountCategory(len(program.Diagnostics)), scalarABIDiagnosticCategories(program.Diagnostics))
			}
			if len(program.Diagnostics) != 0 {
				t.Fatalf("stage=analyze_diagnostics category=diagnostics_present functions=%s diagnostics=%s reasons=%v", scalarABICountCategory(len(program.Functions)), scalarABICountCategory(len(program.Diagnostics)), scalarABIDiagnosticCategories(program.Diagnostics))
			}
			f := program.Functions[0]
			if f.Name != "classify" || len(f.Parameters) != 1 || f.LinkageName == "" {
				t.Fatal("real function identity incomplete")
			}
			gap := solver.CoverageGap{Kind: solver.GapFunction, CompileSnapshot: request.CompileSnapshotDigest, AnalyzerVersion: "clang-ast-ir-v1"}
			selected, err := bindProductionFunctions(program, []generationFunctionTarget{{linkageName: f.LinkageName, gap: gap}})
			if err != nil || len(selected) != 1 || selected[0].gap.SymbolID != f.SymbolID {
				t.Fatal("real function binding rejected")
			}
			vectors, diagnostics, err := (solver.Solver{}).Solve(context.Background(), program, selected[0].gap, solver.Budget{WallTime: 5 * time.Second, CandidateLimit: 32, MemoryBytes: 64 << 20, Concurrency: 1})
			if err != nil || len(vectors) == 0 || len(diagnostics) != 0 {
				t.Fatal("real analyzed integers cannot be solved")
			}
			if f.ReturnType.Kind != analysis.TypeInteger || f.ReturnType.BitWidth != 32 || !f.ReturnType.Signed || !f.ReturnType.Proven || f.Parameters[0].Type.Kind != analysis.TypeInteger || f.Parameters[0].Type.BitWidth != 32 || !f.Parameters[0].Type.Signed || !f.Parameters[0].Type.Proven || len(program.Digest) != 64 {
				t.Fatal("real integer proof incomplete")
			}
			minimum := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), uint(f.Parameters[0].Type.BitWidth-1)))
			maximum := new(big.Int).Sub(new(big.Int).Neg(new(big.Int).Set(minimum)), big.NewInt(1))
			for _, vector := range vectors {
				if len(vector.Inputs) != 1 || vector.Inputs[0].Name != f.Parameters[0].Name || vector.Inputs[0].Value.Kind != analysis.TypeInteger {
					t.Fatal("invalid real input shape")
				}
				value, ok := new(big.Int).SetString(vector.Inputs[0].Value.Integer, 10)
				if !ok || value.Cmp(minimum) < 0 || value.Cmp(maximum) > 0 {
					t.Fatal("real input exceeds proven range")
				}
			}
		})
	}
}

var errScalarABIWorkspace = errors.New("scalar ABI test workspace refused")

type scalarABIWorkspaceLease struct {
	path, parent             string
	identity, parentIdentity os.FileInfo
}

func scalarABIWorkspaceWithinLimit(path string) bool {
	return len(utf16.Encode([]rune(path))) <= 240
}

func scalarABIWorkspaceCanonical(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	if goruntime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(resolved), path)
	}
	return filepath.Clean(resolved) == path
}

func scalarABIPackageParent() (string, error) {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		return "", errScalarABIWorkspace
	}
	parent := filepath.Dir(file)
	cwd, err := os.Getwd()
	if err != nil || cwd != parent || !scalarABIWorkspaceCanonical(parent) {
		return "", errScalarABIWorkspace
	}
	return parent, nil
}

func scalarABINewWorkspace(parent string, relatives []string) (scalarABIWorkspaceLease, error) {
	var lease scalarABIWorkspaceLease
	expected, err := scalarABIPackageParent()
	if err != nil || parent != expected || !scalarABIWorkspaceWithinLimit(parent) {
		return lease, errScalarABIWorkspace
	}
	for _, relative := range relatives {
		local := filepath.FromSlash(relative)
		if !filepath.IsLocal(local) || filepath.Clean(local) != local {
			return lease, errScalarABIWorkspace
		}
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || !os.SameFile(parentInfo, parentInfo) {
		return lease, errScalarABIWorkspace
	}
	path, err := os.MkdirTemp(parent, ".abi-")
	if err != nil {
		return lease, errScalarABIWorkspace
	}
	lease = scalarABIWorkspaceLease{path: path, parent: parent, parentIdentity: parentInfo}
	info, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, info) {
		return lease, errScalarABIWorkspace
	}
	lease.identity = info
	if filepath.Dir(path) != parent || !scalarABIWorkspaceCanonical(path) || !scalarABIWorkspaceWithinLimit(path) {
		return lease, errScalarABIWorkspace
	}
	for _, relative := range relatives {
		if !scalarABIWorkspaceWithinLimit(filepath.Join(path, filepath.FromSlash(relative))) {
			return lease, errScalarABIWorkspace
		}
	}
	return lease, nil
}

func (lease *scalarABIWorkspaceLease) Close() error {
	if lease.path == "" {
		return nil
	}
	parent, err := scalarABIPackageParent()
	if err != nil || parent != lease.parent || filepath.Dir(lease.path) != parent || !strings.HasPrefix(filepath.Base(lease.path), ".abi-") {
		return errScalarABIWorkspace
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || lease.parentIdentity == nil || !os.SameFile(parentInfo, lease.parentIdentity) {
		return errScalarABIWorkspace
	}
	info, err := os.Lstat(lease.path)
	if err != nil || lease.identity == nil || !os.SameFile(info, lease.identity) || !scalarABIWorkspaceCanonical(lease.path) {
		return errScalarABIWorkspace
	}
	if os.RemoveAll(lease.path) != nil {
		return errScalarABIWorkspace
	}
	if _, err := os.Lstat(lease.path); !os.IsNotExist(err) {
		return errScalarABIWorkspace
	}
	lease.path = ""
	return nil
}

func scalarABIWorkspaceForTest(t *testing.T, parent string, relatives []string) string {
	t.Helper()
	if goruntime.GOOS != "windows" {
		return t.TempDir()
	}
	lease, err := scalarABINewWorkspace(parent, relatives)
	if lease.path != "" {
		t.Cleanup(func() {
			if lease.Close() != nil {
				t.Error("scalar_abi_workspace_cleanup=failed")
			} else {
				t.Log("scalar_abi_workspace_cleanup=ok")
			}
		})
	}
	if err != nil {
		t.Fatal("scalar_abi_workspace_setup=failed")
	}
	if len(relatives) == 2 {
		t.Logf("scalar_abi_workspace_lengths=%d/%d/%d", len(utf16.Encode([]rune(lease.path))),
			len(utf16.Encode([]rune(filepath.Join(lease.path, filepath.FromSlash(relatives[0]))))),
			len(utf16.Encode([]rune(filepath.Join(lease.path, filepath.FromSlash(relatives[1]))))))
	}
	return lease.path
}

func TestScalarABIWorkspaceParentBoundary(t *testing.T) {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("workspace_parent_identity_failed")
	}
	parent := filepath.Dir(file)
	work := scalarABIWorkspaceForTest(t, parent, []string{"src/classifier.cpp", "include/classifier.hpp"})
	if goruntime.GOOS == "windows" && filepath.Dir(work) != parent {
		t.Fatal("workspace_parent_boundary")
	}
}

func TestScalarABIWorkspaceSafetyLifecycle(t *testing.T) {
	parent, err := scalarABIPackageParent()
	if err != nil {
		t.Fatal("workspace_test_parent_failed")
	}
	beforeCWD, err := os.Getwd()
	if err != nil {
		t.Fatal("workspace_test_cwd_failed")
	}
	before := map[string]string{}
	for _, key := range []string{"TEMP", "TMP", "GOTMPDIR"} {
		before[key] = os.Getenv(key)
	}
	relatives := []string{"src/classifier.cpp", "include/classifier.hpp"}
	makeLease := func(t *testing.T) *scalarABIWorkspaceLease {
		t.Helper()
		lease, err := scalarABINewWorkspace(parent, relatives)
		if lease.path != "" {
			t.Cleanup(func() {
				if lease.Close() != nil {
					t.Error("workspace_test_cleanup_failed")
				}
			})
		}
		if err != nil {
			t.Fatal("workspace_valid_directory_refused")
		}
		return &lease
	}
	t.Run("bounds", func(t *testing.T) {
		for _, value := range []string{strings.Repeat("a", 240), strings.Repeat("😀", 120)} {
			if !scalarABIWorkspaceWithinLimit(value) {
				t.Fatal("workspace_limit_rejected_boundary")
			}
		}
		for _, value := range []string{strings.Repeat("a", 241), strings.Repeat("😀", 121)} {
			if scalarABIWorkspaceWithinLimit(value) {
				t.Fatal("workspace_limit_accepted_overflow")
			}
		}
	})
	t.Run("isolation_and_bytes", func(t *testing.T) {
		first, second := makeLease(t), makeLease(t)
		if first.path == second.path || filepath.Dir(first.path) != parent || filepath.Dir(second.path) != parent {
			t.Fatal("workspace_not_isolated")
		}
		for _, lease := range []*scalarABIWorkspaceLease{first, second} {
			if !scalarABIWorkspaceWithinLimit(lease.path) {
				t.Fatal("workspace_root_too_long")
			}
			for _, relative := range relatives {
				path := filepath.Join(lease.path, filepath.FromSlash(relative))
				if !scalarABIWorkspaceWithinLimit(path) {
					t.Fatal("workspace_input_too_long")
				}
				if os.MkdirAll(filepath.Dir(path), 0700) != nil {
					t.Fatal("workspace_input_parent_failed")
				}
				input := []byte("exact test input\n")
				if os.WriteFile(path, input, 0600) != nil {
					t.Fatal("workspace_input_copy_failed")
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, input) {
					t.Fatal("workspace_input_changed")
				}
			}
			path := lease.path
			if lease.Close() != nil {
				t.Fatal("workspace_normal_cleanup_failed")
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("workspace_normal_cleanup_leaked")
			}
		}
	})
	t.Run("unsafe_parent_and_relative", func(t *testing.T) {
		for _, value := range []string{"relative", parent + string(filepath.Separator) + ".", parent + strings.Repeat("a", 241)} {
			lease, err := scalarABINewWorkspace(value, relatives)
			if lease.path != "" {
				t.Cleanup(func() {
					if lease.Close() != nil {
						t.Error("workspace_rejection_cleanup_failed")
					}
				})
			}
			if err == nil {
				t.Fatal("workspace_unsafe_parent_accepted")
			}
		}
		lease, err := scalarABINewWorkspace(parent, []string{"../escape"})
		if lease.path != "" {
			t.Cleanup(func() {
				if lease.Close() != nil {
					t.Error("workspace_rejection_cleanup_failed")
				}
			})
		}
		if err == nil {
			t.Fatal("workspace_relative_escape_accepted")
		}
		oversized, err := scalarABINewWorkspace(parent, []string{strings.Repeat("a", 241)})
		if oversized.path != "" {
			t.Cleanup(func() {
				if oversized.Close() != nil {
					t.Error("workspace_rejection_cleanup_failed")
				}
			})
		}
		if err == nil || oversized.path == "" || oversized.Close() != nil {
			t.Fatal("workspace_postcreate_rejection_failed")
		}
	})
	t.Run("failure_cleanup", func(t *testing.T) {
		lease := makeLease(t)
		path := lease.path
		cleanupFailed := false
		failure := func() error {
			defer func() { cleanupFailed = lease.Close() != nil }()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx.Err()
		}()
		if !errors.Is(failure, context.Canceled) || cleanupFailed {
			t.Fatal("workspace_failure_cleanup_failed")
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("workspace_failure_cleanup_leaked")
		}
	})
	t.Run("identity_change", func(t *testing.T) {
		first, second := makeLease(t), makeLease(t)
		originalPath := first.path
		moved := originalPath + ".saved"
		if filepath.Dir(moved) != parent {
			t.Fatal("workspace_move_escaped_parent")
		}
		if _, err := os.Lstat(moved); !os.IsNotExist(err) {
			t.Fatal("workspace_move_target_exists")
		}
		if os.Rename(first.path, moved) != nil {
			t.Fatal("workspace_identity_move_failed")
		}
		first.path = moved
		if filepath.Dir(second.path) != parent || filepath.Dir(originalPath) != parent {
			t.Fatal("workspace_replacement_escaped_parent")
		}
		if _, err := os.Lstat(originalPath); !os.IsNotExist(err) {
			t.Fatal("workspace_replacement_target_exists")
		}
		if os.Rename(second.path, originalPath) != nil {
			t.Fatal("workspace_identity_replacement_failed")
		}
		second.path = originalPath
		stale := *first
		stale.path = originalPath
		if stale.Close() == nil {
			t.Fatal("workspace_changed_identity_deleted")
		}
		current, err := os.Lstat(second.path)
		if err != nil || !os.SameFile(current, second.identity) {
			t.Fatal("workspace_replacement_changed")
		}
	})
	afterCWD, err := os.Getwd()
	if err != nil || afterCWD != beforeCWD {
		t.Fatal("workspace_global_cwd_changed")
	}
	for key, value := range before {
		if os.Getenv(key) != value {
			t.Fatal("workspace_global_environment_changed")
		}
	}
}

// These helpers project only closed categories, never compiler or IR content.
func scalarABICountCategory(count int) string {
	if count == 0 {
		return "zero"
	}
	if count == 1 {
		return "one"
	}
	return "other"
}

func scalarABIAnalysisErrorCategory(err error) string {
	switch err.Error() {
	case "context canceled":
		return "canceled"
	case "context deadline exceeded":
		return "deadline_exceeded"
	case "invalid analysis identity or budget":
		return "invalid_identity_budget"
	case "invalid trusted source root or URI":
		return "invalid_source_identity"
	case "fixed Clang bundle verification failed":
		return "bundle_verification_failed"
	case "compile argument budget exceeded":
		return "argument_budget_exceeded"
	case "unsafe compile argument":
		return "unsafe_argument"
	case "duplicate language mode":
		return "duplicate_standard"
	case "unsafe include root":
		return "unsafe_include_root"
	case "unsupported compile argument":
		return "unsupported_argument"
	case "missing language mode":
		return "missing_standard"
	case "untrusted include root":
		return "untrusted_include_root"
	case "include root escaped workspace":
		return "include_root_escape"
	case "source-directed includes or macros are not supported by fixed analysis":
		return "source_directive"
	case "fixed Clang analysis process failed":
		return "analysis_process_failed"
	case "fixed Clang rejected source":
		return "compiler_rejected_source"
	case "fixed Clang bundle changed during analysis":
		return "bundle_changed"
	case "source snapshot changed during analysis":
		return "source_changed"
	case "fixed Clang emitted unsupported AST":
		return "unsupported_ast"
	case "fixed Clang source range is incomplete":
		return "incomplete_source_range"
	case "source snapshot is missing":
		return "source_missing"
	case "source snapshot contains link":
		return "source_link"
	case "trusted root is not a directory":
		return "root_not_directory"
	case "source escaped workspace":
		return "source_escape"
	case "source snapshot is unavailable":
		return "source_unavailable"
	case "source snapshot exceeds budget":
		return "source_budget_exceeded"
	case "source snapshot is stale":
		return "source_stale"
	default:
		return "unknown_error"
	}
}

func scalarABIDiagnosticCategories(diagnostics []analysis.Diagnostic) []string {
	seen := map[string]bool{}
	for _, diagnostic := range diagnostics {
		category := "unknown_reason"
		switch diagnostic.Reason {
		case analysis.ReasonNone, analysis.ReasonUnknownCall, analysis.ReasonExternalCall,
			analysis.ReasonUnsupportedSyntax, analysis.ReasonUnsupportedType, analysis.ReasonUnboundedLoop,
			analysis.ReasonRecursion, analysis.ReasonMacroExpansion, analysis.ReasonIncompleteAST, analysis.ReasonTemporaryIO:
			category = string(diagnostic.Reason)
		}
		seen[category] = true
	}
	var categories []string
	for _, category := range []string{"none", "unknown-call", "external-call", "unsupported-syntax", "unsupported-type", "unbounded-loop", "recursion", "macro-expansion", "incomplete-ast", "temporary-io", "unknown_reason"} {
		if seen[category] {
			categories = append(categories, category)
		}
	}
	return categories
}
