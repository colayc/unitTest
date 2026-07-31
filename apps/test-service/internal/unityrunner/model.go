package unityrunner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

const ManifestVersion = "utide.unity.manifest.v1"

var (
	ErrInvalidLimits     = errors.New("invalid Unity parser limits")
	ErrInvalidSourcePath = errors.New("invalid Unity source path")
	ErrInvalidSource     = errors.New("invalid Unity source")
	ErrUnsupportedSyntax = errors.New("unsupported Unity source syntax")
	ErrDuplicateIdentity = errors.New("duplicate Unity test identity")
	ErrLimitExceeded     = errors.New("Unity parser limit exceeded")
	ErrInvalidManifest   = errors.New("invalid Unity manifest")
	ErrManifestHash      = errors.New("Unity manifest hash mismatch")
)

type Limits struct {
	MaxSources            int
	MaxSourceBytes        int64
	MaxCases              int
	MaxCaseNameBytes      int
	MaxParameterBytes     int
	MaxParameterInstances int
}

func DefaultLimits() Limits {
	return Limits{
		MaxSources:            256,
		MaxSourceBytes:        4 * 1024 * 1024,
		MaxCases:              50_000,
		MaxCaseNameBytes:      512,
		MaxParameterBytes:     4 * 1024,
		MaxParameterInstances: 10_000,
	}
}

func (limits Limits) Validate() error {
	if limits.MaxSources <= 0 || limits.MaxSourceBytes <= 0 || limits.MaxCases <= 0 ||
		limits.MaxCaseNameBytes <= 0 || limits.MaxParameterBytes <= 0 ||
		limits.MaxParameterInstances <= 0 {
		return ErrInvalidLimits
	}
	return nil
}

type SourceLocation struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}

type TestCase struct {
	Name       string         `json:"name"`
	Identity   string         `json:"identity"`
	Parameters string         `json:"parameters"`
	Arguments  []string       `json:"arguments,omitempty"`
	Location   SourceLocation `json:"location"`
}

type Manifest struct {
	Version          string          `json:"version"`
	GeneratorVersion string          `json:"generatorVersion,omitempty"`
	SHA256           string          `json:"sha256"`
	Sources          []string        `json:"sources"`
	SetUp            *SourceLocation `json:"setUp,omitempty"`
	TearDown         *SourceLocation `json:"tearDown,omitempty"`
	Cases            []TestCase      `json:"cases"`
}

func (manifest Manifest) Verify() error {
	if manifest.Version != ManifestVersion {
		return fmt.Errorf("%w: unsupported version %q", ErrInvalidManifest, manifest.Version)
	}
	if len(manifest.SHA256) != sha256.Size*2 ||
		strings.ToLower(manifest.SHA256) != manifest.SHA256 {
		return fmt.Errorf("%w: malformed SHA-256", ErrInvalidManifest)
	}
	if _, err := hex.DecodeString(manifest.SHA256); err != nil {
		return fmt.Errorf("%w: malformed SHA-256", ErrInvalidManifest)
	}
	if err := validateManifestContent(manifest); err != nil {
		return err
	}
	actual, err := manifestDigest(manifest)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if actual != manifest.SHA256 {
		return fmt.Errorf("%w: got %s, want %s", ErrManifestHash, manifest.SHA256, actual)
	}
	return nil
}

func sealManifest(manifest Manifest) (Manifest, error) {
	manifest.Version = ManifestVersion
	manifest.SHA256 = ""
	if err := validateManifestContent(manifest); err != nil {
		return Manifest{}, err
	}
	digest, err := manifestDigest(manifest)
	if err != nil {
		return Manifest{}, err
	}
	manifest.SHA256 = digest
	return manifest, nil
}

