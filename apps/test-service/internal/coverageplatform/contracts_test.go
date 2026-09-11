package coverageplatform

import (
	"testing"

	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
)

type contractVerifier struct{ valid bool }

func (verifier *contractVerifier) Path() string {
	if verifier == nil || !verifier.valid {
		return ""
	}
	return "C:\\coverage"
}
func (verifier *contractVerifier) Verify() error {
	if verifier == nil || !verifier.valid {
		return ErrInvalidCapability
	}
	return nil
}

type contractPath struct{ valid bool }

func (path *contractPath) Path() string {
	if path == nil || !path.valid {
		return ""
	}
	return "C:\\tool.exe"
}
func (path *contractPath) Verify() error {
	if path == nil || !path.valid {
		return ErrInvalidCapability
	}
	return nil
}

type contractClaim struct{}

func (*contractClaim) Commit()   {}
func (*contractClaim) Rollback() {}

type contractToolset struct {
	valid          bool
	cCompiler      coveragerun.TrustedPath
	cCompilerSet   bool
	cxxCompiler    coveragerun.TrustedPath
	cxxCompilerSet bool
	tools          []coveragerun.TrustedPath
	toolsSet       bool
}

func (toolset *contractToolset) Version() string {
	if toolset != nil && toolset.valid {
		return "v1"
	}
	return ""
}
func (toolset *contractToolset) Identity() string {
	if toolset != nil && toolset.valid {
		return "identity"
	}
	return ""
}
func (toolset *contractToolset) CCompiler() coveragerun.TrustedPath {
	if toolset != nil && toolset.cCompilerSet {
		return toolset.cCompiler
	}
	return &contractPath{valid: toolset != nil && toolset.valid}
}
func (toolset *contractToolset) CXXCompiler() coveragerun.TrustedPath {
	if toolset != nil && toolset.cxxCompilerSet {
		return toolset.cxxCompiler
	}
	return &contractPath{valid: toolset != nil && toolset.valid}
}
func (toolset *contractToolset) Tools() []coveragerun.TrustedPath {
	if toolset != nil && toolset.toolsSet {
		return append([]coveragerun.TrustedPath(nil), toolset.tools...)
	}
	return []coveragerun.TrustedPath{
		&contractPath{valid: toolset != nil && toolset.valid},
		&contractPath{valid: toolset != nil && toolset.valid},
		&contractPath{valid: toolset != nil && toolset.valid},
	}
}
func (toolset *contractToolset) Verify() error {
	if toolset == nil || !toolset.valid {
		return ErrInvalidCapability
	}
	return nil
}
func (*contractToolset) ClaimOwnership() (OwnershipClaim, error) {
	return &contractClaim{}, nil
}
func (*contractToolset) Close() error { return nil }

type contractOutput struct{}

func (*contractOutput) ReadAll() ([]byte, error) { return []byte("output"), nil }

type contractCollector struct{}

func (*contractCollector) ProcessSpec() task.ProcessSpec { return task.ProcessSpec{} }
func (*contractCollector) Verify() error                 { return nil }
func (*contractCollector) VerifyAfter() error            { return nil }
func (*contractCollector) ValidateProcessTarget(string, []string, []string, []string, string) error {
	return nil
}
func (*contractCollector) PinnedOutput() (Output, error) { return &contractOutput{}, nil }
func (*contractCollector) Close() error                  { return nil }

func TestContractInterfacesAcceptTypedImplementations(t *testing.T) {
	var verifier DirectoryVerifier = &contractVerifier{valid: true}
	var toolset Toolset = &contractToolset{valid: true}
	var collector CollectorExecution = &contractCollector{}
	if VerifyDirectory(verifier) != nil || VerifyToolset(toolset) != nil || collector.Verify() != nil {
		t.Fatal("coverage platform contracts are not usable")
	}
	output, err := collector.PinnedOutput()
	if err != nil || stringMustRead(t, output) != "output" {
		t.Fatalf("PinnedOutput() = %v, %v", output, err)
	}
}

func TestContractCapabilitiesRejectTypedNilAndMalformedValues(t *testing.T) {
	var nilVerifier *contractVerifier
	var typedNilVerifier DirectoryVerifier = nilVerifier
	if VerifyDirectory(typedNilVerifier) == nil {
		t.Fatal("typed-nil directory verifier was accepted")
	}
	if VerifyDirectory(&contractVerifier{}) == nil {
		t.Fatal("malformed directory verifier was accepted")
	}
	var nilToolset *contractToolset
	var typedNilToolset Toolset = nilToolset
	if VerifyToolset(typedNilToolset) == nil {
		t.Fatal("typed-nil toolset was accepted")
	}
	if VerifyToolset(&contractToolset{}) == nil {
		t.Fatal("toolset with malformed capabilities was accepted")
	}
	for _, test := range []struct {
		name string
		edit func(*contractToolset)
	}{
		{"nil C compiler", func(toolset *contractToolset) { toolset.cCompilerSet = true }},
		{"invalid C compiler", func(toolset *contractToolset) { toolset.cCompilerSet = true; toolset.cCompiler = &contractPath{} }},
		{"nil CXX compiler", func(toolset *contractToolset) { toolset.cxxCompilerSet = true }},
		{"invalid CXX compiler", func(toolset *contractToolset) { toolset.cxxCompilerSet = true; toolset.cxxCompiler = &contractPath{} }},
		{"nil tools slice", func(toolset *contractToolset) { toolset.toolsSet = true }},
		{"nil first tool", func(toolset *contractToolset) {
			toolset.toolsSet = true
			toolset.tools = []coveragerun.TrustedPath{nil, &contractPath{valid: true}, &contractPath{valid: true}}
		}},
		{"invalid second tool", func(toolset *contractToolset) {
			toolset.toolsSet = true
			toolset.tools = []coveragerun.TrustedPath{&contractPath{valid: true}, &contractPath{}, &contractPath{valid: true}}
		}},
		{"invalid third tool", func(toolset *contractToolset) {
			toolset.toolsSet = true
			toolset.tools = []coveragerun.TrustedPath{&contractPath{valid: true}, &contractPath{valid: true}, &contractPath{}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			toolset := &contractToolset{valid: true}
			test.edit(toolset)
			if VerifyToolset(toolset) == nil {
				t.Fatal("toolset with malformed capability was accepted")
			}
		})
	}
}

func TestContractInstrumentationIsValueComparable(t *testing.T) {
	value := Instrumentation{IncludePath: "C:\\coverage.cmake", SHA256: "sha", Fingerprint: "fingerprint"}
	if value == (Instrumentation{}) {
		t.Fatal("instrumentation contract lost its value semantics")
	}
}

func stringMustRead(t *testing.T, output Output) string {
	t.Helper()
	value, err := output.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}
