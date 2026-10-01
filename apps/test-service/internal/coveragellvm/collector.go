package coveragellvm

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
)

const (
	mergedProfileFileName = "coverage.profdata"
	maxCollectorBinaries  = 127
)

func BuildCollectorInvocation(
	toolset *Toolset,
	manifest Manifest,
	binaries []coveragerun.TrustedPath,
) (merge task.ProcessSpec, export task.ProcessSpec, err error) {
	if toolset == nil || len(manifest.Entries) == 0 ||
		len(binaries) == 0 || len(binaries) > maxCollectorBinaries {
		return task.ProcessSpec{}, task.ProcessSpec{}, ErrInvalidProfiles
	}
	if err := toolset.Verify(); err != nil {
		return task.ProcessSpec{}, task.ProcessSpec{}, err
	}
	root, err := manifest.profileRoot()
	if err != nil {
		return task.ProcessSpec{}, task.ProcessSpec{}, err
	}
	profiles := make([]string, len(manifest.Entries))
	seenProfiles := make(map[string]struct{}, len(profiles))
	for index, entry := range manifest.Entries {
		if filepath.Dir(entry.Path) != root ||
			strings.ToLower(filepath.Ext(entry.Path)) != ".profraw" {
			return task.ProcessSpec{}, task.ProcessSpec{}, ErrInvalidProfiles
		}
		key := profilePathKey(entry.Path)
		if _, duplicate := seenProfiles[key]; duplicate {
			return task.ProcessSpec{}, task.ProcessSpec{}, ErrInvalidProfiles
		}
		seenProfiles[key] = struct{}{}
		profiles[index] = filepath.Base(entry.Path)
	}
	sort.Slice(profiles, func(left, right int) bool {
		return profilePathKey(profiles[left]) < profilePathKey(profiles[right])
	})
	merged := filepath.Join(root, mergedProfileFileName)
	if _, err := os.Lstat(merged); !os.IsNotExist(err) {
		return task.ProcessSpec{}, task.ProcessSpec{}, ErrInvalidProfiles
	}
	paths := make([]string, len(binaries))
	seenBinaries := make(map[string]struct{}, len(paths))
	for index, binary := range binaries {
		path, err := verifiedCollectorPath(binary)
		if err != nil {
			return task.ProcessSpec{}, task.ProcessSpec{}, err
		}
		key := profilePathKey(path)
		if _, duplicate := seenBinaries[key]; duplicate {
			return task.ProcessSpec{}, task.ProcessSpec{}, ErrInvalidProfiles
		}
		seenBinaries[key] = struct{}{}
		paths[index] = path
	}
	additional := append([]coveragerun.TrustedPath(nil), binaries[1:]...)
	sort.Slice(additional, func(left, right int) bool {
		return profilePathKey(additional[left].Path()) < profilePathKey(additional[right].Path())
	})
	unset, err := sanitizedProfileUnset(
		nil,
		inheritedHostileProfileEnvironmentNames(),
		true,
	)
	if err != nil {
		return task.ProcessSpec{}, task.ProcessSpec{}, err
	}
	invocation, err := coveragerun.BuildLLVMInvocation(coveragerun.LLVMInputs{
		Profdata: toolset.Profdata(), Cov: toolset.Cov(), Binary: binaries[0],
		AdditionalBinaries: additional, ProfileDirectory: manifestDirectory{manifest},
		ProfileFiles: profiles, MergedProfile: mergedProfileFileName,
	})
	if err != nil || len(invocation.Merge.Args) > 256 || len(invocation.Export.Args) > 256 {
		return task.ProcessSpec{}, task.ProcessSpec{}, errors.Join(ErrInvalidProfiles, err)
	}
	merge = task.ProcessSpec{
		Executable: invocation.Merge.Executable,
		Args:       invocation.Merge.Args,
		EnvUnset:   append([]string(nil), unset...),
		Dir:        invocation.Merge.Dir,
	}
	export = task.ProcessSpec{
		Executable: invocation.Export.Executable,
		Args:       invocation.Export.Args,
		EnvUnset:   append([]string(nil), unset...),
		Dir:        invocation.Export.Dir,
	}
	if err := toolset.Verify(); err != nil {
		return task.ProcessSpec{}, task.ProcessSpec{}, err
	}
	if err := manifest.Verify(); err != nil {
		return task.ProcessSpec{}, task.ProcessSpec{}, err
	}
	for index, binary := range binaries {
		path, err := verifiedCollectorPath(binary)
		if err != nil || path != paths[index] {
			return task.ProcessSpec{}, task.ProcessSpec{}, errors.Join(
				ErrInvalidProfiles,
				err,
			)
		}
	}
	return merge, export, nil
}

type manifestDirectory struct{ manifest Manifest }

func (directory manifestDirectory) Path() string {
	if directory.manifest.state == nil {
		return ""
	}
	return directory.manifest.state.root
}

func (directory manifestDirectory) Verify() error { return directory.manifest.Verify() }

func verifiedCollectorPath(value coveragerun.TrustedPath) (string, error) {
	if nilCollectorPath(value) {
		return "", ErrInvalidProfiles
	}
	if err := value.Verify(); err != nil {
		return "", errors.Join(ErrInvalidProfiles, err)
	}
	path := value.Path()
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path ||
		strings.ContainsRune(path, '\x00') {
		return "", ErrInvalidProfiles
	}
	return path, nil
}

func nilCollectorPath(value coveragerun.TrustedPath) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
