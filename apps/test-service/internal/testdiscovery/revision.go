package testdiscovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/testdomain"
)

var (
	ErrInvalidFingerprint = errors.New("invalid test catalog fingerprint")
	ErrInvalidRebind      = errors.New("invalid test catalog rebind")
)

type AdapterContract struct {
	CTestName string               `json:"ctestName"`
	Framework testdomain.Framework `json:"framework"`
	Version   string               `json:"version"`
}

type Fingerprint struct {
	WorkspaceGeneration       string
	TestConfigurationSHA256   string
	CMakeInstallationIdentity string
	BuildProfileIdentity      string
	FileAPIReplyIdentity      string
	CTestSemanticSHA256       string
	Executables               []cmake.FingerprintFile
	Manifests                 []cmake.FingerprintFile
	AdapterContracts          []AdapterContract
}

type revisionPayload struct {
	WorkspaceGeneration       string                  `json:"workspaceGeneration"`
	TestConfigurationSHA256   string                  `json:"testConfigurationSha256"`
	CMakeInstallationIdentity string                  `json:"cmakeInstallationIdentity"`
	BuildProfileIdentity      string                  `json:"buildProfileIdentity"`
	FileAPIReplyIdentity      string                  `json:"fileApiReplyIdentity"`
	CTestSemanticSHA256       string                  `json:"ctestSemanticSha256"`
	Executables               []cmake.FingerprintFile `json:"executables"`
	Manifests                 []cmake.FingerprintFile `json:"manifests"`
	AdapterContracts          []AdapterContract       `json:"adapterContracts"`
}

