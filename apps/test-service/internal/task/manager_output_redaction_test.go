package task

import (
	"strings"
	"testing"
)

func TestTaskOutputRedactsNativePaths(t *testing.T) {
	if got := publicDiagnosticURI("file:///E:/private/workspace/test.cpp"); got != "workspace:///" {
		t.Fatalf("diagnostic URI retained a native drive: %q", got)
	}
	if got := nativePathInTaskOutput.ReplaceAllString("unit-test-ide://artifact/abc", "$1[redacted-path]"); got != "unit-test-ide://artifact/abc" {
		t.Fatalf("protocol URI was mistaken for a native path: %q", got)
	}
	for _, input := range []string{
		`error: E:\\build\\generated.cpp(4,2)`,
		`error: file:///E:/build/generated.cpp(4,2)`,
	} {
		got := nativePathInTaskOutput.ReplaceAllString(input, "[redacted-path]")
		if strings.Contains(got, "E:/") || strings.Contains(got, `E:\\`) {
			t.Fatalf("native path leaked from task output: input=%q got=%q", input, got)
		}
	}
}
