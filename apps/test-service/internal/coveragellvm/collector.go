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
		profiles[index] = entry.Path
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
	primary := paths[0]
	additional := append([]string(nil), paths[1:]...)
	sort.Slice(additional, func(left, right int) bool {
		return profilePathKey(additional[left]) < profilePathKey(additional[right])
	})
	unset, err := sanitizedProfileUnset(
		nil,
		inheritedHostileProfileEnvironmentNames(),
		true,
	)
	if err != nil {
		return task.ProcessSpec{}, task.ProcessSpec{}, err
	}
	mergeArgs := append([]string{"merge", "-sparse"}, profiles...)
	mergeArgs = append(mergeArgs, "-o", merged)
	exportArgs := []string{
		"export", "-format=text", "-instr-profile=" + merged, primary,
	}
	for _, path := range additional {
		exportArgs = append(exportArgs, "-object", path)
	}
	if len(mergeArgs) > 256 || len(exportArgs) > 256 {
		return task.ProcessSpec{}, task.ProcessSpec{}, ErrInvalidProfiles
	}
	profdata := toolset.Profdata()
	cov := toolset.Cov()
	if err := profdata.Verify(); err != nil {
		return task.ProcessSpec{}, task.ProcessSpec{}, err
	}
	if err := cov.Verify(); err != nil {
		return task.ProcessSpec{}, task.ProcessSpec{}, err
	}
	merge = task.ProcessSpec{
		Executable: profdata.Path(),
		Args:       mergeArgs,
		EnvUnset:   append([]string(nil), unset...),
		Dir:        root,
	}
	export = task.ProcessSpec{
		Executable: cov.Path(),
		Args:       exportArgs,
		EnvUnset:   append([]string(nil), unset...),
		Dir:        root,
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
