package coveragereport

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragenormalize"
)

func TestHTMLIsOfflineCSPBoundAndEscaped(t *testing.T) {
	input := reportFixture(t)
	path := t.TempDir() + "/source.cpp"
	content := []byte("\n\n\n</style><script>alert(1)</script>")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	input.Document.Files[0].Sha256 = fmt.Sprintf("%x", sum)
	input.Sources[0].SHA256 = input.Document.Files[0].Sha256
	input.Sources[0].NativePath = path
	coverage, err := encodeFixtureDocument(input.Document)
	if err != nil {
		t.Fatal(err)
	}
	input.CoverageJSON = coverage
	set, err := Render(input)
	if err != nil {
		t.Fatal(err)
	}
	const csp = `default-src 'none'; img-src data:; style-src 'sha256-M8dIDePpmq1uziHlvoBCVJcWNZdVurNhgMktIvjnME8='; script-src 'none'; object-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'`
	if !bytes.Contains(set.CoverageHTML, []byte(csp)) {
		t.Fatalf("missing exact CSP:\n%s", set.CoverageHTML)
	}
	cssHash := sha256.Sum256([]byte(reportCSS))
	if got := base64.StdEncoding.EncodeToString(cssHash[:]); got != "M8dIDePpmq1uziHlvoBCVJcWNZdVurNhgMktIvjnME8=" {
		t.Fatalf("CSS digest = %q", got)
	}
	for _, forbidden := range []string{"<script", "<object", "<frame", "<form", "<base", "http://", "https://", "@font-face", "sourceMappingURL", `</style><script>`} {
		if bytes.Contains(bytes.ToLower(set.CoverageHTML), []byte(strings.ToLower(forbidden))) {
			t.Fatalf("HTML contains forbidden %q", forbidden)
		}
	}
	if !bytes.Contains(set.CoverageHTML, []byte(`&lt;/style&gt;&lt;script&gt;alert(1)&lt;/script&gt;`)) {
		t.Fatalf("untrusted text was not escaped:\n%s", set.CoverageHTML)
	}
}

func TestHTMLSourceStalenessDegradesToMetadataOnly(t *testing.T) {
	input := reportFixture(t)
	input.Sources[0].SHA256 = strings.Repeat("0", 64)
	set, err := Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(set.CoverageHTML, []byte("metadata only")) {
		t.Fatalf("stale source did not degrade:\n%s", set.CoverageHTML)
	}
}

func TestHTMLBinaryAndOversizedSourcesDegradeToMetadataOnly(t *testing.T) {
	for name, content := range map[string][]byte{
		"binary":    []byte{'a', 0, 'b'},
		"oversized": bytes.Repeat([]byte{'a'}, maxSourceTextBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			path := t.TempDir() + "/source.cpp"
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(content)
			if source, state := sourceContent(coveragenormalize.SourceBinding{URI: "src/example.cpp", SHA256: fmt.Sprintf("%x", sum), NativePath: path}, fmt.Sprintf("%x", sum)); source != "" || state != "metadata only" {
				t.Fatalf("sourceContent() = %q, %q", source, state)
			}
		})
	}
}
