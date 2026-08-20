package coveragenormalize

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
)

func TestEncodeCanonicalGoldenStableRoundTripAndRedacted(t *testing.T) {
	input := llvmNormalizationFixture(t)
	document, bindings, err := NormalizeLLVM(input)
	if err != nil {
		t.Fatal(err)
	}
	first, err := EncodeCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/coverage-v1.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !bytes.Equal(first, golden) {
		t.Fatalf("canonical output differs\nfirst: %s\nsecond: %s\ngolden: %s", first, second, golden)
	}
	if len(first) == 0 || first[len(first)-1] != '\n' || bytes.HasSuffix(first, []byte("\n\n")) {
		t.Fatalf("canonical output must end in exactly one LF: %q", first)
	}
	decoded, err := coveragemodelv1.Decode(first)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, document) {
		t.Fatalf("deep round trip = %#v, want %#v", decoded, document)
	}
	for _, binding := range bindings {
		if bytes.Contains(first, []byte(binding.NativePath)) {
			t.Fatalf("native path leaked: %q", binding.NativePath)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte("runId"), []byte("createdAt"), []byte("percent"), []byte("executable"),
		[]byte("argv"), []byte("environment"), []byte("LLVM_PROFILE_FILE"), []byte("profraw"),
	} {
		if bytes.Contains(first, forbidden) {
			t.Fatalf("forbidden field/value leaked: %q", forbidden)
		}
	}
}

func TestEncodeCanonicalRejectsInvalidOrNonRoundTrippableDocument(t *testing.T) {
	document := expectedLLVMDocument(coveragemodelv1.Available, []coveragemodelv1.Reason{})
	document.Files = nil
	document.Summary = coveragemodelv1.CoverageSummaryV1{}
	if encoded, err := EncodeCanonical(document); err == nil || encoded != nil {
		t.Fatalf("EncodeCanonical() = %q, %v", encoded, err)
	}
}
