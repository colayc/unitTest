package coveragerun

import (
	"errors"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

func TestResolveCollectorSupportsTheThreeApprovedToolchains(t *testing.T) {
	tests := []struct {
		name      string
		toolchain coveragedomain.ToolchainSnapshot
		profile   ProfileFormat
		merge     MergeStrategy
	}{
		{
			name: "windows clang-cl llvm-cov",
			toolchain: coveragedomain.ToolchainSnapshot{
				Platform:     coveragedomain.PlatformWindows,
				Architecture: coveragedomain.ArchitectureX64,
				Compiler:     coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyClangCL, Version: "18.1.8"},
				Driver:       coveragedomain.DriverSnapshot{Name: coveragedomain.DriverLLVMCov, Version: "18.1.8"},
				Collector:    coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorLLVMCov, Version: "18.1.8"},
			},
			profile: ProfileFormatLLVMRaw,
			merge:   MergeStrategyLLVMProfdata,
		},
		{
			name: "linux gcc gcovr",
			toolchain: coveragedomain.ToolchainSnapshot{
				Platform:     coveragedomain.PlatformLinux,
				Architecture: coveragedomain.ArchitectureX64,
				Compiler:     coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyGCC, Version: "15.1.0"},
				Driver:       coveragedomain.DriverSnapshot{Name: coveragedomain.DriverGCov, Version: "15.1.0"},
				Collector:    coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorGCovr, Version: "8.6"},
			},
			profile: ProfileFormatGCov,
			merge:   MergeStrategyGCovr,
		},
		{
			name: "linux clang llvm-cov",
			toolchain: coveragedomain.ToolchainSnapshot{
				Platform:     coveragedomain.PlatformLinux,
				Architecture: coveragedomain.ArchitectureX64,
				Compiler:     coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyClang, Version: "18.1.8"},
				Driver:       coveragedomain.DriverSnapshot{Name: coveragedomain.DriverLLVMCov, Version: "18.1.8"},
				Collector:    coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorLLVMCov, Version: "18.1.8"},
			},
			profile: ProfileFormatLLVMRaw,
			merge:   MergeStrategyLLVMProfdata,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec, err := ResolveCollector(test.toolchain)
			if err != nil {
				t.Fatal(err)
			}
			if spec.ProfileFormat != test.profile || spec.MergeStrategy != test.merge {
				t.Fatalf("spec = %#v", spec)
			}
		})
	}
}

func TestResolveCollectorRejectsUnsupportedOrIncompleteToolchains(t *testing.T) {
	base := coveragedomain.ToolchainSnapshot{
		Platform:     coveragedomain.PlatformLinux,
		Architecture: coveragedomain.ArchitectureX64,
		Compiler:     coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyGCC, Version: "15.1.0"},
		Driver:       coveragedomain.DriverSnapshot{Name: coveragedomain.DriverGCov, Version: "15.1.0"},
		Collector:    coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorGCovr, Version: "8.6"},
	}
	for name, input := range map[string]coveragedomain.ToolchainSnapshot{
		"windows gcc": func() coveragedomain.ToolchainSnapshot {
			value := base
			value.Platform = coveragedomain.PlatformWindows
			return value
		}(),
		"cross collector": func() coveragedomain.ToolchainSnapshot {
			value := base
			value.Collector.Name = coveragedomain.CollectorLLVMCov
			return value
		}(),
		"missing compiler version": func() coveragedomain.ToolchainSnapshot { value := base; value.Compiler.Version = ""; return value }(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ResolveCollector(input); !errors.Is(err, ErrUnsupportedToolchain) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
