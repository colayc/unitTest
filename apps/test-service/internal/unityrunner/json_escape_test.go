package unityrunner

import (
	"strings"
	"testing"
)

func TestCStringLiteralEscapesEveryUnsafeByteDeterministically(t *testing.T) {
	input := "quote\" slash\\ newline\n tab\t delete\x7f utf8-é"
	want := `"quote\" slash\\ newline\n tab\t delete\177 utf8-\303\251"`
	if got := cStringLiteral(input); got != want {
		t.Fatalf("cStringLiteral() = %q, want %q", got, want)
	}
}

func TestRunnerJSONWriterEscapesQuotesBackslashesAndControls(t *testing.T) {
	manifest := parseFixtureManifest(t, []string{"testdata/basic.c"})
	runner, _, err := Generate(GenerateInput{
		Manifest: manifest, GeneratorVersion: CurrentGeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := string(runner)
	for _, fragment := range []string{
		`case '"':`,
		`case '\\':`,
		`case '\b':`,
		`case '\f':`,
		`case '\n':`,
		`case '\r':`,
		`case '\t':`,
		`"\\u%04x"`,
		"fflush(result)",
	} {
		if !strings.Contains(source, fragment) {
			t.Errorf("runner JSON writer is missing %q", fragment)
		}
	}
}
