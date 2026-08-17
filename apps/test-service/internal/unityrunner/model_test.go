package unityrunner

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManifestUsesCanonicalVersionAndDigest(t *testing.T) {
	manifest, err := ParseSources(".", []string{"testdata/basic.c"}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != ManifestVersion {
		t.Fatalf("Version = %q, want %q", manifest.Version, ManifestVersion)
	}
	if len(manifest.SHA256) != 64 || strings.ToLower(manifest.SHA256) != manifest.SHA256 {
		t.Fatalf("SHA256 = %q", manifest.SHA256)
	}
	if err := manifest.Verify(); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Manifest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Verify(); err != nil {
		t.Fatalf("decoded Verify() error = %v", err)
	}

	decoded.Cases[0].Identity += "-changed"
	if err := decoded.Verify(); err == nil {
		t.Fatal("Verify() accepted a modified manifest")
	}
}

func TestDefaultLimitsAreFiniteAndValid(t *testing.T) {
	limits := DefaultLimits()
	if err := limits.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if limits.MaxSources <= 0 || limits.MaxSourceBytes <= 0 || limits.MaxCases <= 0 ||
		limits.MaxCaseNameBytes <= 0 || limits.MaxParameterBytes <= 0 ||
		limits.MaxParameterInstances <= 0 {
		t.Fatalf("DefaultLimits() = %#v", limits)
	}

	invalid := limits
	invalid.MaxCases = 0
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate() accepted a zero limit")
	}
}
