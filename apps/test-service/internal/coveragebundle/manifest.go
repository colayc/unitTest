package coveragebundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

const (
	manifestName = "manifest.resolved.json"
	readyName    = "READY"
)

var (
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*$`)
	namePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	pathPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+/-]*$`)
)

type resolvedManifest struct {
	SchemaVersion int            `json:"schemaVersion"`
	Platform      string         `json:"platform"`
	PythonVersion string         `json:"pythonVersion"`
	GcovrVersion  string         `json:"gcovrVersion"`
	Inputs        resolvedInputs `json:"inputs"`
	Outputs       []outputRecord `json:"outputs"`
}

type resolvedInputs struct {
	PythonArtifact inputArtifact   `json:"pythonArtifact"`
	Wheels         []wheelInput    `json:"wheels"`
	Provenance     inputProvenance `json:"provenance"`
}

type inputArtifact struct {
	Kind     string `json:"kind"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
}

type wheelInput struct {
	Project  string `json:"project"`
	Version  string `json:"version"`
	Kind     string `json:"kind"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
}

type inputProvenance struct {
	Recipe        recipeInput `json:"recipe"`
	BuilderImage  *string     `json:"builderImage"`
	GlibcBaseline *string     `json:"glibcBaseline"`
}

type recipeInput struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type outputRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind"`
}

func parseResolvedManifest(contents []byte, expectedPlatform string) (resolvedManifest, error) {
	if err := rejectDuplicateJSONNames(contents); err != nil {
		return resolvedManifest{}, fmt.Errorf("manifest JSON: %w", err)
	}
	if err := validateResolvedJSONShape(contents); err != nil {
		return resolvedManifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var manifest resolvedManifest
	if err := decoder.Decode(&manifest); err != nil {
		return resolvedManifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return resolvedManifest{}, err
	}
	if err := manifest.validate(expectedPlatform); err != nil {
		return resolvedManifest{}, err
	}
	return manifest, nil
}

func validateResolvedJSONShape(contents []byte) error {
	root, err := exactJSONObject(contents, []string{"schemaVersion", "platform", "pythonVersion", "gcovrVersion", "inputs", "outputs"}, "resolved manifest")
	if err != nil {
		return err
	}
	inputs, err := exactJSONObject(root["inputs"], []string{"pythonArtifact", "wheels", "provenance"}, "resolved inputs")
	if err != nil {
		return err
	}
	if _, err := exactJSONObject(inputs["pythonArtifact"], []string{"kind", "filename", "url", "sha256"}, "resolved Python input"); err != nil {
		return err
	}
	var wheels []json.RawMessage
	if err := json.Unmarshal(inputs["wheels"], &wheels); err != nil {
		return errors.New("resolved wheel inputs must be an array")
	}
	for _, wheel := range wheels {
		if _, err := exactJSONObject(wheel, []string{"project", "version", "kind", "filename", "url", "sha256"}, "resolved wheel input"); err != nil {
			return err
		}
	}
	provenance, err := exactJSONObject(inputs["provenance"], []string{"recipe", "builderImage", "glibcBaseline"}, "resolved provenance")
	if err != nil {
		return err
	}
	if _, err := exactJSONObject(provenance["recipe"], []string{"name", "sha256"}, "resolved recipe"); err != nil {
		return err
	}
	var outputs []json.RawMessage
	if err := json.Unmarshal(root["outputs"], &outputs); err != nil {
		return errors.New("resolved outputs must be an array")
	}
	for _, output := range outputs {
		if _, err := exactJSONObject(output, []string{"path", "sha256", "kind"}, "resolved output"); err != nil {
			return err
		}
	}
	return nil
}

func exactJSONObject(contents []byte, expected []string, label string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(contents, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	if len(object) != len(expected) {
		return nil, fmt.Errorf("%s has unexpected fields", label)
	}
	for _, key := range expected {
		if _, ok := object[key]; !ok {
			return nil, fmt.Errorf("%s has unexpected fields", label)
		}
	}
	return object, nil
}

func rejectDuplicateJSONNames(contents []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.UseNumber()
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				nameToken, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := nameToken.(string)
				if !ok {
					return errors.New("object key is not a string")
				}
				if _, exists := seen[name]; exists {
					return fmt.Errorf("duplicate object key %q", name)
				}
				seen[name] = struct{}{}
				if err := visit(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return errors.New("unterminated object")
			}
		case '[':
			for decoder.More() {
				if err := visit(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("unterminated array")
			}
		default:
			return errors.New("unexpected JSON delimiter")
		}
		return nil
	}
	if err := visit(); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("manifest has trailing JSON")
		}
		return fmt.Errorf("decode trailing manifest data: %w", err)
	}
	return nil
}

