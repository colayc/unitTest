package coveragenormalize

import (
	"errors"
	"path/filepath"
	"sort"
)

var ErrDuplicateSource = errors.New("duplicate coverage source")

type sourceCandidate struct {
	path    string
	binding SourceBinding
}

// CollectSources validates, filters, sorts, and digests a bounded set of
// workspace-native source candidates. The matcher is evaluated against the
// canonical workspace-relative slash path; the returned URI remains escaped
// for protocol/public use.
func CollectSources(workspaceRoot string, nativePaths []string, matcher *GlobMatcher, limits Limits) ([]SourceBinding, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	if matcher == nil {
		return nil, ErrInvalidGlob
	}
	if int64(len(nativePaths)) > limits.MaxFiles {
		return nil, ErrLimitExceeded
	}
	candidates := make([]sourceCandidate, 0, len(nativePaths))
	seen := make(map[string]struct{}, len(nativePaths))
	for _, nativePath := range nativePaths {
		binding, err := BindSourcePath(workspaceRoot, nativePath)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(workspaceRoot, nativePath)
		if err != nil || relative == "." {
			return nil, ErrInvalidSourcePath
		}
		relative = filepath.ToSlash(relative)
		if !matcher.Include(relative) {
			continue
		}
		if _, duplicate := seen[binding.URI]; duplicate {
			return nil, ErrDuplicateSource
		}
		seen[binding.URI] = struct{}{}
		candidates = append(candidates, sourceCandidate{path: nativePath, binding: binding})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].binding.URI == candidates[j].binding.URI {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].binding.URI < candidates[j].binding.URI
	})
	result := make([]SourceBinding, 0, len(candidates))
	for _, candidate := range candidates {
		binding, err := DigestSource(workspaceRoot, candidate.path, limits)
		if err != nil {
			return nil, err
		}
		result = append(result, binding)
	}
	return result, nil
}
