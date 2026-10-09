package casedesign

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"unicode"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/managedtest"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

func lowerHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func shortText(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func identifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for i, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func validIdentity(d Definition) bool {
	b := d.Binding
	if d.SchemaVersion != SchemaVersion || !lowerHex(b.FileID, 32) || !lowerHex(b.FunctionID, 32) ||
		!lowerHex(b.SymbolID, 64) || !lowerHex(b.SourceDigest, 64) || !lowerHex(b.WorkspaceGeneration, 64) ||
		!lowerHex(b.TargetID, 64) || !lowerHex(b.ProgramDigest, 64) || !lowerHex(b.BuildConfigDigest, 64) ||
		!shortText(b.ToolchainID, 256) || (b.Framework != "unity" && b.Framework != "cpputest") ||
		d.Digest != "" && !lowerHex(d.Digest, 64) {
		return false
	}
	switch d.Origin {
	case OriginAutomatic, OriginManual, OriginImport:
	default:
		return false
	}
	id, err := managedtest.StableCaseID(b.ProjectID, b.SourceRelativePath, b.FunctionID, d.ScenarioID)
	return err == nil && id == d.CaseID
}

// The payload excludes storage-assigned revision and the supplied digest.
func contentBytes(d Definition) ([]byte, error) {
	return json.Marshal(struct {
		SchemaVersion              int
		CaseID, ScenarioID, Origin string
		Enabled                    bool
		Binding                    Binding
		Inputs                     []solver.Input
		Expectations               []Expectation
	}{d.SchemaVersion, d.CaseID, d.ScenarioID, d.Origin, d.Enabled, d.Binding, d.Inputs, d.Expectations})
}

func contentDigest(payload []byte) string {
	hash := sha256.New()
	hash.Write([]byte("case-definition-v1\x00"))
	hash.Write(payload)
	return hex.EncodeToString(hash.Sum(nil))
}
