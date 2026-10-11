package testgenanalysis

import (
	"context"
	"runtime"
	"time"

	"unit-test-ide.local/test-service/internal/probe"
)

func integerABIBindingFor(request AnalysisRequest, manifest string, args []string) (integerABIBinding, error) {
	if runtime.GOARCH != "amd64" {
		return integerABIBinding{}, errIntegerABI
	}
	platform := ""
	switch runtime.GOOS {
	case "windows":
		platform = "windows-x64"
	case "linux":
		platform = "linux-x64"
	default:
		return integerABIBinding{}, errIntegerABI
	}
	standard := ""
	for _, arg := range args {
		if languageModes[arg] {
			if standard != "" {
				return integerABIBinding{}, errIntegerABI
			}
			standard = arg
		}
	}
	binding := integerABIBinding{manifest: manifest, compile: request.CompileSnapshotDigest, standard: standard, platform: platform}
	if !binding.valid() {
		return integerABIBinding{}, errIntegerABI
	}
	return binding, nil
}
func (a Analyzer) probeIntegerABI(ctx context.Context, request AnalysisRequest, binding integerABIBinding) (integerABIFacts, error) {
	if err := ctx.Err(); err != nil {
		return integerABIFacts{}, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return integerABIFacts{}, errIntegerABI
	}
	language := "c"
	if binding.standard == "-std=c++17" || binding.standard == "-std=c++20" {
		language = "c++"
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return integerABIFacts{}, context.DeadlineExceeded
	}
	result, err := a.runner.Run(ctx, probe.Spec{
		Executable: a.bundle.ClangPath(), Dir: request.WorkspaceRoot, Env: []string{},
		Args: []string{"--no-default-config", "-dM", "-E", "-x", language, binding.standard,
			"-nostdinc", "-nostdinc++", "-resource-dir", a.bundle.ResourceDir(), "-"},
		Timeout: remaining, MaxOutput: maxIntegerABIBytes,
	})
	if ctx.Err() != nil {
		return integerABIFacts{}, ctx.Err()
	}
	if err != nil || result.ExitCode != 0 {
		return integerABIFacts{}, errIntegerABI
	}
	return decodeIntegerABIFacts(result.Stdout, binding)
}
