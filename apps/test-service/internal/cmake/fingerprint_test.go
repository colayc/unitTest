package cmake

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestFileAPIReplyCapturesCacheInputsAndEveryConsumedReplyFileState(t *testing.T) {
	fixture := newFileAPIReplyFixture(t)
	reply := readWithFixture(t, fixture)

	if reply.Cache.Path != filepath.Join(fixture.buildDir, "CMakeCache.txt") ||
		reply.Cache.Identity == "" || len(reply.Cache.SHA256) != 64 {
		t.Fatalf("Cache = %#v, want canonical CMakeCache identity and digest", reply.Cache)
	}
	wantReplyNames := []string{
		"cache-v2.json",
		"cmakeFiles-v1.json",
		"codemodel-v2.json",
		"index-2026-07-26.json",
		"target-app-Debug.json",
		"target-support-Debug.json",
		"toolchains-v1.json",
	}
	if got := stateBaseNames(reply.StateFiles); !reflect.DeepEqual(got, wantReplyNames) {
		t.Fatalf("reply state names = %#v, want every consumed graph file %#v", got, wantReplyNames)
	}
	wantInputPaths := []string{
		filepath.Join(fixture.buildDir, "CMakeFiles", "4.3.4", "CMakeSystem.cmake"),
		filepath.Join(fixture.sourceDir, "CMakeLists.txt"),
		filepath.Join(fixture.sourceDir, "cmake", "options.cmake"),
	}
	sort.Strings(wantInputPaths)
	gotInputPaths := make([]string, len(reply.CMakeInputStates))
	for index, state := range reply.CMakeInputStates {
		gotInputPaths[index] = state.Path
	}
	if !reflect.DeepEqual(gotInputPaths, wantInputPaths) {
		t.Fatalf("CMake input state paths = %#v, want %#v", gotInputPaths, wantInputPaths)
	}
	for _, states := range [][]FingerprintFile{reply.StateFiles, reply.CMakeInputStates} {
		if !sort.SliceIsSorted(states, func(first, second int) bool {
			return states[first].Path < states[second].Path
		}) {
			t.Fatalf("states are not sorted: %#v", states)
		}
		for index, state := range states {
			if state.Path == "" || state.Identity == "" || len(state.SHA256) != 64 {
				t.Fatalf("state[%d] = %#v, want canonical path, identity, SHA-256", index, state)
			}
		}
	}

	cacheBefore := reply.Cache
	if err := os.WriteFile(
		filepath.Join(fixture.buildDir, "CMakeCache.txt"),
		[]byte("CMAKE_BUILD_TYPE:STRING=Release\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	reply = readWithFixture(t, fixture)
	if reply.Cache.SHA256 == cacheBefore.SHA256 {
		t.Fatalf("cache content change kept state %#v", reply.Cache)
	}

	inputBefore := stateByBaseName(t, reply.CMakeInputStates, "options.cmake")
	if err := os.WriteFile(
		filepath.Join(fixture.sourceDir, "cmake", "options.cmake"),
		[]byte("set(FIXTURE OFF)\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	reply = readWithFixture(t, fixture)
	inputAfter := stateByBaseName(t, reply.CMakeInputStates, "options.cmake")
	if inputAfter.SHA256 == inputBefore.SHA256 {
		t.Fatalf("CMake input content change kept state %#v", inputAfter)
	}

	targetBefore := stateByBaseName(t, reply.StateFiles, "target-app-Debug.json")
	mutateJSONFile(t, filepath.Join(fixture.replyDir, "target-app-Debug.json"), func(value map[string]any) {
		value["futureStateField"] = "changed"
	})
	reply = readWithFixture(t, fixture)
	targetAfter := stateByBaseName(t, reply.StateFiles, "target-app-Debug.json")
	if targetAfter.SHA256 == targetBefore.SHA256 {
		t.Fatalf("target reply content change kept state %#v", targetAfter)
	}
}

func TestFileAPIReplyFailsClosedWhenKnownCMakeInputCannotBeSnapshotted(t *testing.T) {
	fixture := newFileAPIReplyFixture(t)
	if err := os.Remove(filepath.Join(fixture.sourceDir, "cmake", "options.cmake")); err != nil {
		t.Fatal(err)
	}
	expectFileAPIReadError(t, fixture)
}

func TestConfigureFingerprintIsCanonicalAndTracksConfigureGraph(t *testing.T) {
	base := validFingerprintInputForTest()
	first := ConfigureFingerprint(base)
	if len(first) != 64 || strings.ToLower(first) != first {
		t.Fatalf("ConfigureFingerprint() = %q, want lowercase SHA-256", first)
	}

	reordered := cloneProfileFingerprintInput(base)
	reordered.PresetInputs = []FingerprintFile{
		base.PresetInputs[1],
		base.PresetInputs[0],
		base.PresetInputs[0],
	}
	reordered.CMakeInputStates = []FingerprintFile{
		base.CMakeInputStates[1],
		base.CMakeInputStates[0],
	}
	reordered.FileAPIState = append(
		[]FingerprintFile{base.FileAPIState[1], base.FileAPIState[0]},
		base.FileAPIState[0],
	)
	if got := ConfigureFingerprint(reordered); got != first {
		t.Fatalf("equivalent ordering/duplicates changed fingerprint: %q != %q", got, first)
	}

	mutations := []struct {
		name   string
		mutate func(*ProfileFingerprintInput)
	}{
		{"workspace generation", func(value *ProfileFingerprintInput) { value.WorkspaceGeneration = strings.Repeat("9", 64) }},
		{"project", func(value *ProfileFingerprintInput) { value.Profile.ProjectID = "other" }},
		{"profile", func(value *ProfileFingerprintInput) { value.Profile.ID = strings.Repeat("b", 64) }},
		{"preset", func(value *ProfileFingerprintInput) { value.Profile.ConfigurePreset = "release" }},
		{"generator", func(value *ProfileFingerprintInput) { value.Profile.Generator = "Unix Makefiles" }},
		{"configuration", func(value *ProfileFingerprintInput) { value.Profile.Configuration = "Release" }},
		{"binary directory", func(value *ProfileFingerprintInput) { value.Profile.BinaryDir = "out/release" }},
		{"cmake identity", func(value *ProfileFingerprintInput) { value.CMakeIdentity = strings.Repeat("c", 64) }},
		{"toolchain identity", func(value *ProfileFingerprintInput) { value.ToolchainIdentity = strings.Repeat("d", 64) }},
		{"preset content", func(value *ProfileFingerprintInput) { value.PresetInputs[0].SHA256 = strings.Repeat("e", 64) }},
		{"preset file identity", func(value *ProfileFingerprintInput) { value.PresetInputs[0].Identity = "preset-two" }},
		{"cmake input content", func(value *ProfileFingerprintInput) { value.CMakeInputStates[0].SHA256 = strings.Repeat("f", 64) }},
		{"cmake input identity", func(value *ProfileFingerprintInput) { value.CMakeInputStates[0].Identity = "input-two" }},
		{"cache content", func(value *ProfileFingerprintInput) { value.Cache.SHA256 = strings.Repeat("1", 64) }},
		{"cache identity", func(value *ProfileFingerprintInput) { value.Cache.Identity = "cache-two" }},
		{"reply content", func(value *ProfileFingerprintInput) { value.FileAPIState[0].SHA256 = strings.Repeat("2", 64) }},
		{"reply identity", func(value *ProfileFingerprintInput) { value.FileAPIState[0].Identity = "reply-two" }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := cloneProfileFingerprintInput(base)
			mutation.mutate(&changed)
			if got := ConfigureFingerprint(changed); got == first {
				t.Fatalf("semantic mutation kept fingerprint %q", got)
			}
		})
	}
}

func TestNeedsConfigureFailsClosedForStateAndConflictingFileIdentity(t *testing.T) {
	current := validFingerprintInputForTest()
	previous := BuildConfiguration{
		Fingerprint: ConfigureFingerprint(current),
		Succeeded:   true,
	}
	if NeedsConfigure(previous, current) {
		t.Fatal("NeedsConfigure() = true for unchanged successful configuration")
	}

	tests := []struct {
		name   string
		mutate func(*BuildConfiguration, *ProfileFingerprintInput)
	}{
		{"previous failed", func(previous *BuildConfiguration, _ *ProfileFingerprintInput) {
			previous.Succeeded = false
		}},
		{"missing cache", func(_ *BuildConfiguration, current *ProfileFingerprintInput) {
			current.Cache = FingerprintFile{}
		}},
		{"missing reply state", func(_ *BuildConfiguration, current *ProfileFingerprintInput) {
			current.FileAPIState = nil
		}},
		{"missing CMake input state", func(_ *BuildConfiguration, current *ProfileFingerprintInput) {
			current.CMakeInputStates = nil
		}},
		{"invalid digest", func(_ *BuildConfiguration, current *ProfileFingerprintInput) {
			current.CMakeInputStates[0].SHA256 = "not-a-sha256"
		}},
		{"preset same path conflict", func(_ *BuildConfiguration, current *ProfileFingerprintInput) {
			conflict := current.PresetInputs[0]
			conflict.SHA256 = strings.Repeat("9", 64)
			current.PresetInputs = append(current.PresetInputs, conflict)
		}},
		{"CMake input same path conflict", func(_ *BuildConfiguration, current *ProfileFingerprintInput) {
			conflict := current.CMakeInputStates[0]
			conflict.Identity = "replacement-file"
			current.CMakeInputStates = append(current.CMakeInputStates, conflict)
		}},
		{"reply same path conflict", func(_ *BuildConfiguration, current *ProfileFingerprintInput) {
			conflict := current.FileAPIState[0]
			conflict.SHA256 = strings.Repeat("8", 64)
			current.FileAPIState = append(current.FileAPIState, conflict)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changedPrevious := previous
			changedCurrent := cloneProfileFingerprintInput(current)
			test.mutate(&changedPrevious, &changedCurrent)
			if !NeedsConfigure(changedPrevious, changedCurrent) {
				t.Fatal("NeedsConfigure() = false, want fail-closed reconfigure")
			}
		})
	}
}

func TestFingerprintStateRejectsCrossCollectionConflictsAndAllowsEquivalentDuplicates(t *testing.T) {
	type location struct {
		name string
		file func(*ProfileFingerprintInput) *FingerprintFile
	}
	locations := []location{
		{
			name: "preset inputs",
			file: func(input *ProfileFingerprintInput) *FingerprintFile {
				return &input.PresetInputs[0]
			},
		},
		{
			name: "CMake inputs",
			file: func(input *ProfileFingerprintInput) *FingerprintFile {
				return &input.CMakeInputStates[0]
			},
		},
		{
			name: "cache",
			file: func(input *ProfileFingerprintInput) *FingerprintFile {
				return &input.Cache
			},
		},
		{
			name: "File API state",
			file: func(input *ProfileFingerprintInput) *FingerprintFile {
				return &input.FileAPIState[0]
			},
		},
	}
	for first := 0; first < len(locations); first++ {
		for second := first + 1; second < len(locations); second++ {
			pairName := locations[first].name + "/" + locations[second].name
			t.Run(pairName+"/conflict", func(t *testing.T) {
				input := cloneProfileFingerprintInput(validFingerprintInputForTest())
				source := *locations[first].file(&input)
				conflict := source
				conflict.Identity += "-replacement"
				*locations[second].file(&input) = conflict
				if validProfileFingerprintInput(input) {
					t.Fatal("validProfileFingerprintInput() = true for same canonical path with conflicting identity")
				}
			})
			t.Run(pairName+"/equivalent duplicate", func(t *testing.T) {
				input := cloneProfileFingerprintInput(validFingerprintInputForTest())
				source := *locations[first].file(&input)
				*locations[second].file(&input) = source
				if !validProfileFingerprintInput(input) {
					t.Fatal("validProfileFingerprintInput() = false for equivalent cross-collection duplicate")
				}
			})
		}
	}
}

func TestOrdinaryCPPContentDoesNotAffectConfigureFingerprint(t *testing.T) {
	source := filepath.Join(t.TempDir(), "ordinary.cpp")
	if err := os.WriteFile(source, []byte("int value = 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := validFingerprintInputForTest()
	before := ConfigureFingerprint(input)

	if err := os.WriteFile(source, []byte("int value = 2;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after := ConfigureFingerprint(input)
	if after != before {
		t.Fatalf("ordinary .cpp content changed fingerprint: %q != %q", after, before)
	}
}

func validFingerprintInputForTest() ProfileFingerprintInput {
	return ProfileFingerprintInput{
		WorkspaceGeneration: strings.Repeat("0", 64),
		Profile: BuildProfile{
			ID:              strings.Repeat("a", 64),
			ProjectID:       "fixture",
			Origin:          "preset",
			ConfigurePreset: "debug",
			BuildPreset:     "debug-build",
			ToolchainID:     "clang",
			Generator:       "Ninja",
			Configuration:   "Debug",
			BinaryDir:       "out/debug",
		},
		CMakeIdentity:     strings.Repeat("a", 64),
		ToolchainIdentity: strings.Repeat("b", 64),
		PresetInputs: []FingerprintFile{
			fingerprintFileForTest("CMakePresets.json", "preset-one", "1"),
			fingerprintFileForTest("included.json", "preset-include", "2"),
		},
		CMakeInputStates: []FingerprintFile{
			fingerprintFileForTest("CMakeLists.txt", "input-one", "3"),
			fingerprintFileForTest("cmake/options.cmake", "input-options", "4"),
		},
		Cache: fingerprintFileForTest("CMakeCache.txt", "cache-one", "5"),
		FileAPIState: []FingerprintFile{
			fingerprintFileForTest("index-fixture.json", "reply-index", "6"),
			fingerprintFileForTest("target-fixture.json", "reply-target", "7"),
		},
	}
}

func fingerprintFileForTest(path, identity, digit string) FingerprintFile {
	return FingerprintFile{
		Path:     path,
		Identity: identity,
		SHA256:   strings.Repeat(digit, 64),
	}
}

func cloneProfileFingerprintInput(input ProfileFingerprintInput) ProfileFingerprintInput {
	result := input
	result.PresetInputs = append([]FingerprintFile(nil), input.PresetInputs...)
	result.CMakeInputStates = append([]FingerprintFile(nil), input.CMakeInputStates...)
	result.FileAPIState = append([]FingerprintFile(nil), input.FileAPIState...)
	return result
}

func stateByBaseName(t *testing.T, states []FingerprintFile, name string) FingerprintFile {
	t.Helper()
	for _, state := range states {
		if filepath.Base(state.Path) == name {
			return state
		}
	}
	t.Fatalf("state %q is missing from %#v", name, states)
	return FingerprintFile{}
}

func stateBaseNames(states []FingerprintFile) []string {
	names := make([]string, len(states))
	for index, state := range states {
		names[index] = filepath.Base(state.Path)
	}
	return names
}
