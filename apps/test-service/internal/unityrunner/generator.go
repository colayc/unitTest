package unityrunner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const CurrentGeneratorVersion = "1.0.0"

var (
	ErrInvalidGenerateInput = errors.New("invalid Unity runner generation input")
	ErrGenerationLimit      = errors.New("Unity runner generation limit exceeded")

	generatorVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
)

type GenerateLimits struct {
	MaxCases         int
	MaxManifestBytes int
	MaxRunnerBytes   int
}

func DefaultGenerateLimits() GenerateLimits {
	return GenerateLimits{
		MaxCases:         50_000,
		MaxManifestBytes: 8 * 1024 * 1024,
		MaxRunnerBytes:   64 * 1024 * 1024,
	}
}

func (limits GenerateLimits) Validate() error {
	if limits.MaxCases <= 0 || limits.MaxManifestBytes <= 0 || limits.MaxRunnerBytes <= 0 {
		return ErrInvalidGenerateInput
	}
	return nil
}

type GenerateInput struct {
	Manifest         Manifest
	GeneratorVersion string
	Limits           GenerateLimits
}

func Generate(input GenerateInput) (runnerC []byte, manifestJSON []byte, err error) {
	limits := input.Limits
	if limits == (GenerateLimits{}) {
		limits = DefaultGenerateLimits()
	}
	if err := limits.Validate(); err != nil {
		return nil, nil, err
	}
	if !validGeneratorVersion(input.GeneratorVersion) {
		return nil, nil, fmt.Errorf("%w: malformed generator version", ErrInvalidGenerateInput)
	}
	if len(input.Manifest.Cases) > limits.MaxCases {
		return nil, nil, fmt.Errorf(
			"%w: case count %d exceeds %d",
			ErrGenerationLimit, len(input.Manifest.Cases), limits.MaxCases,
		)
	}
	if !manifestRawSizeWithin(input.Manifest, limits.MaxManifestBytes) {
		return nil, nil, fmt.Errorf(
			"%w: manifest content exceeds %d bytes before encoding",
			ErrGenerationLimit, limits.MaxManifestBytes,
		)
	}

	manifest := cloneManifest(input.Manifest)
	if err := manifest.Verify(); err != nil {
		return nil, nil, err
	}
	if manifest.GeneratorVersion != "" && manifest.GeneratorVersion != input.GeneratorVersion {
		return nil, nil, fmt.Errorf(
			"%w: manifest generator version %q conflicts with %q",
			ErrInvalidGenerateInput, manifest.GeneratorVersion, input.GeneratorVersion,
		)
	}
	if err := validateGeneratorBindings(manifest); err != nil {
		return nil, nil, err
	}

	manifest.GeneratorVersion = input.GeneratorVersion
	manifest, err = sealManifest(manifest)
	if err != nil {
		return nil, nil, err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("%w: encode manifest: %v", ErrInvalidGenerateInput, err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > limits.MaxManifestBytes {
		return nil, nil, fmt.Errorf(
			"%w: manifest size %d exceeds %d bytes",
			ErrGenerationLimit, len(encoded), limits.MaxManifestBytes,
		)
	}

	runner, err := renderRunner(manifest)
	if err != nil {
		return nil, nil, err
	}
	if len(runner) > limits.MaxRunnerBytes {
		return nil, nil, fmt.Errorf(
			"%w: runner size %d exceeds %d bytes",
			ErrGenerationLimit, len(runner), limits.MaxRunnerBytes,
		)
	}
	return runner, encoded, nil
}

func manifestRawSizeWithin(manifest Manifest, limit int) bool {
	total := 0
	add := func(value string) bool {
		if len(value) > limit-total {
			return false
		}
		total += len(value)
		return true
	}
	if limit <= 0 || !add(manifest.Version) || !add(manifest.GeneratorVersion) ||
		!add(manifest.SHA256) {
		return false
	}
	for _, source := range manifest.Sources {
		if !add(source) {
			return false
		}
	}
	for _, location := range []*SourceLocation{manifest.SetUp, manifest.TearDown} {
		if location != nil && !add(location.Path) {
			return false
		}
	}
	for _, testCase := range manifest.Cases {
		if !add(testCase.Name) || !add(testCase.Identity) || !add(testCase.Parameters) ||
			!add(testCase.Location.Path) {
			return false
		}
		for _, argument := range testCase.Arguments {
			if !add(argument) {
				return false
			}
		}
	}
	return true
}

func validGeneratorVersion(version string) bool {
	return len(version) > 0 && len(version) <= 128 &&
		validText(version) && generatorVersionPattern.MatchString(version)
}

func cloneManifest(manifest Manifest) Manifest {
	result := manifest
	result.Sources = append([]string(nil), manifest.Sources...)
	result.Cases = make([]TestCase, len(manifest.Cases))
	for index, testCase := range manifest.Cases {
		result.Cases[index] = testCase
		result.Cases[index].Arguments = append([]string(nil), testCase.Arguments...)
	}
	if manifest.SetUp != nil {
		location := *manifest.SetUp
		result.SetUp = &location
	}
	if manifest.TearDown != nil {
		location := *manifest.TearDown
		result.TearDown = &location
	}
	return result
}

func validateGeneratorBindings(manifest Manifest) error {
	type functionBinding struct {
		parameters string
		location   SourceLocation
	}
	functions := make(map[string]functionBinding)
	for _, testCase := range manifest.Cases {
		wantIdentity := testCase.Name
		if len(testCase.Arguments) > 0 {
			wantIdentity += "(" + strings.Join(testCase.Arguments, ", ") + ")"
		}
		if testCase.Identity != wantIdentity {
			return fmt.Errorf(
				"%w: case %q does not match canonical identity %q",
				ErrInvalidGenerateInput, testCase.Identity, wantIdentity,
			)
		}
		if len(testCase.Arguments) == 0 && testCase.Parameters != "void" {
			return fmt.Errorf(
				"%w: non-parameterized case %q has parameters",
				ErrInvalidGenerateInput, testCase.Identity,
			)
		}
		if strings.ContainsAny(testCase.Parameters, "\r\n\x00#;{}") {
			return fmt.Errorf(
				"%w: case %q has an unsafe parameter declaration",
				ErrInvalidGenerateInput, testCase.Identity,
			)
		}
		previous, exists := functions[testCase.Name]
		if exists && (previous.parameters != testCase.Parameters || previous.location != testCase.Location) {
			return fmt.Errorf(
				"%w: function %q has conflicting declarations",
				ErrInvalidGenerateInput, testCase.Name,
			)
		}
		functions[testCase.Name] = functionBinding{
			parameters: testCase.Parameters,
			location:   testCase.Location,
		}
	}
	return nil
}

func compactArgumentsJSON(arguments []string) (string, error) {
	if arguments == nil {
		arguments = []string{}
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return "", err
	}
	if !json.Valid(encoded) || bytes.IndexByte(encoded, '\n') >= 0 {
		return "", errors.New("arguments did not encode as one JSON value")
	}
	return string(encoded), nil
}
