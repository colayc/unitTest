package testdiscovery

import (
	"context"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestCatalogRevisionTracksSemanticFingerprintFields(t *testing.T) {
	base := fingerprintFixture(strings.Repeat("1", 64))
	base.AdapterContracts = []AdapterContract{{
		CTestName: "core.tests",
		Framework: testdomain.FrameworkCppUTest,
		Version:   "cpputest.v1",
	}}
	first, err := CatalogRevision(base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*Fingerprint){
		"workspace generation": func(value *Fingerprint) {
			value.WorkspaceGeneration = strings.Repeat("9", 64)
		},
		"test configuration": func(value *Fingerprint) {
			value.TestConfigurationSHA256 = strings.Repeat("8", 64)
		},
		"CMake installation": func(value *Fingerprint) {
			value.CMakeInstallationIdentity = strings.Repeat("7", 64)
		},
		"build profile": func(value *Fingerprint) {
			value.BuildProfileIdentity = strings.Repeat("6", 64)
		},
		"File API reply": func(value *Fingerprint) {
			value.FileAPIReplyIdentity = strings.Repeat("5", 64)
		},
		"CTest semantic": func(value *Fingerprint) {
			value.CTestSemanticSHA256 = strings.Repeat("4", 64)
		},
		"executable SHA": func(value *Fingerprint) {
			value.Executables[0].SHA256 = strings.Repeat("3", 64)
		},
		"manifest SHA": func(value *Fingerprint) {
			value.Manifests[0].SHA256 = strings.Repeat("2", 64)
		},
		"adapter contract": func(value *Fingerprint) {
			value.AdapterContracts[0].Version = "cpputest.v2"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := cloneFingerprint(base)
			mutate(&changed)
			revision, err := CatalogRevision(changed)
			if err != nil {
				t.Fatal(err)
			}
			if revision == first {
				t.Fatalf("%s did not change revision %q", name, first)
			}
		})
	}

	reordered := cloneFingerprint(base)
	reordered.Executables = []cmake.FingerprintFile{
		{Path: "/build/other", Identity: "file-2", SHA256: strings.Repeat("d", 64)},
		reordered.Executables[0],
	}
	ordered := cloneFingerprint(reordered)
	ordered.Executables[0], ordered.Executables[1] = ordered.Executables[1], ordered.Executables[0]
	firstOrder, err := CatalogRevision(reordered)
	if err != nil {
		t.Fatal(err)
	}
	secondOrder, err := CatalogRevision(ordered)
	if err != nil {
		t.Fatal(err)
	}
	if firstOrder != secondOrder {
		t.Fatal("collection order changed Catalog revision")
	}
}

func TestValidateRevisionUsesContentSHAInsteadOfMTime(t *testing.T) {
	fingerprint := fingerprintFixture(strings.Repeat("1", 64))
	revision, err := CatalogRevision(fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	catalog := validEmptyCatalog(revision)

	current, err := ValidateRevision(context.Background(), catalog, staticFingerprintSource{
		fingerprint: fingerprint,
	})
	if err != nil || current.State != RevisionCurrent {
		t.Fatalf("current status = %#v, %v", current, err)
	}

	changed := cloneFingerprint(fingerprint)
	// File mtimes are intentionally absent from Fingerprint. A same-path,
	// same-identity file with changed content must still invalidate the Catalog.
	changed.Executables[0].SHA256 = strings.Repeat("e", 64)
	stale, err := ValidateRevision(context.Background(), catalog, staticFingerprintSource{
		fingerprint: changed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stale.State != RevisionStale || stale.CatalogRevision != revision ||
		stale.CurrentRevision == revision {
		t.Fatalf("stale status = %#v", stale)
	}
}

func TestRebindSelectionRequiresEveryStableID(t *testing.T) {
	adapter := &discoveryAdapter{
		framework: testdomain.FrameworkCppUTest,
		version:   "cpputest.v1",
		results: map[string]testframework.DiscoveryResult{
			"core.tests": {
				Items: []testframework.DiscoveredItem{
					{Kind: testdomain.ItemCase, LogicalName: "adds", DisplayName: "adds"},
				},
			},
		},
	}
	catalog := mustBuildCatalog(t, BuildInput{
		ProjectID:   "core",
		ProfileID:   strings.Repeat("1", 64),
		GeneratedAt: fixedGeneratedAt,
		Fingerprint: fingerprintFixture(strings.Repeat("1", 64)),
		Containers: []ContainerInput{{
			Descriptor: compatibleDiscoveryDescriptor("core.tests"),
			Selection:  frameworkSelection(adapter),
		}},
	})
	selected := []testdomain.ID{catalog.Containers[0].ID, catalog.Items[0].ID}
	status, err := RebindSelection(selected, catalog)
	if err != nil || !status.Rebindable || len(status.MissingIDs) != 0 ||
		status.Revision != catalog.Revision {
		t.Fatalf("rebind status = %#v, %v", status, err)
	}

	missing := testdomain.ID("utid-v1-" + strings.Repeat("f", 64))
	status, err = RebindSelection(append(selected, missing), catalog)
	if err != nil {
		t.Fatal(err)
	}
	if status.Rebindable || len(status.MissingIDs) != 1 || status.MissingIDs[0] != missing {
		t.Fatalf("missing status = %#v", status)
	}
}

func fingerprintFixture(profileID string) Fingerprint {
	return Fingerprint{
		WorkspaceGeneration:       strings.Repeat("a", 64),
		TestConfigurationSHA256:   strings.Repeat("b", 64),
		CMakeInstallationIdentity: strings.Repeat("c", 64),
		BuildProfileIdentity:      profileID,
		FileAPIReplyIdentity:      strings.Repeat("d", 64),
		CTestSemanticSHA256:       strings.Repeat("e", 64),
		Executables: []cmake.FingerprintFile{{
			Path: "/build/core-tests", Identity: "file-1", SHA256: strings.Repeat("f", 64),
		}},
		Manifests: []cmake.FingerprintFile{{
			Path: "/build/unity-manifest.json", Identity: "manifest-1", SHA256: strings.Repeat("1", 64),
		}},
	}
}

func validEmptyCatalog(revision string) testdomain.Catalog {
	return testdomain.Catalog{
		ProjectID:   "core",
		ProfileID:   strings.Repeat("1", 64),
		Revision:    revision,
		GeneratedAt: fixedGeneratedAt,
		Containers:  []testdomain.Container{},
		Items:       []testdomain.Item{},
		Diagnostics: []testdomain.Diagnostic{},
	}
}

type staticFingerprintSource struct {
	fingerprint Fingerprint
	err         error
}

func (source staticFingerprintSource) CurrentFingerprint(
	context.Context,
	testdomain.Catalog,
) (Fingerprint, error) {
	return source.fingerprint, source.err
}
