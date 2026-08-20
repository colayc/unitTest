//go:build windows

package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"unit-test-ide.local/test-service/internal/coveragellvm"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/toolchain"
)

const preflightTimeout = 60 * time.Second

type preflightReport struct {
	SchemaVersion int    `json:"schemaVersion"`
	Platform      string `json:"platform"`
	Architecture  string `json:"architecture"`
	Status        string `json:"status"`
	Version       string `json:"version,omitempty"`
}

func main() {
	if err := run(os.Stdout); err != nil {
		_, _ = io.WriteString(os.Stderr, "coverage toolset preflight failed\n")
		os.Exit(1)
	}
}

func run(stdout io.Writer) error {
	report := preflightReport{
		SchemaVersion: 1,
		Platform:      "windows",
		Architecture:  "x64",
		Status:        "unavailable",
	}
	registry, err := toolchain.NewRegistry(
		toolchain.NewWindowsAdapters(probe.NewRunner(), nil)...,
	)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), preflightTimeout)
		instances, _ := registry.Discover(ctx)
		cancel()
		for _, instance := range instances {
			if instance.Family != toolchain.FamilyClangCL {
				continue
			}
			verified, verifyErr := coveragellvm.PinToolset(instance)
			if verifyErr != nil {
				continue
			}
			if verifyErr = verified.Verify(); verifyErr == nil {
				report.Status = "verified"
				report.Version = verified.Version()
			}
			closeErr := verified.Close()
			if verifyErr == nil && closeErr != nil {
				report.Status = "unavailable"
				report.Version = ""
			}
			if report.Status == "verified" {
				break
			}
		}
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(report)
}
