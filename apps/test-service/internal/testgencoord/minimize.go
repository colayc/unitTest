package testgencoord

import (
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/testgendomain"
)

// Coverage contains stable, path-redacted collector identities rather than
// aggregate counts. Aggregate counts cannot prove branch preservation.
type Coverage struct{ Functions, Lines, Branches []string }

type ValidatedCandidate struct {
	CaseID               string
	Kind                 testgendomain.CandidateKind
	Coverage             Coverage
	AssertionDigest      string
	Complexity           int64
	Locked               bool
	IndependentErrorPath bool
}

func coverageKeys(c Coverage) map[string]struct{} {
	keys := make(map[string]struct{}, len(c.Functions)+len(c.Lines)+len(c.Branches))
	for _, group := range []struct {
		prefix string
		ids    []string
	}{{"f:", c.Functions}, {"l:", c.Lines}, {"b:", c.Branches}} {
		for _, id := range group.ids {
			if validCoverageID(id) {
				keys[group.prefix+id] = struct{}{}
			}
		}
	}
	return keys
}

func validCoverageID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func validMinCandidate(c ValidatedCandidate) bool {
	return validMinHex(c.CaseID, 32) && (c.Kind == testgendomain.KindVerified || c.Kind == testgendomain.KindCharacterization) &&
		validMinHex(c.AssertionDigest, 64) && c.Complexity >= 0 && c.Complexity <= 1_000_000 &&
		len(c.Coverage.Functions)+len(c.Coverage.Lines)+len(c.Coverage.Branches) <= 10000 &&
		!(c.IndependentErrorPath && c.Kind != testgendomain.KindVerified)
}

func validMinHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func quality(c ValidatedCandidate) int {
	if c.Kind == testgendomain.KindVerified {
		return 2
	}
	return 1
}

func better(a, b ValidatedCandidate) bool {
	if quality(a) != quality(b) {
		return quality(a) > quality(b)
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Complexity != b.Complexity {
		return a.Complexity < b.Complexity
	}
	return a.CaseID < b.CaseID
}

// Minimize uses deterministic greedy set-cover followed by redundant-case
// pruning. Locked and independently validated error-path cases are retained.
func Minimize(baseline Coverage, candidates []ValidatedCandidate) []ValidatedCandidate {
	if len(candidates) > 1000 {
		return nil
	}
	base := coverageKeys(baseline)
	valid := make([]ValidatedCandidate, 0, len(candidates))
	all := make(map[string]struct{})
	seen := make(map[string]bool)
	for _, c := range candidates {
		if !validMinCandidate(c) || seen[c.CaseID] {
			return nil
		}
		seen[c.CaseID] = true
		valid = append(valid, c)
		for k := range coverageKeys(c.Coverage) {
			if _, covered := base[k]; !covered {
				all[k] = struct{}{}
			}
		}
	}
	selected := make(map[string]bool)
	covered := make(map[string]struct{})
	for _, c := range valid {
		if c.Locked || c.IndependentErrorPath {
			selected[c.CaseID] = true
			for k := range coverageKeys(c.Coverage) {
				if _, needed := all[k]; needed {
					covered[k] = struct{}{}
				}
			}
		}
	}
	for len(covered) < len(all) {
		best := -1
		bestGain := 0
		for i, c := range valid {
			if selected[c.CaseID] {
				continue
			}
			gain := 0
			for k := range coverageKeys(c.Coverage) {
				if _, needed := all[k]; needed {
					if _, have := covered[k]; !have {
						gain++
					}
				}
			}
			if gain > bestGain || gain == bestGain && gain > 0 && (best < 0 || better(c, valid[best])) {
				best, bestGain = i, gain
			}
		}
		if best < 0 {
			break
		}
		c := valid[best]
		selected[c.CaseID] = true
		for k := range coverageKeys(c.Coverage) {
			if _, needed := all[k]; needed {
				covered[k] = struct{}{}
			}
		}
	}
	// Remove redundant non-mandatory cases in reverse preference order.
	order := append([]ValidatedCandidate(nil), valid...)
	sort.Slice(order, func(i, j int) bool { return better(order[j], order[i]) })
	for _, c := range order {
		if !selected[c.CaseID] || c.Locked || c.IndependentErrorPath {
			continue
		}
		delete(selected, c.CaseID)
		remaining := make(map[string]struct{})
		for _, other := range valid {
			if selected[other.CaseID] {
				for k := range coverageKeys(other.Coverage) {
					remaining[k] = struct{}{}
				}
			}
		}
		for k := range all {
			if _, ok := remaining[k]; !ok {
				selected[c.CaseID] = true
				break
			}
		}
	}
	result := make([]ValidatedCandidate, 0, len(selected))
	for _, c := range valid {
		if selected[c.CaseID] {
			result = append(result, c)
		}
	}
	sort.Slice(result, func(i, j int) bool { return strings.Compare(result[i].CaseID, result[j].CaseID) < 0 })
	return result
}
