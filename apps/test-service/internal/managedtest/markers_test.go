package managedtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

var caseA = "utc_" + strings.Repeat("a", 32)
var caseB = "utc_" + strings.Repeat("b", 32)
var functionA = strings.Repeat("c", 32)

func mustRender(t *testing.T, caseID, newline string) []byte {
	t.Helper()
	block, err := RenderMarkers(caseID, functionA, "calculateTotal", []byte("TEST(Example, Empty) {}"+newline), newline)
	if err != nil {
		t.Fatal(err)
	}
	return block
}

func TestParseDocumentPreservesUnmanagedBytesAndOffsets(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(strings.ReplaceAll(newline, "\n", "LF"), func(t *testing.T) {
			prefix := []byte("// user header" + newline + "#include <a>" + newline)
			suffix := []byte("// handwritten" + newline)
			block := mustRender(t, caseA, newline)
			input := append(append(bytes.Clone(prefix), block...), suffix...)
			doc, err := ParseDocument(input, 4096, 2)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(doc.Bytes, input) || len(doc.Blocks) != 1 {
				t.Fatalf("document changed: %#v", doc)
			}
			got := doc.Blocks[0]
			if got.StartByte != len(prefix) || got.EndByte != len(prefix)+len(block) || got.CaseID != caseA || got.FunctionID != functionA || got.Symbol != "calculateTotal" {
				t.Fatalf("bad block metadata: %#v", got)
			}
			if !bytes.Equal(got.Body, []byte("TEST(Example, Empty) {}"+newline)) {
				t.Fatalf("body = %q", got.Body)
			}
			if !bytes.Equal(doc.Bytes[:got.StartByte], prefix) || !bytes.Equal(doc.Bytes[got.EndByte:], suffix) {
				t.Fatal("unmanaged bytes changed")
			}
			canonical := bytes.ReplaceAll(block, []byte("\r\n"), []byte("\n"))
			sum := sha256.Sum256(canonical)
			if got.Digest != hex.EncodeToString(sum[:]) {
				t.Fatalf("digest = %q", got.Digest)
			}
		})
	}
}

func TestParseDocumentZeroAndManyBlocks(t *testing.T) {
	if doc, err := ParseDocument([]byte("handwritten\n"), 4096, 2); err != nil || len(doc.Blocks) != 0 {
		t.Fatalf("zero: %#v %v", doc, err)
	}
	first := mustRender(t, caseA, "\n")
	second := mustRender(t, caseB, "\n")
	input := append(append(bytes.Clone(first), []byte("handwritten\n")...), second...)
	doc, err := ParseDocument(input, 4096, 2)
	if err != nil || len(doc.Blocks) != 2 || doc.Blocks[1].StartByte != len(first)+len("handwritten\n") {
		t.Fatalf("many: %#v %v", doc, err)
	}
}

func TestParseDocumentOwnsInputAndNormalizesDigestOnly(t *testing.T) {
	lf := mustRender(t, caseA, "\n")
	crlf := mustRender(t, caseA, "\r\n")
	lfDoc, err := ParseDocument(lf, 4096, 1)
	if err != nil {
		t.Fatal(err)
	}
	crlfDoc, err := ParseDocument(crlf, 4096, 1)
	if err != nil {
		t.Fatal(err)
	}
	if lfDoc.Blocks[0].Digest != crlfDoc.Blocks[0].Digest {
		t.Fatal("newline-only change altered canonical digest")
	}
	if bytes.Equal(lfDoc.Bytes, crlfDoc.Bytes) {
		t.Fatal("original newline bytes were not retained")
	}
	before := bytes.Clone(lfDoc.Bytes)
	lf[0] = '!'
	if !bytes.Equal(lfDoc.Bytes, before) {
		t.Fatal("document aliases caller input")
	}
}

func TestParseDocumentLeavesOpaqueUnmanagedBytesExact(t *testing.T) {
	prefix := []byte{0xff, 0xfe, '\n'}
	suffix := []byte{0x80, 0x81}
	block := mustRender(t, caseA, "\n")
	input := append(append(bytes.Clone(prefix), block...), suffix...)
	doc, err := ParseDocument(input, 4096, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(doc.Bytes, input) || !bytes.Equal(doc.Bytes[:doc.Blocks[0].StartByte], prefix) || !bytes.Equal(doc.Bytes[doc.Blocks[0].EndByte:], suffix) {
		t.Fatal("opaque unmanaged bytes changed")
	}
}

func TestParseDocumentRejectsMarkersInsideMultilineLexicalContext(t *testing.T) {
	block := string(mustRender(t, caseA, "\n"))
	tests := map[string]string{
		"block comment":             "/* user comment\n" + block + "*/\n",
		"raw string":                "const char *s = R\"tag(raw text\n" + block + ")tag\";\n",
		"continued ordinary string": "const char *s = \"prefix\\\n" + block + "\";\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDocument([]byte(input), 4096, 1); err == nil {
				t.Fatal("accepted marker inside multiline lexical context")
			}
		})
	}
}

func TestParseDocumentRejectsUnterminatedMultilineContext(t *testing.T) {
	for name, input := range map[string]string{
		"block comment":    "/* unfinished\n",
		"raw string":       "const char *s = R\"tag(unfinished\n",
		"ordinary string":  "const char *s = \"unfinished\n",
		"continued string": "const char *s = \"unfinished\\\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDocument([]byte(input), 4096, 1); err == nil {
				t.Fatal("accepted unterminated lexical context")
			}
		})
	}
}

