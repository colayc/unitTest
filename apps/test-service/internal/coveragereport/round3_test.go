package coveragereport

import (
	"fmt"
	"strings"
	"testing"
)

func TestSafeDiagnosticRedactsAbsolutePOSIXPathAfterEveryASCIIPunctuation(t *testing.T) {
	path := "/home/alice/key"
	cases := []struct{ name, value string }{{"start", path}}
	for value := byte(33); value <= 126; value++ {
		if value >= '0' && value <= '9' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' {
			continue
		}
		cases = append(cases, struct{ name, value string }{fmt.Sprintf("punctuation-%d", value), "failure" + string(value) + path + "-tail"})
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := safeDiagnostic(testCase.value)
			if strings.Contains(got, path) || got != "[redacted-sensitive-diagnostic]" {
				t.Fatalf("safeDiagnostic(%q) = %q", testCase.value, got)
			}
		})
	}
}

func TestSafeDiagnosticRetainsSafeNoSlashMessage(t *testing.T) {
	const message = "assertion expected true but received false"
	if got := safeDiagnostic(message); got != message {
		t.Fatalf("safeDiagnostic(%q) = %q", message, got)
	}
}
