// Package testgenrender renders bound candidate data into staged C/C++ tests.
// It does not edit the workspace or decide whether a test may be published.
package testgenrender

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	assert "unit-test-ide.local/test-service/internal/testgenassert"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

type Language string

const (
	LanguageC   Language = "c"
	LanguageCPP Language = "cpp"
)

type TargetMetadata struct {
	TestTarget       string
	ProductionTarget string
	FrameworkTarget  string
	CMakePath        string
	TestPath         string
	ExistingCMake    string
}
type Case struct {
	Vector      solver.InputVector
	Observation assert.Observation
}
type RenderRequest struct {
	Program    analysis.Program
	SymbolID   string
	Language   Language
	HeaderPath string
	Target     TargetMetadata
	Cases      []Case
}
type StagedFile struct {
	Path         string
	Content      []byte
	BeforeDigest string
	AfterDigest  string
}
type StagedEditSet struct {
	Files     []StagedFile
	Diff      string
	CaseKinds map[string]assert.CandidateKind
}

var ErrInvalidRender = errors.New("unsafe or unsupported render request")
var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func validID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' && r < 'a' || r > 'f' {
			return false
		}
	}
	return true
}
func validPath(s string) bool {
	if s == "" || len(s) > 240 || strings.ContainsAny(s, "\\:\x00\r\n\"$;") || strings.HasPrefix(s, "/") || strings.Contains(s, "//") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}
func hash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func Render(r RenderRequest) (StagedEditSet, error) {
	if !validID(r.SymbolID) || r.Program.Version != analysis.IRVersion || !validID(r.Program.Digest) || len(r.Cases) < 1 || len(r.Cases) > 1000 || !validPath(r.HeaderPath) || !validPath(r.Target.TestPath) || !validPath(r.Target.CMakePath) || !strings.HasPrefix(r.Target.TestPath, "tests/") || !strings.HasPrefix(r.Target.CMakePath, "tests/") || !identifierPattern.MatchString(r.Target.TestTarget) || !identifierPattern.MatchString(r.Target.ProductionTarget) || !identifierPattern.MatchString(r.Target.FrameworkTarget) || r.Target.TestTarget == r.Target.ProductionTarget || r.Target.FrameworkTarget == r.Target.ProductionTarget || r.Target.FrameworkTarget == r.Target.TestTarget || forbiddenName(r.Target.FrameworkTarget) {
		return StagedEditSet{}, ErrInvalidRender
	}
	if r.Language != LanguageC && r.Language != LanguageCPP {
		return StagedEditSet{}, ErrInvalidRender
	}
	if r.Language == LanguageC && !strings.HasSuffix(r.Target.TestPath, ".c") || r.Language == LanguageCPP && !strings.HasSuffix(r.Target.TestPath, ".cpp") || !strings.HasSuffix(r.Target.CMakePath, "CMakeLists.txt") {
		return StagedEditSet{}, ErrInvalidRender
	}
	var fn *analysis.Function
	for i := range r.Program.Functions {
		if r.Program.Functions[i].SymbolID == r.SymbolID {
			if fn != nil {
				return StagedEditSet{}, ErrInvalidRender
			}
			fn = &r.Program.Functions[i]
		}
	}
	if fn == nil || !identifierPattern.MatchString(fn.Name) || fn.Decision.Kind != analysis.DecisionSupported {
		return StagedEditSet{}, ErrInvalidRender
	}
	cases := make([]derivedCase, 0, len(r.Cases))
	seen := map[string]bool{}
	kinds := map[string]assert.CandidateKind{}
	for _, c := range r.Cases {
		if !validID(c.Vector.ID) || seen[c.Vector.ID] || c.Observation.SymbolID != r.SymbolID {
			return StagedEditSet{}, ErrInvalidRender
		}
		seen[c.Vector.ID] = true
		assertions, kind, err := assert.Derive(r.Program, c.Vector, c.Observation)
		if err != nil || len(assertions) == 0 {
			return StagedEditSet{}, ErrInvalidRender
		}
		cases = append(cases, derivedCase{vector: c.Vector, assertions: assertions, kind: kind})
		kinds[c.Vector.ID] = kind
	}
	var source string
	var err error
	if r.Language == LanguageC {
		source, err = renderUnity(r, *fn, cases)
	} else {
		source, err = renderCppUTest(r, *fn, cases)
	}
	if err != nil {
		return StagedEditSet{}, err
	}
	cmake, err := patchCMake(r.Target, r.Language, r.SymbolID)
	if err != nil {
		return StagedEditSet{}, err
	}
	files := []StagedFile{{Path: r.Target.TestPath, Content: []byte(source), AfterDigest: hash([]byte(source))}, {Path: r.Target.CMakePath, Content: []byte(cmake), BeforeDigest: hash([]byte(r.Target.ExistingCMake)), AfterDigest: hash([]byte(cmake))}}
	return StagedEditSet{Files: files, Diff: unified(r.Target.TestPath, "", source) + unified(r.Target.CMakePath, r.Target.ExistingCMake, cmake), CaseKinds: kinds}, nil
}
func forbiddenName(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "mock") || strings.Contains(lower, "stub")
}

type derivedCase struct {
	vector     solver.InputVector
	assertions []assert.Assertion
	kind       assert.CandidateKind
}

func unified(path, before, after string) string {
	if before == after {
		return ""
	}
	oldLines := strings.Split(strings.TrimSuffix(before, "\n"), "\n")
	if before == "" {
		oldLines = nil
	}
	newLines := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	if after == "" {
		newLines = nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n@@ -%d,%d +1,%d @@\n", path, path, func() int {
		if len(oldLines) == 0 {
			return 0
		}
		return 1
	}(), len(oldLines), len(newLines))
	for _, line := range oldLines {
		b.WriteString("-" + line + "\n")
	}
	for _, line := range newLines {
		b.WriteString("+" + line + "\n")
	}
	return b.String()
}