func TestParseDocumentPreservesUnmanagedMultilineContexts(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(strings.ReplaceAll(newline, "\n", "LF"), func(t *testing.T) {
			prefix := []byte("/* harmless" + newline + "comment */" + newline +
				"const char *raw = R\"tag(first" + newline + "second)tag\";" + newline +
				"const int count = 1'000;" + newline +
				"const char *quoted = \"ordinary string\";" + newline +
				"const char *continued = \"first\\" + newline + "second\";" + newline)
			block := mustRender(t, caseA, newline)
			input := append(bytes.Clone(prefix), block...)
			doc, err := ParseDocument(input, 4096, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(doc.Blocks) != 1 || doc.Blocks[0].StartByte != len(prefix) || !bytes.Equal(doc.Bytes[:len(prefix)], prefix) || !bytes.Equal(doc.Bytes, input) {
				t.Fatal("valid unmanaged lexical context changed")
			}
		})
	}
}

func TestParseDocumentRejectsMalformedMarkers(t *testing.T) {
	base := string(mustRender(t, caseA, "\n"))
	start := strings.Split(base, "\n")[0] + "\n"
	end := "// unit-test-ide:managed-end case=" + caseA + "\n"
	tests := map[string]string{
		"missing end": start + "body\n", "orphan end": end,
		"nested":                   start + start + "body\n" + end + end,
		"duplicate begin":          start + start + end,
		"mismatched end":           strings.Replace(base, end, "// unit-test-ide:managed-end case="+caseB+"\n", 1),
		"duplicate case":           base + base,
		"extra metadata":           strings.Replace(base, " symbol=calculateTotal", " symbol=calculateTotal source=/tmp/private", 1),
		"invalid symbol":           strings.Replace(base, "symbol=calculateTotal", "symbol=../../private", 1),
		"uppercase ID":             strings.Replace(base, caseA, "utc_"+strings.Repeat("A", 32), 1),
		"indented marker":          "  " + base,
		"string literal":           "const char *s = \"" + strings.TrimSpace(start) + "\";\n",
		"truncated marker token":   "// unit-test-ide:managed\n",
		"spaced namespace":         "// unit-test-ide: managed-begin case=" + caseA + "\n",
		"case-shifted namespace":   "// Unit-Test-Ide:managed-begin case=" + caseA + "\n",
		"spaced delimiter":         "// unit-test-ide :managed-begin case=" + caseA + "\n",
		"missing delimiter":        "// unit-test-ide managed-begin case=" + caseA + "\n",
		"trailing marker data":     strings.Replace(base, end, strings.TrimSpace(end)+" garbage\n", 1),
		"NUL":                      base + "\x00",
		"mixed newlines":           strings.Replace(base, "TEST(Example, Empty) {}\n", "TEST(Example, Empty) {}\r\n", 1),
		"bare CR":                  strings.Replace(base, "\n", "\r", 1),
		"unterminated last marker": strings.TrimSuffix(base, "\n"),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDocument([]byte(input), 4096, 2); err == nil {
				t.Fatal("accepted malformed document")
			}
		})
	}
}

func TestParseDocumentLimits(t *testing.T) {
	block := mustRender(t, caseA, "\n")
	if _, err := ParseDocument(block, int64(len(block)-1), 2); err == nil {
		t.Fatal("accepted oversized file")
	}
	if _, err := ParseDocument(block, 4096, 0); err == nil {
		t.Fatal("accepted zero block limit")
	}
	if _, err := ParseDocument(append(bytes.Clone(block), mustRender(t, caseB, "\n")...), 4096, 1); err == nil {
		t.Fatal("accepted excess block")
	}
	if _, err := ParseDocument(block, -1, 2); err == nil {
		t.Fatal("accepted negative byte limit")
	}
	if _, err := ParseDocument(make([]byte, maxDocumentBytes+1), int64(maxDocumentBytes+1), 1); err == nil {
		t.Fatal("accepted input beyond absolute bound")
	}
}

func TestRenderMarkersRejectsUnsafeInput(t *testing.T) {
	tests := []struct {
		name, caseID, functionID, symbol, newline string
		body                                      []byte
	}{
		{"case", "utc_bad", functionA, "safe", "\n", []byte("body\n")},
		{"function", caseA, "bad", "safe", "\n", []byte("body\n")},
		{"symbol", caseA, functionA, "/tmp/private", "\n", []byte("body\n")},
		{"newline", caseA, functionA, "safe", "\r", []byte("body\n")},
		{"body NUL", caseA, functionA, "safe", "\n", []byte("a\x00\n")},
		{"body no newline", caseA, functionA, "safe", "\n", []byte("body")},
		{"body marker", caseA, functionA, "safe", "\n", []byte("// unit-test-ide:managed-end case=" + caseA + "\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := RenderMarkers(tt.caseID, tt.functionID, tt.symbol, tt.body, tt.newline); err == nil {
				t.Fatal("accepted unsafe render")
			}
		})
	}
}

func FuzzParseDocument(f *testing.F) {
	fixture, err := os.ReadFile("testdata/managed_lf.txt")
	if err != nil {
		f.Fatal(err)
	}
	f.Add([]byte(""))
	f.Add([]byte("unmanaged\n"))
	f.Add(fixture)
	f.Add(bytes.ReplaceAll(fixture, []byte("\n"), []byte("\r\n")))
	f.Add([]byte("// unit-test-ide:managed-begin bad\n"))
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 8192 {
			t.Skip()
		}
		doc, err := ParseDocument(input, 8192, 32)
		if err != nil {
			return
		}
		if !bytes.Equal(doc.Bytes, input) {
			t.Fatal("parsed bytes differ")
		}
		previous := 0
		for _, block := range doc.Blocks {
			if block.StartByte < previous || block.EndByte > len(input) || block.StartByte >= block.EndByte {
				t.Fatal("invalid offsets")
			}
			previous = block.EndByte
		}
	})
}
