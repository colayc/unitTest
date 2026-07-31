package unityrunner

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateMatchesGoldenFiles(t *testing.T) {
	manifest := parseFixtureManifest(t, []string{"testdata/basic.c"})
	runner, manifestJSON, err := Generate(GenerateInput{
		Manifest: manifest, GeneratorVersion: CurrentGeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "testdata/basic.runner.golden.c", runner)
	assertGolden(t, "testdata/basic.manifest.golden.json", manifestJSON)
}

func TestGenerateIsDeterministicAcrossSourceInputOrder(t *testing.T) {
	forward := parseFixtureManifest(t, []string{"testdata/basic.c", "testdata/parameterized.c"})
	reverse := parseFixtureManifest(t, []string{"testdata/parameterized.c", "testdata/basic.c"})

	firstRunner, firstManifest, err := Generate(GenerateInput{
		Manifest: forward, GeneratorVersion: CurrentGeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondRunner, secondManifest, err := Generate(GenerateInput{
		Manifest: reverse, GeneratorVersion: CurrentGeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstRunner, secondRunner) || !bytes.Equal(firstManifest, secondManifest) {
		t.Fatal("Generate() output depends on declared source order")
	}
	repeatedRunner, repeatedManifest, err := Generate(GenerateInput{
		Manifest: forward, GeneratorVersion: CurrentGeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstRunner, repeatedRunner) || !bytes.Equal(firstManifest, repeatedManifest) {
		t.Fatal("Generate() output is not byte-for-byte repeatable")
	}
}

func TestGenerateDispatchContainsOnlyManifestCasesAndUsesExactMatch(t *testing.T) {
	manifest := parseFixtureManifest(t, []string{"testdata/parameterized.c"})
	runner, _, err := Generate(GenerateInput{
		Manifest: manifest, GeneratorVersion: CurrentGeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := string(runner)
	for _, testCase := range manifest.Cases {
		if !strings.Contains(source, cStringLiteral(testCase.Identity)) {
			t.Errorf("runner does not contain case %q", testCase.Identity)
		}
	}
	for _, forbidden := range []string{"test_from_string", "test_from_block_comment", "strstr("} {
		if strings.Contains(source, forbidden) {
			t.Errorf("runner contains forbidden text %q", forbidden)
		}
	}
	if !strings.Contains(source, "strcmp(options.case_identity, utide_cases[index].identity) == 0") {
		t.Fatal("runner does not use exact identity comparison")
	}
}

func TestGenerateListModeDoesNotExecuteTests(t *testing.T) {
	manifest := parseFixtureManifest(t, []string{"testdata/basic.c"})
	runner, _, err := Generate(GenerateInput{
		Manifest: manifest, GeneratorVersion: CurrentGeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := string(runner)
	start := strings.Index(source, "static int utide_list_cases(")
	end := strings.Index(source, "static const struct utide_case *utide_find_case(")
	if start < 0 || end <= start {
		t.Fatal("runner list/find functions are missing or reordered")
	}
	listFunction := source[start:end]
	if strings.Contains(listFunction, "UnityDefaultTestRun") ||
		strings.Contains(listFunction, "->function") {
		t.Fatal("list mode can execute a test function")
	}
	if !strings.Contains(listFunction, "utide_write_case_record") {
		t.Fatal("list mode does not write complete case records")
	}
}

func TestGenerateWritesVersionedVerifiableManifestWithoutAbsolutePaths(t *testing.T) {
	manifest := parseFixtureManifest(t, []string{"testdata/basic.c"})
	runner, manifestJSON, err := Generate(GenerateInput{
		Manifest: manifest, GeneratorVersion: CurrentGeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	var generated Manifest
	if err := json.Unmarshal(manifestJSON, &generated); err != nil {
		t.Fatal(err)
	}
	if generated.GeneratorVersion != CurrentGeneratorVersion {
		t.Fatalf("GeneratorVersion = %q", generated.GeneratorVersion)
	}
	if err := generated.Verify(); err != nil {
		t.Fatalf("generated manifest Verify() error = %v", err)
	}
	if !bytes.Contains(runner, []byte(CurrentGeneratorVersion)) ||
		!bytes.Contains(runner, []byte(generated.SHA256)) {
		t.Fatal("runner does not bind generator version and manifest SHA-256")
	}
	absolute, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range [][]byte{runner, manifestJSON} {
		normalized := strings.ReplaceAll(string(output), "\\\\", "/")
		if strings.Contains(normalized, filepath.ToSlash(absolute)) {
			t.Fatalf("generated output contains absolute workspace path %q", absolute)
		}
	}
}

func TestGenerateRejectsInvalidTextAndAbsoluteManifestPaths(t *testing.T) {
	base := parseFixtureManifest(t, []string{"testdata/basic.c"})
	tests := map[string]func(*Manifest){
		"NUL": func(value *Manifest) {
			value.Cases[0].Identity = "bad\x00identity"
		},
		"control": func(value *Manifest) {
			value.Cases[0].Identity = "bad\nidentity"
		},
		"absolute source": func(value *Manifest) {
			value.Sources[0] = "C:/workspace/test.c"
			value.SetUp.Path = value.Sources[0]
			value.TearDown.Path = value.Sources[0]
			for index := range value.Cases {
				value.Cases[index].Location.Path = value.Sources[0]
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			manifest := base
			manifest.Sources = append([]string(nil), base.Sources...)
			manifest.Cases = append([]TestCase(nil), base.Cases...)
			setUp, tearDown := *base.SetUp, *base.TearDown
			manifest.SetUp, manifest.TearDown = &setUp, &tearDown
			mutate(&manifest)
			digest, err := manifestDigest(manifest)
			if err != nil {
				t.Fatal(err)
			}
			manifest.SHA256 = digest
			if _, _, err := Generate(GenerateInput{
				Manifest: manifest, GeneratorVersion: CurrentGeneratorVersion,
			}); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("Generate() error = %v, want ErrInvalidManifest", err)
			}
		})
	}
}

func TestGenerateEnforcesCaseManifestAndRunnerLimits(t *testing.T) {
	manifest := parseFixtureManifest(t, []string{"testdata/basic.c"})

	limits := DefaultGenerateLimits()
	limits.MaxCases = 1
	if _, _, err := Generate(GenerateInput{
		Manifest: manifest, GeneratorVersion: CurrentGeneratorVersion, Limits: limits,
	}); !errors.Is(err, ErrGenerationLimit) {
		t.Fatalf("case limit error = %v", err)
	}

	limits = DefaultGenerateLimits()
	limits.MaxManifestBytes = 16
	if _, _, err := Generate(GenerateInput{
		Manifest: manifest, GeneratorVersion: CurrentGeneratorVersion, Limits: limits,
	}); !errors.Is(err, ErrGenerationLimit) {
		t.Fatalf("manifest limit error = %v", err)
	}

	limits = DefaultGenerateLimits()
	limits.MaxRunnerBytes = 32
	if _, _, err := Generate(GenerateInput{
		Manifest: manifest, GeneratorVersion: CurrentGeneratorVersion, Limits: limits,
	}); !errors.Is(err, ErrGenerationLimit) {
		t.Fatalf("runner limit error = %v", err)
	}
}

func TestGenerateRejectsInvalidOrConflictingGeneratorVersion(t *testing.T) {
	manifest := parseFixtureManifest(t, []string{"testdata/basic.c"})
	for _, version := range []string{"", "latest", "1.0.0\ninjected", strings.Repeat("1", 129)} {
		if _, _, err := Generate(GenerateInput{
			Manifest: manifest, GeneratorVersion: version,
		}); !errors.Is(err, ErrInvalidGenerateInput) {
			t.Fatalf("version %q error = %v", version, err)
		}
	}

	manifest.GeneratorVersion = "9.9.9"
	digest, err := manifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.SHA256 = digest
	if _, _, err := Generate(GenerateInput{
		Manifest: manifest, GeneratorVersion: CurrentGeneratorVersion,
	}); !errors.Is(err, ErrInvalidGenerateInput) {
		t.Fatalf("conflicting version error = %v", err)
	}
}

func parseFixtureManifest(t *testing.T, sources []string) Manifest {
	t.Helper()
	manifest, err := ParseSources(".", sources, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func assertGolden(t *testing.T, relative string, actual []byte) {
	t.Helper()
	if os.Getenv("UPDATE_UNITY_GOLDEN") == "1" {
		if err := os.WriteFile(relative, actual, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(relative)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatalf("%s does not match generated output (got %d bytes, want %d)", relative, len(actual), len(expected))
	}
}
