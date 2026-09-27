package testgenanalysis

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/testgenbundle"
)

const maxSourceBytes = 1 << 20
const maxASTBytes = 16 << 20
const maxArguments = 64

// AnalysisRequest is service-owned; it is never decoded from a protocol
// envelope. It binds a source file to the trusted compile snapshot.
type AnalysisRequest struct {
	WorkspaceRoot         string
	SourceRelative        string
	SourceDigest          string
	CompileSnapshotDigest string
	Arguments             []string
	Timeout               time.Duration
}

type verifiedBundle interface {
	ClangPath() string
	ResourceDir() string
	ManifestSHA256() string
	Verify() error
}

type Analyzer struct {
	bundle verifiedBundle
	runner probe.Runner
}

func NewAnalyzer(bundle *testgenbundle.Bundle) (*Analyzer, error) {
	if bundle == nil {
		return nil, errors.New("missing fixed Clang bundle")
	}
	if err := bundle.Verify(); err != nil {
		return nil, errors.New("fixed Clang bundle verification failed")
	}
	return &Analyzer{bundle: bundle, runner: probe.NewRunner()}, nil
}

func (a Analyzer) Analyze(ctx context.Context, request AnalysisRequest) (Program, error) {
	if err := ctx.Err(); err != nil {
		return Program{}, err
	}
	if a.bundle == nil || a.runner == nil || !validSHA(a.bundle.ManifestSHA256()) || !validSHA(request.SourceDigest) || !validSHA(request.CompileSnapshotDigest) || request.Timeout <= 0 || request.Timeout > 30*time.Second {
		return Program{}, errors.New("invalid analysis identity or budget")
	}
	if !filepath.IsAbs(request.WorkspaceRoot) || filepath.Clean(request.WorkspaceRoot) != request.WorkspaceRoot || !safeRelativeSource(request.SourceRelative) {
		return Program{}, errors.New("invalid trusted source root or URI")
	}
	if err := a.bundle.Verify(); err != nil {
		return Program{}, errors.New("fixed Clang bundle verification failed")
	}
	args, err := normalizedArguments(request.Arguments, request.WorkspaceRoot)
	if err != nil {
		return Program{}, err
	}
	sourcePath := filepath.Join(request.WorkspaceRoot, filepath.FromSlash(request.SourceRelative))
	if err := verifySource(sourcePath, request.WorkspaceRoot, request.SourceDigest); err != nil {
		return Program{}, err
	}
	args = append([]string{"-Xclang", "-ast-dump=json", "-fsyntax-only"}, args...)
	args = append(args, "-resource-dir", a.bundle.ResourceDir(), sourcePath)
	result, err := a.runner.Run(ctx, probe.Spec{Executable: a.bundle.ClangPath(), Args: args, Dir: request.WorkspaceRoot, Env: []string{}, Timeout: request.Timeout, MaxOutput: maxASTBytes})
	if err != nil {
		if ctx.Err() != nil {
			return Program{}, ctx.Err()
		}
		return Program{}, errors.New("fixed Clang analysis process failed")
	}
	if result.ExitCode != 0 {
		return Program{}, errors.New("fixed Clang rejected source")
	}
	if err := a.bundle.Verify(); err != nil {
		return Program{}, errors.New("fixed Clang bundle changed during analysis")
	}
	if err := verifySource(sourcePath, request.WorkspaceRoot, request.SourceDigest); err != nil {
		return Program{}, err
	}
	program, err := decodeAST(strings.NewReader(string(result.Stdout)), maxASTBytes, request.SourceDigest)
	if err != nil {
		return Program{}, errors.New("fixed Clang emitted unsupported AST")
	}
	return program, nil
}

func safeRelativeSource(v string) bool {
	if len(v) == 0 || len(v) > 1024 || strings.ContainsAny(v, "\\:\x00\n\r") || strings.HasPrefix(v, "/") {
		return false
	}
	for _, part := range strings.Split(v, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") {
			return false
		}
		for _, c := range part {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
				return false
			}
		}
	}
	return true
}
func verifySource(path, root, digest string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return errors.New("source snapshot is missing")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("source snapshot contains link")
		}
		if current == root {
			if !info.IsDir() {
				return errors.New("trusted root is not a directory")
			}
			break
		}
		if parent := filepath.Dir(current); parent == current {
			return errors.New("source escaped workspace")
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("source snapshot is unavailable")
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxSourceBytes {
		return errors.New("source snapshot exceeds budget")
	}
	bytes, err := io.ReadAll(io.LimitReader(file, maxSourceBytes+1))
	if err != nil || len(bytes) > maxSourceBytes {
		return errors.New("source snapshot exceeds budget")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || digestBytes(bytes) != digest {
		return errors.New("source snapshot is stale")
	}
	return nil
}
