package testdomain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const idPrefix = "utid-v1-"

type ID string

func (id ID) String() string {
	return string(id)
}

func ValidID(id ID) bool {
	value := string(id)
	if len(value) != len(idPrefix)+sha256.Size*2 || !strings.HasPrefix(value, idPrefix) {
		return false
	}
	return validHex(value[len(idPrefix):], sha256.Size*2)
}

type Framework string

const (
	FrameworkCppUTest    Framework = "cpputest"
	FrameworkUnity       Framework = "unity"
	FrameworkOpaqueCTest Framework = "opaque-ctest"
)

func (framework Framework) Valid() bool {
	switch framework {
	case FrameworkCppUTest, FrameworkUnity, FrameworkOpaqueCTest:
		return true
	default:
		return false
	}
}

type CaseIdentity struct {
	ProjectID   string
	CTestName   string
	Framework   Framework
	Group       string
	Suite       string
	Name        string
	Parameters  []Parameter
	SourcePath  string
	ProfileID   string
	ToolchainID string
}

func ContainerID(projectID, ctestName string) (ID, error) {
	fields, err := identityFields(
		identityField{name: "kind", value: "container"},
		identityField{name: "project_id", value: projectID},
		identityField{name: "ctest_logical_name", value: ctestName},
	)
	if err != nil {
		return "", err
	}
	return hashIdentity(fields), nil
}

func GroupID(projectID, ctestName string, framework Framework, group string) (ID, error) {
	if !framework.Valid() {
		return "", invalid(ErrInvalidFramework, "framework", "unsupported value")
	}
	fields, err := identityFields(
		identityField{name: "kind", value: "group"},
		identityField{name: "project_id", value: projectID},
		identityField{name: "ctest_logical_name", value: ctestName},
		identityField{name: "framework", value: string(framework)},
		identityField{name: "group", value: group},
	)
	if err != nil {
		return "", err
	}
	return hashIdentity(fields), nil
}

func SuiteID(projectID, ctestName string, framework Framework, group, suite string) (ID, error) {
	if !framework.Valid() {
		return "", invalid(ErrInvalidFramework, "framework", "unsupported value")
	}
	fields, err := identityFields(
		identityField{name: "kind", value: "suite"},
		identityField{name: "project_id", value: projectID},
		identityField{name: "ctest_logical_name", value: ctestName},
		identityField{name: "framework", value: string(framework)},
		identityField{name: "group", value: group},
		identityField{name: "suite", value: suite},
	)
	if err != nil {
		return "", err
	}
	return hashIdentity(fields), nil
}

func CaseID(identity CaseIdentity) (ID, error) {
	if !identity.Framework.Valid() {
		return "", invalid(ErrInvalidFramework, "framework", "unsupported value")
	}
	fields, err := identityFields(
		identityField{name: "kind", value: "case"},
		identityField{name: "project_id", value: identity.ProjectID},
		identityField{name: "ctest_logical_name", value: identity.CTestName},
		identityField{name: "framework", value: string(identity.Framework)},
		identityField{name: "group", value: identity.Group, optional: true},
		identityField{name: "suite", value: identity.Suite, optional: true},
		identityField{name: "case", value: identity.Name},
	)
	if err != nil {
		return "", err
	}
	parameters, err := canonicalParameters(identity.Parameters)
	if err != nil {
		return "", err
	}
	for _, parameter := range parameters {
		fields = append(fields,
			identityField{name: "parameter_name", value: parameter.Name},
			identityField{name: "parameter_value", value: parameter.Value, optional: true},
		)
	}
	return hashIdentity(fields), nil
}

type identityField struct {
	name     string
	value    string
	optional bool
}

func identityFields(fields ...identityField) ([]identityField, error) {
	normalized := make([]identityField, len(fields))
	for index, field := range fields {
		name, err := normalizeIdentityText(field.name, false)
		if err != nil {
			return nil, invalid(ErrInvalidIdentity, "field_name", err.Error())
		}
		value, err := normalizeIdentityText(field.value, field.optional)
		if err != nil {
			return nil, invalid(ErrInvalidIdentity, field.name, err.Error())
		}
		normalized[index] = identityField{name: name, value: value, optional: field.optional}
	}
	return normalized, nil
}

func normalizeIdentityText(value string, optional bool) (string, error) {
	return normalizeBoundedText(value, optional, 512)
}

func normalizeBoundedText(value string, optional bool, maximumBytes int) (string, error) {
	if !utf8.ValidString(value) {
		return "", invalid(ErrInvalidIdentity, "", "must be valid UTF-8")
	}
	normalized := norm.NFC.String(value)
	if !optional && normalized == "" {
		return "", invalid(ErrInvalidIdentity, "", "must not be empty")
	}
	if strings.IndexByte(normalized, 0) >= 0 {
		return "", invalid(ErrInvalidIdentity, "", "must not contain NUL")
	}
	if len([]byte(normalized)) > maximumBytes {
		return "", invalid(ErrInvalidIdentity, "", "exceeds the UTF-8 byte limit")
	}
	return normalized, nil
}

func canonicalParameters(parameters []Parameter) ([]Parameter, error) {
	if len(parameters) > 64 {
		return nil, invalid(ErrInvalidIdentity, "parameters", "must not contain more than 64 values")
	}
	result := append([]Parameter(nil), parameters...)
	seen := make(map[string]struct{}, len(result))
	for index := range result {
		name, err := normalizeIdentityText(result[index].Name, false)
		if err != nil {
			return nil, invalid(ErrInvalidIdentity, "parameter.name", err.Error())
		}
		value, err := normalizeIdentityText(result[index].Value, true)
		if err != nil {
			return nil, invalid(ErrInvalidIdentity, "parameter.value", err.Error())
		}
		if _, exists := seen[name]; exists {
			return nil, invalid(ErrDuplicateIdentity, "parameter.name", "duplicate value")
		}
		seen[name] = struct{}{}
		result[index] = Parameter{Name: name, Value: value}
	}
	sort.Slice(result, func(first, second int) bool {
		if result[first].Name == result[second].Name {
			return result[first].Value < result[second].Value
		}
		return result[first].Name < result[second].Name
	})
	return result, nil
}

func hashIdentity(fields []identityField) ID {
	hash := sha256.New()
	writeIdentityField(hash, []byte("schema"), []byte("utid-v1"))
	for _, field := range fields {
		writeIdentityField(hash, []byte(field.name), []byte(field.value))
	}
	return ID(idPrefix + hex.EncodeToString(hash.Sum(nil)))
}

type identityWriter interface {
	Write([]byte) (int, error)
}

func writeIdentityField(writer identityWriter, name, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(name)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write(name)
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write(value)
}
