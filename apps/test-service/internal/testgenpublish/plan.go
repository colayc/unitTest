package testgenpublish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/testgenrender"
)

var ErrInvalidPlan = errors.New("invalid test publication plan")
var ErrConflict = errors.New("test publication conflict")

const maxEditBytes = 4 << 20
const maxTotalEditBytes = 16 << 20
const maxFiles = 128

type CandidateSet struct {
	RunID, SnapshotDigest                         string
	CaseIDs, CharacterizationIDs                  []string
	TestTarget, ProductionTarget, FrameworkTarget string
	SymbolID                                      string
	Files                                         []testgenrender.StagedFile
	Diff                                          string
}

type PlannedEdit struct{ Path, BeforeDigest, AfterDigest string }
type PublishPlan struct {
	RunID, CandidateSetDigest, SnapshotDigest                    string
	Diff, DiffDigest, ConfirmationDigest, CharacterizationDigest string
	Edits                                                        []PlannedEdit
}

type preparedFile struct {
	edit    PlannedEdit
	after   []byte
	before  []byte
	mode    os.FileMode
	existed bool
}
type preparedPlan struct {
	public PublishPlan
	files  []preparedFile
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func validHex(value string, n int) bool {
	if len(value) != n {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]{0,127}$`)

func validRelative(s string) bool {
	if s == "" || len(s) > 240 || path.IsAbs(s) || strings.ContainsAny(s, "\\:\x00\r\n\"<>|?*") || strings.Contains(s, "//") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		upper := strings.ToUpper(strings.Split(part, ".")[0])
		if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" || upper == "CONIN$" || upper == "CONOUT$" || len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9' {
			return false
		}
	}
	for _, c := range []byte(s) {
		if c < 0x20 || c == 0x7f || strings.ContainsRune("$;[]", rune(c)) {
			return false
		}
	}
	return true
}
func generatedTestPath(s string) bool {
	return validRelative(s) && strings.HasPrefix(s, "tests/generated/") && (strings.HasSuffix(s, "_test.c") || strings.HasSuffix(s, "_test.cpp"))
}
func cmakePath(s string) bool {
	return validRelative(s) && strings.HasPrefix(s, "tests/") && path.Base(s) == "CMakeLists.txt"
}

func (p *Publisher) Plan(ctx context.Context, set CandidateSet) (PublishPlan, error) {
	if p == nil || ctx == nil || p.root == nil || p.verify == nil || !validHex(set.RunID, 32) || !validHex(set.SnapshotDigest, 64) || len(set.CaseIDs) == 0 || len(set.CaseIDs) > 1000 || len(set.Files) < 2 || len(set.Files) > maxFiles || !identifier.MatchString(set.TestTarget) || !identifier.MatchString(set.ProductionTarget) || !identifier.MatchString(set.FrameworkTarget) || set.TestTarget == set.ProductionTarget || set.TestTarget == set.FrameworkTarget || set.ProductionTarget == set.FrameworkTarget || (set.FrameworkTarget != "CppUTest" && set.FrameworkTarget != "Unity") || (set.FrameworkTarget == "Unity" && !validHex(set.SymbolID, 64)) {
		return PublishPlan{}, ErrInvalidPlan
	}
	if err := ctx.Err(); err != nil {
		return PublishPlan{}, err
	}
	if err := p.verify(ctx, set.SnapshotDigest); err != nil {
		return PublishPlan{}, ErrConflict
	}
	caseSeen := map[string]bool{}
	for _, id := range set.CaseIDs {
		if !validHex(id, 32) || caseSeen[id] {
			return PublishPlan{}, ErrInvalidPlan
		}
		caseSeen[id] = true
	}
	chars := append([]string(nil), set.CharacterizationIDs...)
	charSeen := map[string]bool{}
	for _, id := range chars {
		if !caseSeen[id] || charSeen[id] {
			return PublishPlan{}, ErrInvalidPlan
		}
		charSeen[id] = true
	}
	sort.Strings(chars)
	files := make([]preparedFile, 0, len(set.Files))
	seen := map[string]bool{}
	var cmakeIndex = -1
	var totalBytes int
	for _, edit := range set.Files {
		if err := ctx.Err(); err != nil {
			return PublishPlan{}, err
		}
		if !generatedTestPath(edit.Path) && !cmakePath(edit.Path) || seen[strings.ToLower(edit.Path)] || len(edit.Content) == 0 || len(edit.Content) > maxEditBytes || len(edit.Content) > maxTotalEditBytes-totalBytes || digest(edit.Content) != edit.AfterDigest {
			return PublishPlan{}, ErrInvalidPlan
		}
		totalBytes += len(edit.Content)
		seen[strings.ToLower(edit.Path)] = true
		if cmakePath(edit.Path) {
			if cmakeIndex >= 0 || !validHex(edit.BeforeDigest, 64) {
				return PublishPlan{}, ErrInvalidPlan
			}
			cmakeIndex = len(files)
		}
		before, mode, exists, err := p.readTarget(edit.Path)
		if err != nil {
			return PublishPlan{}, err
		}
		if exists != (edit.BeforeDigest != "") || exists && digest(before) != edit.BeforeDigest {
			return PublishPlan{}, conflictReceipt(edit.Path, edit.BeforeDigest, before, exists)
		}
		if exists && digest(before) == edit.AfterDigest {
			return PublishPlan{}, ErrInvalidPlan
		}
		files = append(files, preparedFile{edit: PlannedEdit{Path: edit.Path, BeforeDigest: edit.BeforeDigest, AfterDigest: edit.AfterDigest}, after: append([]byte(nil), edit.Content...), before: before, mode: mode, existed: exists})
	}
	if cmakeIndex < 0 {
		return PublishPlan{}, ErrInvalidPlan
	}
	cmake := files[cmakeIndex]
	sources := []string{}
	for i, file := range files {
		if i == cmakeIndex {
			continue
		}
		if file.existed && file.mode&0222 == 0 {
			return PublishPlan{}, ErrConflict
		}
		if set.FrameworkTarget == "Unity" && !strings.HasSuffix(file.edit.Path, ".c") || set.FrameworkTarget == "CppUTest" && !strings.HasSuffix(file.edit.Path, ".cpp") {
			return PublishPlan{}, ErrInvalidPlan
		}
		ref, ok := cmakeSourceRef(cmake.edit.Path, file.edit.Path)
		if !ok {
			return PublishPlan{}, ErrInvalidPlan
		}
		sources = append(sources, ref)
	}
	if !validCMakePatch(string(cmake.before), string(cmake.after), set, sources) {
		return PublishPlan{}, ErrInvalidPlan
	}
	sort.Slice(files, func(i, j int) bool { return files[i].edit.Path < files[j].edit.Path })
	var diff strings.Builder
	edits := make([]PlannedEdit, 0, len(files))
	for _, file := range files {
		edits = append(edits, file.edit)
		diff.WriteString(unified(file.edit.Path, string(file.before), string(file.after)))
	}
	if set.Diff != "" && set.Diff != diff.String() {
		return PublishPlan{}, ErrInvalidPlan
	}
	charDigest := ""
	if len(chars) > 0 {
		encoded, _ := json.Marshal(chars)
		charDigest = digest(encoded)
	}
	identity := struct {
		RunID, Snapshot, TestTarget, ProductionTarget, FrameworkTarget, SymbolID string
		CaseIDs                                                                  []string
		Edits                                                                    []PlannedEdit
		CharacterizationDigest                                                   string
	}{set.RunID, set.SnapshotDigest, set.TestTarget, set.ProductionTarget, set.FrameworkTarget, set.SymbolID, append([]string(nil), set.CaseIDs...), edits, charDigest}
	sort.Strings(identity.CaseIDs)
	encoded, _ := json.Marshal(identity)
	setDigest := digest(encoded)
	public := PublishPlan{RunID: set.RunID, CandidateSetDigest: setDigest, SnapshotDigest: set.SnapshotDigest, Diff: diff.String(), DiffDigest: digest([]byte(diff.String())), CharacterizationDigest: charDigest, Edits: edits}
	confirmation, _ := json.Marshal(struct{ RunID, CandidateSetDigest, SnapshotDigest, DiffDigest, CharacterizationDigest string }{public.RunID, public.CandidateSetDigest, public.SnapshotDigest, public.DiffDigest, public.CharacterizationDigest})
	public.ConfirmationDigest = digest(confirmation)
	p.mu.Lock()
	p.plans[public.ConfirmationDigest] = preparedPlan{public: public, files: files}
	p.mu.Unlock()
	return public, nil
}

func cmakeSourceRef(cmake, test string) (string, bool) {
	base := path.Dir(cmake)
	if !strings.HasPrefix(test, base+"/") {
		return "", false
	}
	return strings.TrimPrefix(test, base+"/"), true
}
func validCMakePatch(before, after string, set CandidateSet, sources []string) bool {
	if set.FrameworkTarget == "Unity" && len(sources) != 1 {
		return false
	}
	if len(before) > 1<<20 || len(after) > 1<<20 || !strings.Contains(before, "add_executable("+set.TestTarget+" ") || !strings.Contains(before, "target_link_libraries("+set.TestTarget+" ") || !strings.Contains(before, set.ProductionTarget) || !strings.Contains(before, set.FrameworkTarget) {
		return false
	}
	// The original CMake bytes must remain a subsequence of complete lines.
	old := strings.SplitAfter(before, "\n")
	newLines := strings.SplitAfter(after, "\n")
	added := []string{}
	at := 0
	for _, line := range newLines {
		if at < len(old) && line == old[at] {
			at++
		} else if line != "" {
			added = append(added, strings.TrimSuffix(line, "\n"))
		}
	}
	if at != len(old) || len(added) == 0 {
		return false
	}
	allowed := map[string]int{}
	for _, source := range sources {
		if set.FrameworkTarget == "Unity" {
			generated := set.TestTarget + "_generated_" + set.SymbolID[:12]
			allowed[fmt.Sprintf("add_executable(%s \"%s\")", generated, source)]++
			allowed[fmt.Sprintf("target_link_libraries(%s PRIVATE %s %s)", generated, set.ProductionTarget, set.FrameworkTarget)]++
			allowed[fmt.Sprintf("add_test(NAME %s COMMAND %s)", generated, generated)]++
		} else {
			allowed[fmt.Sprintf("target_sources(%s PRIVATE \"%s\")", set.TestTarget, source)]++
		}
	}
	for _, line := range added {
		if allowed[line] <= 0 {
			return false
		}
		allowed[line]--
	}
	for _, left := range allowed {
		if left != 0 {
			return false
		}
	}
	return true
}

func unified(name, before, after string) string {
	if before == after {
		return ""
	}
	old := strings.Split(strings.TrimSuffix(before, "\n"), "\n")
	if before == "" {
		old = nil
	}
	newLines := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	if after == "" {
		newLines = nil
	}
	var b strings.Builder
	start := 0
	if len(old) > 0 {
		start = 1
	}
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n@@ -%d,%d +1,%d @@\n", name, name, start, len(old), len(newLines))
	for _, line := range old {
		b.WriteString("-" + line + "\n")
	}
	for _, line := range newLines {
		b.WriteString("+" + line + "\n")
	}
	return b.String()
}