func manifestDigest(manifest Manifest) (string, error) {
	payload := struct {
		Version          string          `json:"version"`
		GeneratorVersion string          `json:"generatorVersion,omitempty"`
		Sources          []string        `json:"sources"`
		SetUp            *SourceLocation `json:"setUp,omitempty"`
		TearDown         *SourceLocation `json:"tearDown,omitempty"`
		Cases            []TestCase      `json:"cases"`
	}{
		Version: manifest.Version, GeneratorVersion: manifest.GeneratorVersion, Sources: manifest.Sources,
		SetUp: manifest.SetUp, TearDown: manifest.TearDown, Cases: manifest.Cases,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func validateManifestContent(manifest Manifest) error {
	if manifest.GeneratorVersion != "" &&
		(!validText(manifest.GeneratorVersion) || len(manifest.GeneratorVersion) > 128) {
		return fmt.Errorf("%w: malformed generator version", ErrInvalidManifest)
	}
	if len(manifest.Sources) == 0 {
		return fmt.Errorf("%w: sources must not be empty", ErrInvalidManifest)
	}
	seenSources := make(map[string]struct{}, len(manifest.Sources))
	for index, source := range manifest.Sources {
		if !validManifestPath(source) {
			return fmt.Errorf("%w: invalid source path %q", ErrInvalidManifest, source)
		}
		if _, exists := seenSources[source]; exists {
			return fmt.Errorf("%w: duplicate source path %q", ErrInvalidManifest, source)
		}
		seenSources[source] = struct{}{}
		if index > 0 && manifest.Sources[index-1] >= source {
			return fmt.Errorf("%w: sources are not in canonical order", ErrInvalidManifest)
		}
	}
	if err := validateLocation(manifest.SetUp, seenSources); err != nil {
		return fmt.Errorf("%w: setUp: %v", ErrInvalidManifest, err)
	}
	if err := validateLocation(manifest.TearDown, seenSources); err != nil {
		return fmt.Errorf("%w: tearDown: %v", ErrInvalidManifest, err)
	}

	seenCases := make(map[string]struct{}, len(manifest.Cases))
	for index, testCase := range manifest.Cases {
		if !validIdentifier(testCase.Name) || !validText(testCase.Identity) ||
			!validText(testCase.Parameters) || testCase.Parameters == "" {
			return fmt.Errorf("%w: malformed case at index %d", ErrInvalidManifest, index)
		}
		if _, exists := seenCases[testCase.Identity]; exists {
			return fmt.Errorf("%w: duplicate case identity %q", ErrInvalidManifest, testCase.Identity)
		}
		seenCases[testCase.Identity] = struct{}{}
		if index > 0 && lessTestCase(testCase, manifest.Cases[index-1]) {
			return fmt.Errorf("%w: cases are not in canonical order", ErrInvalidManifest)
		}
		for _, argument := range testCase.Arguments {
			if !validText(argument) || argument == "" {
				return fmt.Errorf("%w: malformed argument for %q", ErrInvalidManifest, testCase.Identity)
			}
		}
		if err := validateLocation(&testCase.Location, seenSources); err != nil {
			return fmt.Errorf("%w: case %q: %v", ErrInvalidManifest, testCase.Identity, err)
		}
	}
	return nil
}

func validateLocation(location *SourceLocation, sources map[string]struct{}) error {
	if location == nil {
		return nil
	}
	if location.Line <= 0 {
		return errors.New("line must be positive")
	}
	if _, exists := sources[location.Path]; !exists {
		return errors.New("path is not a declared source")
	}
	return nil
}

func lessTestCase(left, right TestCase) bool {
	if left.Identity != right.Identity {
		return left.Identity < right.Identity
	}
	if left.Location.Path != right.Location.Path {
		return left.Location.Path < right.Location.Path
	}
	return left.Location.Line < right.Location.Line
}

func validManifestPath(value string) bool {
	return validText(value) && value != "." && !strings.HasPrefix(value, "/") &&
		!strings.Contains(value, "\\") && path.Clean(value) == value &&
		!hasPortableVolume(value) && value != ".." && !strings.HasPrefix(value, "../")
}

func validIdentifier(value string) bool {
	if value == "" || !(value[0] == '_' || isASCIILetter(value[0])) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if value[index] != '_' && !isASCIILetter(value[index]) && !isASCIIDigit(value[index]) {
			return false
		}
	}
	return true
}

func validText(value string) bool {
	if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	for _, character := range value {
		if character < 0x20 && character != '\t' || character == 0x7f {
			return false
		}
	}
	return true
}

func sortManifestCases(cases []TestCase) {
	sort.Slice(cases, func(left, right int) bool {
		return lessTestCase(cases[left], cases[right])
	})
}
