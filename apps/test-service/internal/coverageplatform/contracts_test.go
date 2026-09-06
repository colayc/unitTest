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

type contractToolset struct{ valid bool }

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
	return &contractPath{valid: toolset != nil && toolset.valid}
}
func (toolset *contractToolset) CXXCompiler() coveragerun.TrustedPath {
	return &contractPath{valid: toolset != nil && toolset.valid}
}
func (toolset *contractToolset) Tools() []coveragerun.TrustedPath {
	return []coveragerun.TrustedPath{&contractPath{valid: toolset != nil && toolset.valid}}
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
