package managedtest

import (
	"strings"
	"testing"
	"time"
)

func TestRecordRequiresIdentityAndReceiptBinding(t *testing.T) {
	functionID := strings.Repeat("a", 32)
	caseID, err := StableCaseID("project", "src/example.cpp", functionID, "zero")
	if err != nil {
		t.Fatal(err)
	}
	value := Record{CaseID: caseID, ProjectID: "project", SourceFileID: strings.Repeat("b", 32), FunctionID: functionID,
		SourceRelativePath: "src/example.cpp", ScenarioID: "zero", TestRelativePath: "tests/generated/src/example_test.cpp",
		AcceptedBlockDigest: strings.Repeat("c", 64), GeneratorVersion: "1", Framework: "cpputest", ToolchainID: "toolchain-1",
		SourceDigest: strings.Repeat("d", 64), ValidationReceiptDigest: strings.Repeat("e", 64), Status: StatusCurrent,
		LastVerifiedAt: time.Date(2026, 9, 28, 1, 2, 3, 0, time.UTC)}
	if !ValidRecord(value) {
		t.Fatal("valid record rejected")
	}
	bad := value
	bad.ScenarioID = "other"
	if ValidRecord(bad) {
		t.Fatal("case identity detached from scenario")
	}
	bad = value
	bad.ValidationReceiptDigest = ""
	if ValidRecord(bad) {
		t.Fatal("record without validation receipt accepted")
	}
	bad = value
	bad.TestRelativePath = "tests/generated/../escape_test.cpp"
	if ValidRecord(bad) {
		t.Fatal("escaping test path accepted")
	}
}