func CatalogRevision(fingerprint Fingerprint) (string, error) {
	canonical, err := canonicalFingerprint(fingerprint)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(revisionPayload{
		WorkspaceGeneration:       canonical.WorkspaceGeneration,
		TestConfigurationSHA256:   canonical.TestConfigurationSHA256,
		CMakeInstallationIdentity: canonical.CMakeInstallationIdentity,
		BuildProfileIdentity:      canonical.BuildProfileIdentity,
		FileAPIReplyIdentity:      canonical.FileAPIReplyIdentity,
		CTestSemanticSHA256:       canonical.CTestSemanticSHA256,
		Executables:               canonical.Executables,
		Manifests:                 canonical.Manifests,
		AdapterContracts:          canonical.AdapterContracts,
	})
	if err != nil {
		return "", ErrInvalidFingerprint
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

type FingerprintSource interface {
	CurrentFingerprint(context.Context, testdomain.Catalog) (Fingerprint, error)
}

type RevisionState string

const (
	RevisionCurrent RevisionState = "current"
	RevisionStale   RevisionState = "stale"
)

type RevisionStatus struct {
	State           RevisionState
	CatalogRevision string
	CurrentRevision string
}

func ValidateRevision(
	ctx context.Context,
	catalog testdomain.Catalog,
	source FingerprintSource,
) (RevisionStatus, error) {
	if ctx == nil || source == nil {
		return RevisionStatus{}, ErrInvalidFingerprint
	}
	validated, err := testdomain.NewCatalog(catalog)
	if err != nil {
		return RevisionStatus{}, err
	}
	if err := ctx.Err(); err != nil {
		return RevisionStatus{}, err
	}
	fingerprint, err := source.CurrentFingerprint(ctx, validated)
	if err != nil {
		return RevisionStatus{}, err
	}
	if fingerprint.BuildProfileIdentity != validated.ProfileID {
		return RevisionStatus{}, ErrInvalidFingerprint
	}
	current, err := CatalogRevision(fingerprint)
	if err != nil {
		return RevisionStatus{}, err
	}
	status := RevisionStatus{
		State:           RevisionStale,
		CatalogRevision: validated.Revision,
		CurrentRevision: current,
	}
	if current == validated.Revision {
		status.State = RevisionCurrent
	}
	return status, nil
}

type RebindStatus struct {
	Rebindable bool
	Revision   string
	MissingIDs []testdomain.ID
}

func RebindSelection(
	selected []testdomain.ID,
	refreshed testdomain.Catalog,
) (RebindStatus, error) {
	if len(selected) == 0 {
		return RebindStatus{}, ErrInvalidRebind
	}
	catalog, err := testdomain.NewCatalog(refreshed)
	if err != nil {
		return RebindStatus{}, err
	}
	available := make(map[testdomain.ID]struct{}, len(catalog.Containers)+len(catalog.Items))
	for _, container := range catalog.Containers {
		available[container.ID] = struct{}{}
	}
	for _, item := range catalog.Items {
		available[item.ID] = struct{}{}
	}
	seen := make(map[testdomain.ID]struct{}, len(selected))
	missing := make([]testdomain.ID, 0)
	for _, id := range selected {
		if !testdomain.ValidID(id) {
			return RebindStatus{}, ErrInvalidRebind
		}
		if _, duplicate := seen[id]; duplicate {
			return RebindStatus{}, ErrInvalidRebind
		}
		seen[id] = struct{}{}
		if _, exists := available[id]; !exists {
			missing = append(missing, id)
		}
	}
	sort.Slice(missing, func(first, second int) bool {
		return missing[first] < missing[second]
	})
	return RebindStatus{
		Rebindable: len(missing) == 0,
		Revision:   catalog.Revision,
		MissingIDs: missing,
	}, nil
}

func canonicalFingerprint(value Fingerprint) (Fingerprint, error) {
	hashes := []string{
		value.WorkspaceGeneration,
		value.TestConfigurationSHA256,
		value.CMakeInstallationIdentity,
		value.BuildProfileIdentity,
		value.FileAPIReplyIdentity,
		value.CTestSemanticSHA256,
	}
	for _, hash := range hashes {
		if !validLowerHash(hash) {
			return Fingerprint{}, ErrInvalidFingerprint
		}
	}
	var err error
	value.Executables, err = canonicalFiles(value.Executables)
	if err != nil {
		return Fingerprint{}, err
	}
	value.Manifests, err = canonicalFiles(value.Manifests)
	if err != nil {
		return Fingerprint{}, err
	}
	value.AdapterContracts, err = canonicalContracts(value.AdapterContracts)
	if err != nil {
		return Fingerprint{}, err
	}
	return value, nil
}

func canonicalFiles(values []cmake.FingerprintFile) ([]cmake.FingerprintFile, error) {
	result := make([]cmake.FingerprintFile, len(values))
	for index, value := range values {
		if !validText(value.Path, 32*1024) || !validText(value.Identity, 4096) ||
			!validLowerHash(value.SHA256) {
			return nil, ErrInvalidFingerprint
		}
		value.Path = filepath.ToSlash(filepath.Clean(value.Path))
		result[index] = value
	}
	sort.Slice(result, func(first, second int) bool {
		if result[first].Path != result[second].Path {
			return result[first].Path < result[second].Path
		}
		if result[first].Identity != result[second].Identity {
			return result[first].Identity < result[second].Identity
		}
		return result[first].SHA256 < result[second].SHA256
	})
	result, err := uniqueFiles(result)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return []cmake.FingerprintFile{}, nil
	}
	return result, nil
}

func uniqueFiles(values []cmake.FingerprintFile) ([]cmake.FingerprintFile, error) {
	if len(values) == 0 {
		return values, nil
	}
	write := 1
	for read := 1; read < len(values); read++ {
		if values[read].Path == values[write-1].Path {
			if values[read] != values[write-1] {
				return nil, ErrInvalidFingerprint
			}
			continue
		}
		values[write] = values[read]
		write++
	}
	return values[:write], nil
}

func canonicalContracts(values []AdapterContract) ([]AdapterContract, error) {
	result := append([]AdapterContract(nil), values...)
	for _, value := range result {
		if !validText(value.CTestName, 512) ||
			!selectableFramework(value.Framework) ||
			!validContractVersion(value.Version) {
			return nil, ErrInvalidFingerprint
		}
	}
	sort.Slice(result, func(first, second int) bool {
		if result[first].CTestName != result[second].CTestName {
			return result[first].CTestName < result[second].CTestName
		}
		if result[first].Framework != result[second].Framework {
			return result[first].Framework < result[second].Framework
		}
		return result[first].Version < result[second].Version
	})
	for index := 1; index < len(result); index++ {
		if result[index].CTestName == result[index-1].CTestName {
			return nil, ErrInvalidFingerprint
		}
	}
	if result == nil {
		return []AdapterContract{}, nil
	}
	return result, nil
}

func cloneFingerprint(value Fingerprint) Fingerprint {
	value.Executables = append([]cmake.FingerprintFile(nil), value.Executables...)
	value.Manifests = append([]cmake.FingerprintFile(nil), value.Manifests...)
	value.AdapterContracts = append([]AdapterContract(nil), value.AdapterContracts...)
	return value
}

func validLowerHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func validText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) &&
		!strings.ContainsRune(value, '\x00')
}

func selectableFramework(value testdomain.Framework) bool {
	return value == testdomain.FrameworkCppUTest ||
		value == testdomain.FrameworkUnity
}

func validContractVersion(value string) bool {
	if !validText(value, 128) {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
