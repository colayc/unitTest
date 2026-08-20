package coveragenormalize

import (
	"errors"
	"sort"
)

var ErrDuplicateSource = errors.New("duplicate coverage source")

type sourceCandidate struct {
	binding    SourceBinding
	identity   physicalSourceID
	inputIndex int
}

// CollectSources validates, filters, sorts, and digests a bounded set of
// workspace-native source candidates. The matcher is evaluated against the
// canonical workspace-relative slash path; the returned URI remains escaped
// for protocol/public use.
func CollectSources(workspaceRoot string, nativePaths []string, matcher *GlobMatcher, limits Limits) ([]SourceBinding, error) {
	collected, err := collectSources(workspaceRoot, nativePaths, matcher, limits)
	if err != nil {
		return nil, err
	}
	result := make([]SourceBinding, len(collected))
	for index := range collected {
		result[index] = collected[index].binding
	}
	return result, nil
}

func collectSources(workspaceRoot string, nativePaths []string, matcher *GlobMatcher, limits Limits) ([]sourceCandidate, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	if matcher == nil {
		return nil, ErrInvalidGlob
	}
	if int64(len(nativePaths)) > limits.MaxFiles {
		return nil, ErrLimitExceeded
	}
	root, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	candidates := make([]sourceCandidate, 0, len(nativePaths))
	seenURI := make(map[string]struct{}, len(nativePaths))
	seenPhysical := make(map[physicalSourceID]struct{}, len(nativePaths))
	for inputIndex, nativePath := range nativePaths {
		path, relative, err := workspaceRelativeSource(root, nativePath)
		if err != nil {
			return nil, err
		}
		if !matcher.Include(relative) {
			continue
		}
		snapshot, err := openSourceSnapshot(root, path, limits)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seenURI[snapshot.binding.URI]; duplicate {
			snapshot.close()
			return nil, ErrDuplicateSource
		}
		if _, duplicate := seenPhysical[snapshot.identity]; duplicate {
			snapshot.close()
			return nil, ErrDuplicateSource
		}
		binding, err := snapshot.digest()
		snapshot.close()
		if err != nil {
			return nil, err
		}
		seenURI[binding.URI] = struct{}{}
		seenPhysical[snapshot.identity] = struct{}{}
		candidates = append(candidates, sourceCandidate{
			binding: binding, identity: snapshot.identity, inputIndex: inputIndex,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].binding.URI < candidates[j].binding.URI
	})
	return candidates, nil
}
