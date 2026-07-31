package ctest

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/workspace"
)

var ErrInvalidDescriptor = errors.New("invalid CTest execution descriptor")

type ExecutionDescriptor struct {
	LogicalName        string
	TestDirectory      string
	Configuration      string
	TargetID           string
	Executable         cmake.FingerprintFile
	Arguments          []string
	WorkingDirectory   string
	Environment        []EnvironmentEntry
	EnvironmentChanges []EnvironmentModification
	TimeoutSeconds     *float64
	Labels             []string
	Disabled           bool
	SkipReturnCode     *int
	Compatibility      Compatibility
	Blocked            bool
	BlockedReason      Reason
}

type ArgumentConflict func([]string) bool

func BuildDescriptor(
	container RawTest,
	profile cmake.BuildProfile,
	targets []cmake.Target,
) (ExecutionDescriptor, error) {
	if container.Name == "" || len(container.Command) == 0 || container.Command[0] == "" ||
		profile.ID == "" || profile.ProjectID == "" || profile.BinaryDir == "" {
		return ExecutionDescriptor{}, ErrInvalidDescriptor
	}
	settings, compatibility := ClassifyProperties(container.Properties)
	result := ExecutionDescriptor{
		LogicalName:        container.Name,
		TestDirectory:      profile.BinaryDir,
		Configuration:      container.Config,
		Arguments:          append([]string(nil), container.Command[1:]...),
		Environment:        append([]EnvironmentEntry(nil), settings.Environment...),
		EnvironmentChanges: append([]EnvironmentModification(nil), settings.EnvironmentModifications...),
		TimeoutSeconds:     cloneFloat(settings.TimeoutSeconds),
		Labels:             append([]string(nil), settings.Labels...),
		Disabled:           settings.Disabled,
		SkipReturnCode:     cloneInt(settings.SkipReturnCode),
		Compatibility:      compatibility,
	}
	expectedConfiguration := container.Config
	if expectedConfiguration == "" {
		expectedConfiguration = profile.Configuration
		result.Configuration = expectedConfiguration
	}
	if container.Config != "" && profile.Configuration != "" &&
		container.Config != profile.Configuration {
		addReason(&result.Compatibility, ReasonConfigurationMismatch)
	}

	command := container.Command[0]
	var matched *cmake.Target
	matchingArtifact := false
	for index := range targets {
		target := &targets[index]
		if target.Type != "EXECUTABLE" || !targetListsPath(*target, command) {
			continue
		}
		matchingArtifact = true
		if target.Configuration != expectedConfiguration {
			continue
		}
		matched = target
		break
	}
	if matched == nil {
		if matchingArtifact {
			addReason(&result.Compatibility, ReasonConfigurationMismatch)
		} else {
			addReason(&result.Compatibility, ReasonCommandNotTarget)
		}
		if _, ok := resolveAllowedPath(command, false, profile, targets); !ok {
			blockDescriptor(&result, ReasonExternalCommand)
		}
	} else {
		state, err := cmake.SnapshotTargetArtifact(profile, *matched, command)
		if err != nil {
			addReason(&result.Compatibility, ReasonUnsafeExecutable)
			blockDescriptor(&result, ReasonUnsafeExecutable)
		} else {
			result.TargetID = matched.ID
			result.Executable = state
		}
	}

	if settings.WorkingDirectory == "" {
		addReason(&result.Compatibility, ReasonMissingWorkingDirectory)
	} else if resolved, ok := resolveAllowedPath(
		settings.WorkingDirectory,
		true,
		profile,
		targets,
	); !ok {
		addReason(&result.Compatibility, ReasonExternalWorkingDirectory)
		blockDescriptor(&result, ReasonExternalWorkingDirectory)
	} else {
		result.WorkingDirectory = resolved
	}
	finalizeCompatibility(&result.Compatibility)
	return result, nil
}

func (descriptor ExecutionDescriptor) ValidateExecutable() error {
	return cmake.VerifyTargetArtifact(descriptor.Executable)
}

func (descriptor ExecutionDescriptor) CheckReservedArguments(
	conflict ArgumentConflict,
) Compatibility {
	result := Compatibility{
		CaseLevel: descriptor.Compatibility.CaseLevel,
		Reasons:   append([]Reason(nil), descriptor.Compatibility.Reasons...),
		RunSerial: descriptor.Compatibility.RunSerial,
	}
	if conflict != nil && conflict(append([]string(nil), descriptor.Arguments...)) {
		addReason(&result, ReasonReservedArgument)
	}
	finalizeCompatibility(&result)
	return result
}

func blockDescriptor(descriptor *ExecutionDescriptor, reason Reason) {
	descriptor.Blocked = true
	if descriptor.BlockedReason == "" {
		descriptor.BlockedReason = reason
	}
	descriptor.Compatibility.CaseLevel = false
}

func targetListsPath(target cmake.Target, candidate string) bool {
	for _, artifact := range target.Artifacts {
		if equivalentPath(artifact, candidate) {
			return true
		}
	}
	return false
}

func equivalentPath(first, second string) bool {
	firstAbsolute, firstErr := filepath.Abs(first)
	secondAbsolute, secondErr := filepath.Abs(second)
	if firstErr != nil || secondErr != nil {
		return false
	}
	firstAbsolute = filepath.Clean(firstAbsolute)
	secondAbsolute = filepath.Clean(secondAbsolute)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(firstAbsolute, secondAbsolute)
	}
	return firstAbsolute == secondAbsolute
}

func resolveAllowedPath(
	value string,
	directory bool,
	profile cmake.BuildProfile,
	targets []cmake.Target,
) (string, bool) {
	if value == "" || strings.IndexByte(value, 0) >= 0 {
		return "", false
	}
	absolute, err := filepath.Abs(value)
	if err != nil || filepath.Clean(absolute) != absolute {
		return "", false
	}
	info, err := os.Stat(absolute)
	if err != nil || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return "", false
	}
	roots := []string{profile.BinaryDir}
	for _, target := range targets {
		roots = append(roots, target.ProjectSourceDir, target.ProjectBuildDir)
	}
	sort.Strings(roots)
	previous := ""
	for _, candidate := range roots {
		if candidate == "" || candidate == previous {
			continue
		}
		previous = candidate
		root, err := workspace.OpenRoot(candidate)
		if err == nil && root.Contains(absolute) {
			return absolute, true
		}
	}
	return "", false
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
