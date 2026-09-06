package coverageplatform

import (
	"errors"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
)

type contractVerifier struct{}

func (*contractVerifier) Path() string  { return "C:\\coverage" }
func (*contractVerifier) Verify() error { return nil }

type contractClaim struct{}

func (*contractClaim) Commit()   {}
func (*contractClaim) Rollback() {}

type contractToolset struct{}

func (*contractToolset) Version() string                      { return "v1" }
func (*contractToolset) Identity() string                     { return "identity" }
func (*contractToolset) CCompiler() coveragerun.TrustedPath   { return nil }
func (*contractToolset) CXXCompiler() coveragerun.TrustedPath { return nil }
func (*contractToolset) Tools() []coveragerun.TrustedPath     { return nil }
func (*contractToolset) Verify() error                        { return nil }
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
	var verifier DirectoryVerifier = &contractVerifier{}
	var toolset Toolset = &contractToolset{}
	var collector CollectorExecution = &contractCollector{}
	if verifier.Path() == "" || toolset.Version() == "" || collector.Verify() != nil {
		t.Fatal("coverage platform contracts are not usable")
	}
	output, err := collector.PinnedOutput()
	if err != nil || stringMustRead(t, output) != "output" {
		t.Fatalf("PinnedOutput() = %v, %v", output, err)
	}
}

func TestContractInstrumentationIsValueComparable(t *testing.T) {
	value := Instrumentation{IncludePath: "C:\\coverage.cmake", SHA256: "sha", Fingerprint: "fingerprint"}
	if value == (Instrumentation{}) || errors.Is(nil, errors.New("unexpected")) {
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
