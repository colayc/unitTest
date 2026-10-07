package runtime

import (
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	render "unit-test-ide.local/test-service/internal/testgenrender"
)

func TestProductionValidationTargetUsesExactGeneratedUnityExecutable(t *testing.T) {
	_, target, _, _ := productionPipelineFixture(t)
	target.language = render.LanguageC
	target.framework = "unity"
	target.renderTarget.FrameworkTarget = "unity"
	target.renderTarget.TestTarget = "classifier-tests"
	target.testTargetID = strings.Repeat("a", 64)
	root := t.TempDir()
	original := cmake.Target{
		ID: target.testTargetID, Name: target.renderTarget.TestTarget, Type: "EXECUTABLE",
		Artifacts: []string{filepath.Join(root, "tests", "classifier-tests.exe")},
	}
	registration := productionValidationPlanRegistration{target: target, symbolID: strings.Repeat("b", 64)}
	resolved, ok := productionValidationTestTarget(registration, []cmake.Target{original})
	if !ok {
		t.Fatal("generated Unity validation target was not resolved")
	}
	want := "classifier-tests_generated_" + strings.Repeat("b", 12)
	if ctestName, ok := productionValidationExecutableName(registration); !ok || ctestName != want {
		t.Fatalf("generated Unity CTest name = %q, %v", ctestName, ok)
	}
	if resolved.ID != original.ID || resolved.Name != want || len(resolved.Artifacts) != 1 ||
		resolved.Artifacts[0] != filepath.Join(root, "tests", want+".exe") {
		t.Fatalf("resolved target = %+v, want name %q", resolved, want)
	}

	registration.symbolID = "invalid"
	if _, ok := productionValidationTestTarget(registration, []cmake.Target{original}); ok {
		t.Fatal("invalid Unity symbol selected a validation target")
	}
}

func TestProductionValidationTargetKeepsCppUTestExecutable(t *testing.T) {
	_, target, _, _ := productionPipelineFixture(t)
	original := cmake.Target{ID: target.testTargetID, Name: target.renderTarget.TestTarget, Type: "EXECUTABLE", Artifacts: []string{filepath.Join(t.TempDir(), "unit_tests")}}
	resolved, ok := productionValidationTestTarget(productionValidationPlanRegistration{target: target}, []cmake.Target{original})
	if !ok || resolved.Name != original.Name || resolved.Artifacts[0] != original.Artifacts[0] {
		t.Fatalf("CppUTest validation target changed: %+v", resolved)
	}
}