func (manifest resolvedManifest) validate(expectedPlatform string) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("unsupported resolved manifest schema %d", manifest.SchemaVersion)
	}
	if manifest.Platform != expectedPlatform {
		return fmt.Errorf("resolved manifest platform %q does not match %q", manifest.Platform, expectedPlatform)
	}
	if !versionPattern.MatchString(manifest.PythonVersion) || !versionPattern.MatchString(manifest.GcovrVersion) {
		return errors.New("resolved manifest has invalid versions")
	}
	if err := manifest.Inputs.validate(manifest); err != nil {
		return err
	}
	if len(manifest.Outputs) == 0 {
		return errors.New("resolved manifest contains no outputs")
	}
	previous := ""
	seen := map[string]struct{}{}
	outputs := make(map[string]outputRecord, len(manifest.Outputs))
	for _, output := range manifest.Outputs {
		if !canonicalOutputPath(output.Path) || output.Kind != "regular-file" || !digestPattern.MatchString(output.SHA256) {
			return fmt.Errorf("invalid resolved output record %q", output.Path)
		}
		if previous != "" && output.Path <= previous {
			return errors.New("resolved outputs are not strictly sorted")
		}
		folded := strings.ToLower(output.Path)
		if _, exists := seen[folded]; exists {
			return fmt.Errorf("duplicate or case-alias output %q", output.Path)
		}
		if output.Path == manifestName || output.Path == readyName {
			return fmt.Errorf("reserved output path %q", output.Path)
		}
		if output.Path != "app/gcovr-runner.pyz" && !strings.HasPrefix(output.Path, "licenses/") && !strings.HasPrefix(output.Path, "python/") {
			return fmt.Errorf("output is outside the closed bundle layout: %q", output.Path)
		}
		if strings.HasPrefix(output.Path, "app/") && output.Path != "app/gcovr-runner.pyz" {
			return fmt.Errorf("unexpected application output %q", output.Path)
		}
		previous = output.Path
		seen[folded] = struct{}{}
		outputs[output.Path] = output
	}
	return validateRequiredOutputs(manifest, outputs)
}

func (inputs resolvedInputs) validate(manifest resolvedManifest) error {
	wantArtifactKind := "embedded-archive"
	if manifest.Platform == "linux-x64" {
		wantArtifactKind = "source-archive"
	}
	if inputs.PythonArtifact.Kind != wantArtifactKind || !validInputFile(inputs.PythonArtifact.Filename) || !validInputURL(inputs.PythonArtifact.URL) || !digestPattern.MatchString(inputs.PythonArtifact.SHA256) {
		return errors.New("invalid resolved Python input")
	}
	if len(inputs.Wheels) == 0 {
		return errors.New("resolved wheel inputs are empty")
	}
	seen := map[string]struct{}{}
	gcovrFound := false
	previous := ""
	for _, wheel := range inputs.Wheels {
		if wheel.Kind != "wheel" || !namePattern.MatchString(wheel.Project) || !versionPattern.MatchString(wheel.Version) || !validInputFile(wheel.Filename) || !strings.HasSuffix(strings.ToLower(wheel.Filename), ".whl") || !validInputURL(wheel.URL) || !digestPattern.MatchString(wheel.SHA256) {
			return fmt.Errorf("invalid resolved wheel input %q", wheel.Project)
		}
		folded := strings.ToLower(wheel.Project)
		if previous != "" && folded <= previous {
			return errors.New("resolved wheel inputs are not strictly sorted")
		}
		if _, exists := seen[folded]; exists {
			return errors.New("duplicate resolved wheel input")
		}
		seen[folded] = struct{}{}
		previous = folded
		if folded == "gcovr" && wheel.Version == manifest.GcovrVersion {
			gcovrFound = true
		}
	}
	if !gcovrFound {
		return errors.New("resolved gcovr input does not match the manifest version")
	}
	if inputs.Provenance.Recipe.Name == "" || !digestPattern.MatchString(inputs.Provenance.Recipe.SHA256) {
		return errors.New("invalid resolved recipe provenance")
	}
	if manifest.Platform == "windows-x64" {
		if inputs.Provenance.BuilderImage != nil || inputs.Provenance.GlibcBaseline != nil {
			return errors.New("unexpected Linux provenance in Windows bundle")
		}
	} else if inputs.Provenance.BuilderImage == nil || *inputs.Provenance.BuilderImage == "" || inputs.Provenance.GlibcBaseline == nil || !versionPattern.MatchString(*inputs.Provenance.GlibcBaseline) {
		return errors.New("missing Linux provenance")
	}
	return nil
}

func validateRequiredOutputs(manifest resolvedManifest, outputs map[string]outputRecord) error {
	required := []string{"app/gcovr-runner.pyz"}
	parts := strings.Split(manifest.PythonVersion, ".")
	if len(parts) < 2 {
		return errors.New("invalid Python version")
	}
	if manifest.Platform == "windows-x64" {
		required = append(required, "python/python.exe", "python/python"+parts[0]+parts[1]+".zip")
	} else {
		required = append(required, "python/bin/python3", "python/lib/python"+parts[0]+"."+parts[1]+"/os.py")
	}
	for _, name := range required {
		if _, ok := outputs[name]; !ok {
			return fmt.Errorf("resolved manifest is missing required output %q", name)
		}
	}
	return nil
}

func canonicalOutputPath(value string) bool {
	if value == "" || !pathPattern.MatchString(value) || strings.Contains(value, `\`) || path.IsAbs(value) || path.Clean(value) != value {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validInputFile(value string) bool {
	return namePattern.MatchString(value) && value != "." && value != ".."
}

func validInputURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" {
		return false
	}
	return parsed.Hostname() == "www.python.org" || parsed.Hostname() == "files.pythonhosted.org"
}
