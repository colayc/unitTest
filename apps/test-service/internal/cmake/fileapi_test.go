package cmake

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/workspace"
)

func TestWriteQueryPublishesFixedStatefulRequest(t *testing.T) {
	buildDir := t.TempDir()

	if err := WriteQuery(buildDir); err != nil {
		t.Fatalf("WriteQuery() error = %v", err)
	}

	queryPath := filepath.Join(
		buildDir,
		".cmake",
		"api",
		"v1",
		"query",
		"client-unit-test-ide",
		"query.json",
	)
	data, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatalf("ReadFile(query.json) error = %v", err)
	}
	var got struct {
		Requests []struct {
			Kind    string `json:"kind"`
			Version struct {
				Major int `json:"major"`
			} `json:"version"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("query JSON error = %v\n%s", err, data)
	}
	want := []struct {
		Kind    string `json:"kind"`
		Version struct {
			Major int `json:"major"`
		} `json:"version"`
	}{
		queryRequestForTest("codemodel", 2),
		queryRequestForTest("cache", 2),
		queryRequestForTest("cmakeFiles", 1),
		queryRequestForTest("toolchains", 1),
	}
	if !reflect.DeepEqual(got.Requests, want) {
		t.Fatalf("requests = %#v, want %#v", got.Requests, want)
	}
	if leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(queryPath), ".query-*.tmp")); err != nil {
		t.Fatal(err)
	} else if len(leftovers) != 0 {
		t.Fatalf("temporary query files remain: %#v", leftovers)
	}
}

func TestWriteQueryAtomicallyReplacesExistingQuery(t *testing.T) {
	buildDir := t.TempDir()
	queryDir := filepath.Join(buildDir, ".cmake", "api", "v1", "query", "client-unit-test-ide")
	if err := os.MkdirAll(queryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	queryPath := filepath.Join(queryDir, "query.json")
	if err := os.WriteFile(queryPath, []byte(`{"requests":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteQuery(buildDir); err != nil {
		t.Fatalf("WriteQuery() error = %v", err)
	}

	data, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Requests []json.RawMessage `json:"requests"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("replacement is not complete JSON: %v", err)
	}
	if len(decoded.Requests) != 4 {
		t.Fatalf("replacement request count = %d, want 4", len(decoded.Requests))
	}
}

func TestWriteQueryFailureCleansTemporaryFileAndPreservesDestination(t *testing.T) {
	buildDir := t.TempDir()
	queryDir := filepath.Join(buildDir, ".cmake", "api", "v1", "query", "client-unit-test-ide")
	queryPath := filepath.Join(queryDir, "query.json")
	if err := os.MkdirAll(queryPath, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := WriteQuery(buildDir); err == nil {
		t.Fatal("WriteQuery() error = nil, want atomic replace failure")
	}
	info, err := os.Stat(queryPath)
	if err != nil {
		t.Fatalf("destination was removed: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("destination mode = %v, want preserved directory", info.Mode())
	}
	if leftovers, err := filepath.Glob(filepath.Join(queryDir, ".query-*.tmp")); err != nil {
		t.Fatal(err)
	} else if len(leftovers) != 0 {
		t.Fatalf("temporary query files remain after failure: %#v", leftovers)
	}
}

func TestWriteQueryInjectedPublishFailurePreservesOldQuery(t *testing.T) {
	buildDir := t.TempDir()
	queryDir := filepath.Join(buildDir, ".cmake", "api", "v1", "query", "client-unit-test-ide")
	if err := os.MkdirAll(queryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	queryPath := filepath.Join(queryDir, "query.json")
	oldQuery := []byte(`{"requests":[{"kind":"old"}]}`)
	if err := os.WriteFile(queryPath, oldQuery, 0o600); err != nil {
		t.Fatal(err)
	}
	publishError := errors.New("injected publish failure")

	err := writeQueryWithPublisher(buildDir, func(source, destination string) error {
		if filepath.Dir(source) != queryDir || destination != queryPath {
			t.Fatalf("publish paths = (%q, %q), want same-dir temp and %q", source, destination, queryPath)
		}
		return publishError
	})
	if !errors.Is(err, publishError) {
		t.Fatalf("writeQueryWithPublisher() error = %v, want injected failure", err)
	}
	if got, err := os.ReadFile(queryPath); err != nil {
		t.Fatal(err)
	} else if !reflect.DeepEqual(got, oldQuery) {
		t.Fatalf("old query changed after publish failure: %q", got)
	}
	if leftovers, err := filepath.Glob(filepath.Join(queryDir, ".query-*.tmp")); err != nil {
		t.Fatal(err)
	} else if len(leftovers) != 0 {
		t.Fatalf("temporary query files remain after publish failure: %#v", leftovers)
	}
}

func queryRequestForTest(kind string, major int) struct {
	Kind    string `json:"kind"`
	Version struct {
		Major int `json:"major"`
	} `json:"version"`
} {
	request := struct {
		Kind    string `json:"kind"`
		Version struct {
			Major int `json:"major"`
		} `json:"version"`
	}{Kind: kind}
	request.Version.Major = major
	return request
}

func TestFileAPIReplyFollowsObjectGraphAndReturnsCanonicalTargets(t *testing.T) {
	fixture := newFileAPIReplyFixture(t)

	reply, err := ReadReply(
		fixture.buildDir,
		[]string{fixture.sourceDir, fixture.buildDir},
		fixture.profile,
	)
	if err != nil {
		t.Fatalf("ReadReply() error = %v", err)
	}

	if got, want := reply.Configurations, []string{"Debug"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Configurations = %#v, want %#v", got, want)
	}
	if got, want := targetNames(reply.Targets), []string{"app", "support"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("target names = %#v, want %#v", got, want)
	}
	for _, target := range reply.Targets {
		if len(target.ID) != 64 || target.ID == target.Name || strings.ToLower(target.ID) != target.ID {
			t.Fatalf("target %q ID = %q, want opaque lowercase SHA-256", target.Name, target.ID)
		}
		if target.SourceDir != fixture.sourceDir || target.BuildDir != fixture.buildDir {
			t.Fatalf("target %q paths = (%q, %q)", target.Name, target.SourceDir, target.BuildDir)
		}
	}
	app := reply.Targets[0]
	if got, want := app.Artifacts, []string{filepath.Join(fixture.buildDir, "bin", "app")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("app artifacts = %#v, want sorted and deduplicated %#v", got, want)
	}
	wantInputs := []string{
		filepath.Join(fixture.buildDir, "CMakeFiles", "4.3.4", "CMakeSystem.cmake"),
		filepath.Join(fixture.sourceDir, "CMakeLists.txt"),
		filepath.Join(fixture.sourceDir, "cmake", "options.cmake"),
	}
	sort.Strings(wantInputs)
	if !reflect.DeepEqual(reply.CMakeInputs, wantInputs) {
		t.Fatalf("CMakeInputs = %#v, want %#v", reply.CMakeInputs, wantInputs)
	}
	if len(reply.ToolchainIDs) != 2 || !sort.StringsAreSorted(reply.ToolchainIDs) {
		t.Fatalf("ToolchainIDs = %#v, want two stable sorted identities", reply.ToolchainIDs)
	}
	for _, id := range reply.ToolchainIDs {
		if len(id) != 64 || strings.ToLower(id) != id {
			t.Fatalf("toolchain ID = %q, want lowercase SHA-256", id)
		}
	}

	again, err := ReadReply(
		fixture.buildDir,
		[]string{fixture.buildDir, fixture.sourceDir},
		fixture.profile,
	)
	if err != nil {
		t.Fatalf("ReadReply(again) error = %v", err)
	}
	if !reflect.DeepEqual(again, reply) {
		t.Fatalf("equivalent allowed-root order changed reply:\nfirst  = %#v\nsecond = %#v", reply, again)
	}
}

func TestFileAPITargetIDIncludesProjectProfileConfigurationAndNativeIdentity(t *testing.T) {
	fixture := newFileAPIReplyFixture(t)
	read := func(profile BuildProfile) FileAPIReply {
		t.Helper()
		reply, err := ReadReply(
			fixture.buildDir,
			[]string{fixture.sourceDir, fixture.buildDir},
			profile,
		)
		if err != nil {
			t.Fatalf("ReadReply() error = %v", err)
		}
		return reply
	}

	base := read(fixture.profile)
	changedProject := fixture.profile
	changedProject.ProjectID = "other-project"
	if reflect.DeepEqual(targetIDs(base.Targets), targetIDs(read(changedProject).Targets)) {
		t.Fatal("changing ProjectID did not change target IDs")
	}
	changedProfile := fixture.profile
	changedProfile.ID = strings.Repeat("b", 64)
	if reflect.DeepEqual(targetIDs(base.Targets), targetIDs(read(changedProfile).Targets)) {
		t.Fatal("changing profile ID did not change target IDs")
	}

	mutateJSONFile(t, filepath.Join(fixture.replyDir, "codemodel-v2.json"), func(value map[string]any) {
		configurations := value["configurations"].([]any)
		configurations[0].(map[string]any)["name"] = "RelWithDebInfo"
	})
	changedConfiguration := read(fixture.profile)
	if reflect.DeepEqual(targetIDs(base.Targets), targetIDs(changedConfiguration.Targets)) {
		t.Fatal("changing configuration did not change target IDs")
	}

	fixture = newFileAPIReplyFixture(t)
	base = readWithFixture(t, fixture)
	mutateJSONFile(t, filepath.Join(fixture.replyDir, "codemodel-v2.json"), func(value map[string]any) {
		configurations := value["configurations"].([]any)
		targets := configurations[0].(map[string]any)["targets"].([]any)
		targets[1].(map[string]any)["id"] = "app::@changed"
	})
	mutateJSONFile(t, filepath.Join(fixture.replyDir, "target-app-Debug.json"), func(value map[string]any) {
		value["id"] = "app::@changed"
	})
	changedNative := readWithFixture(t, fixture)
	if reflect.DeepEqual(targetIDs(base.Targets), targetIDs(changedNative.Targets)) {
		t.Fatal("changing native target identity did not change target IDs")
	}
}

func TestFileAPIReplyRequiresOneValidProfileWhenTargetsExist(t *testing.T) {
	fixture := newFileAPIReplyFixture(t)
	tests := []struct {
		name     string
		profiles []BuildProfile
	}{
		{name: "missing"},
		{name: "empty ID", profiles: []BuildProfile{{ProjectID: "fixture"}}},
		{name: "empty project ID", profiles: []BuildProfile{{ID: strings.Repeat("a", 64)}}},
		{name: "ambiguous", profiles: []BuildProfile{fixture.profile, fixture.profile}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ReadReply(
				fixture.buildDir,
				[]string{fixture.sourceDir, fixture.buildDir},
				test.profiles...,
			); err == nil {
				t.Fatal("ReadReply() error = nil, want invalid profile error")
			}
		})
	}
}

func TestFileAPIReplyValidatesCacheObjectEvenThoughItIsNotExposed(t *testing.T) {
	fixture := newFileAPIReplyFixture(t)
	mutateJSONFile(t, filepath.Join(fixture.replyDir, "cache-v2.json"), func(value map[string]any) {
		value["kind"] = "not-cache"
	})

	if _, err := ReadReply(
		fixture.buildDir,
		[]string{fixture.sourceDir, fixture.buildDir},
		fixture.profile,
	); err == nil {
		t.Fatal("ReadReply() error = nil, want cache identity error")
	}
}

func TestFileAPIReplyRejectsMissingOrAmbiguousIndex(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		if err := os.Remove(filepath.Join(fixture.replyDir, "index-2026-07-26.json")); err != nil {
			t.Fatal(err)
		}
		expectFileAPIReadError(t, fixture)
	})
	t.Run("multiple", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		data, err := os.ReadFile(filepath.Join(fixture.replyDir, "index-2026-07-26.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.replyDir, "index-2026-07-27.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		expectFileAPIReadError(t, fixture)
	})
}

func TestFileAPIReplyRejectsUnsupportedOrMismatchedObjectIdentity(t *testing.T) {
	t.Run("unsupported object major", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		mutateJSONFile(t, filepath.Join(fixture.replyDir, "codemodel-v2.json"), func(value map[string]any) {
			value["version"].(map[string]any)["major"] = float64(3)
		})
		expectFileAPIReadError(t, fixture)
	})
	t.Run("duplicate exact object reference", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		mutateJSONFile(t, filepath.Join(fixture.replyDir, "index-2026-07-26.json"), func(value map[string]any) {
			objects := value["objects"].([]any)
			value["objects"] = append(objects, objects[0])
		})
		expectFileAPIReadError(t, fixture)
	})
	t.Run("duplicate supported object kind", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		mutateJSONFile(t, filepath.Join(fixture.replyDir, "index-2026-07-26.json"), func(value map[string]any) {
			objects := value["objects"].([]any)
			value["objects"] = append(objects, map[string]any{
				"kind":     "cache",
				"version":  map[string]any{"major": 2, "minor": 1},
				"jsonFile": "cache-other.json",
			})
		})
		expectFileAPIReadError(t, fixture)
	})
	t.Run("response reference mismatch", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		mutateJSONFile(t, filepath.Join(fixture.replyDir, "index-2026-07-26.json"), func(value map[string]any) {
			reply := value["reply"].(map[string]any)
			client := reply["client-unit-test-ide"].(map[string]any)
			query := client["query.json"].(map[string]any)
			responses := query["responses"].([]any)
			responses[1].(map[string]any)["jsonFile"] = "other-cache.json"
		})
		expectFileAPIReadError(t, fixture)
	})
	t.Run("response error", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		mutateJSONFile(t, filepath.Join(fixture.replyDir, "index-2026-07-26.json"), func(value map[string]any) {
			reply := value["reply"].(map[string]any)
			client := reply["client-unit-test-ide"].(map[string]any)
			query := client["query.json"].(map[string]any)
			responses := query["responses"].([]any)
			responses[1] = map[string]any{"error": "cache unavailable"}
		})
		expectFileAPIReadError(t, fixture)
	})
	t.Run("target native identity mismatch", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		mutateJSONFile(t, filepath.Join(fixture.replyDir, "target-app-Debug.json"), func(value map[string]any) {
			value["id"] = "other::@fixture"
		})
		expectFileAPIReadError(t, fixture)
	})
	t.Run("target codemodel major mismatch", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		mutateJSONFile(t, filepath.Join(fixture.replyDir, "target-app-Debug.json"), func(value map[string]any) {
			value["codemodelVersion"].(map[string]any)["major"] = float64(3)
		})
		expectFileAPIReadError(t, fixture)
	})
}

func TestFileAPIReplyAcceptsUnknownFieldsForNewSupportedMinor(t *testing.T) {
	fixture := newFileAPIReplyFixture(t)
	mutateJSONFile(t, filepath.Join(fixture.replyDir, "index-2026-07-26.json"), func(value map[string]any) {
		objects := value["objects"].([]any)
		reply := value["reply"].(map[string]any)
		client := reply["client-unit-test-ide"].(map[string]any)
		query := client["query.json"].(map[string]any)
		responses := query["responses"].([]any)
		objects[0].(map[string]any)["version"].(map[string]any)["minor"] = float64(99)
		responses[0].(map[string]any)["version"].(map[string]any)["minor"] = float64(99)
		value["futureIndexField"] = map[string]any{"ignored": true}
	})
	mutateJSONFile(t, filepath.Join(fixture.replyDir, "codemodel-v2.json"), func(value map[string]any) {
		value["version"].(map[string]any)["minor"] = float64(99)
		value["futureObjectField"] = []any{"ignored"}
	})
	for _, name := range []string{"target-app-Debug.json", "target-support-Debug.json"} {
		mutateJSONFile(t, filepath.Join(fixture.replyDir, name), func(value map[string]any) {
			value["codemodelVersion"].(map[string]any)["minor"] = float64(99)
			value["futureTargetField"] = "ignored"
		})
	}

	if _, err := ReadReply(
		fixture.buildDir,
		[]string{fixture.sourceDir, fixture.buildDir},
		fixture.profile,
	); err != nil {
		t.Fatalf("ReadReply() rejected forward-compatible minor fields: %v", err)
	}
}

func TestFileAPIReplyRejectsMalformedTrailingAndDuplicateJSON(t *testing.T) {
	t.Run("malformed index", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		if err := os.WriteFile(
			filepath.Join(fixture.replyDir, "index-2026-07-26.json"),
			[]byte(`{"objects":`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		expectFileAPIReadError(t, fixture)
	})
	t.Run("trailing target JSON", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		path := filepath.Join(fixture.replyDir, "target-app-Debug.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, []byte("\n{}")...)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		expectFileAPIReadError(t, fixture)
	})
	t.Run("duplicate target key", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		path := filepath.Join(fixture.replyDir, "target-app-Debug.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data = []byte(strings.Replace(
			string(data),
			`"name": "app"`,
			`"name": "wrong", "name": "app"`,
			1,
		))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		expectFileAPIReadError(t, fixture)
	})
}

func TestFileAPIReplyRejectsReplyAndPayloadPathEscapes(t *testing.T) {
	t.Run("jsonFile must be a basename", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		nested := filepath.Join(fixture.replyDir, "nested")
		if err := os.Mkdir(nested, 0o700); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(fixture.replyDir, "cache-v2.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nested, "cache-v2.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		mutateFileAPIReference(t, fixture, "cache", "nested/cache-v2.json")
		expectFileAPIReadError(t, fixture)
	})
	t.Run("reply junction or symlink escape", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		outside := t.TempDir()
		data, err := os.ReadFile(filepath.Join(fixture.replyDir, "cache-v2.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outside, "cache-v2.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		createDirectoryLink(t, filepath.Join(fixture.replyDir, "escape"), outside)
		mutateFileAPIReference(t, fixture, "cache", "escape/cache-v2.json")
		expectFileAPIReadError(t, fixture)
	})
	t.Run("artifact absolute escape", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		outside := filepath.Join(t.TempDir(), "app")
		mutateJSONFile(t, filepath.Join(fixture.replyDir, "target-app-Debug.json"), func(value map[string]any) {
			value["artifacts"] = []any{map[string]any{"path": filepath.ToSlash(outside)}}
		})
		expectFileAPIReadError(t, fixture)
	})
	t.Run("cmake input symlink or junction escape", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "outside.cmake"), []byte("set(ESCAPED ON)\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		createDirectoryLink(t, filepath.Join(fixture.sourceDir, "escape"), outside)
		mutateJSONFile(t, filepath.Join(fixture.replyDir, "cmakeFiles-v1.json"), func(value map[string]any) {
			value["inputs"] = []any{map[string]any{"path": "escape/outside.cmake"}}
		})
		expectFileAPIReadError(t, fixture)
	})
}

func TestFileAPIReplyAcceptsCanonicalMissingTailArtifact(t *testing.T) {
	fixture := newFileAPIReplyFixture(t)
	missing := filepath.Join(fixture.buildDir, "future", "nested", "app")
	mutateJSONFile(t, filepath.Join(fixture.replyDir, "target-app-Debug.json"), func(value map[string]any) {
		value["artifacts"] = []any{map[string]any{"path": "future/nested/app"}}
	})

	reply := readWithFixture(t, fixture)
	if got := reply.Targets[0].Artifacts; !reflect.DeepEqual(got, []string{missing}) {
		t.Fatalf("Artifacts = %#v, want canonical missing-tail path %q", got, missing)
	}
}

func TestFileAPIReaderSnapshotsFailClosedOnMutationOrReplacement(t *testing.T) {
	files := []fileAPISnapshotCase{
		{name: "index", relative: "index-2026-07-26.json"},
		{name: "object", relative: "cache-v2.json"},
		{name: "target", relative: "target-app-Debug.json"},
		{name: "CMake input", absolute: func(fixture fileAPIReplyFixture) string {
			return filepath.Join(fixture.sourceDir, "cmake", "options.cmake")
		}},
		{name: "CMake cache", absolute: func(fixture fileAPIReplyFixture) string {
			return filepath.Join(fixture.buildDir, "CMakeCache.txt")
		}},
	}
	for _, file := range files {
		for _, mode := range []string{"content mutation", "identity replacement"} {
			t.Run(file.name+"/"+mode, func(t *testing.T) {
				fixture := newFileAPIReplyFixture(t)
				reader := newFileAPIReaderForTest(t, fixture)
				defer reader.close()
				targetPath := file.absolutePath(fixture)
				if file.relative != "" {
					targetPath = filepath.Join(fixture.replyDir, file.relative)
					if _, err := reader.read(file.relative); err != nil {
						t.Fatalf("reader.read() error = %v", err)
					}
				} else if _, err := reader.snapshotAllowed(targetPath); err != nil {
					t.Fatalf("reader.snapshotAllowed() error = %v", err)
				}

				var mutationErr error
				switch mode {
				case "content mutation":
					mutationErr = os.WriteFile(targetPath, []byte("changed after snapshot\n"), 0o600)
				case "identity replacement":
					replacement := targetPath + ".replacement"
					if err := os.WriteFile(replacement, []byte("replacement after snapshot\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					mutationErr = replaceFileAtomically(replacement, targetPath)
					if mutationErr != nil {
						_ = os.Remove(replacement)
					}
				}
				verifyErr := reader.verify()
				if mutationErr == nil && verifyErr == nil {
					t.Fatal("filesystem mutation succeeded and reader verification accepted stale state")
				}
				if mutationErr != nil && verifyErr != nil {
					t.Fatalf("filesystem blocked mutation (%v), but unchanged snapshot verification failed: %v", mutationErr, verifyErr)
				}
			})
		}
	}
}

func TestFileAPIAllowedRootsAreCanonicalDeduplicatedAndFailClosed(t *testing.T) {
	fixture := newFileAPIReplyFixture(t)
	if _, err := ReadReply(fixture.buildDir, nil, fixture.profile); err == nil {
		t.Fatal("ReadReply() with no allowed roots error = nil")
	}

	commonRoot := filepath.Dir(fixture.sourceDir)
	first, err := ReadReply(
		fixture.buildDir,
		[]string{fixture.sourceDir, fixture.sourceDir, fixture.buildDir, commonRoot},
		fixture.profile,
	)
	if err != nil {
		t.Fatalf("ReadReply(overlapping roots) error = %v", err)
	}
	second := readWithFixture(t, fixture)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("duplicate/overlapping roots changed reply:\nfirst  = %#v\nsecond = %#v", first, second)
	}

	outside := t.TempDir()
	mutateJSONFile(t, filepath.Join(fixture.replyDir, "codemodel-v2.json"), func(value map[string]any) {
		value["paths"].(map[string]any)["source"] = filepath.ToSlash(outside)
	})
	expectFileAPIReadError(t, fixture)
}

func TestFileAPIReplyDeduplicatesEquivalentConfigurationsAndTargets(t *testing.T) {
	fixture := newFileAPIReplyFixture(t)
	mutateJSONFile(t, filepath.Join(fixture.replyDir, "codemodel-v2.json"), func(value map[string]any) {
		configurations := value["configurations"].([]any)
		configuration := configurations[0].(map[string]any)
		targets := configuration["targets"].([]any)
		configuration["targets"] = append(targets, targets[0])
		value["configurations"] = append(configurations, configuration)
	})

	reply := readWithFixture(t, fixture)
	if got := reply.Configurations; !reflect.DeepEqual(got, []string{"Debug"}) {
		t.Fatalf("Configurations = %#v, want one deduplicated configuration", got)
	}
	if got := targetNames(reply.Targets); !reflect.DeepEqual(got, []string{"app", "support"}) {
		t.Fatalf("targets = %#v, want equivalent targets deduplicated", got)
	}
}

func TestFileAPIReplyRejectsEmptyOrConflictingConfigurationTargetIdentity(t *testing.T) {
	t.Run("empty configuration", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		mutateJSONFile(t, filepath.Join(fixture.replyDir, "codemodel-v2.json"), func(value map[string]any) {
			configurations := value["configurations"].([]any)
			configurations[0].(map[string]any)["name"] = ""
		})
		expectFileAPIReadError(t, fixture)
	})
	t.Run("empty target ID", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		mutateJSONFile(t, filepath.Join(fixture.replyDir, "codemodel-v2.json"), func(value map[string]any) {
			configurations := value["configurations"].([]any)
			targets := configurations[0].(map[string]any)["targets"].([]any)
			targets[0].(map[string]any)["id"] = ""
		})
		expectFileAPIReadError(t, fixture)
	})
	t.Run("conflicting duplicate native ID", func(t *testing.T) {
		fixture := newFileAPIReplyFixture(t)
		source := filepath.Join(fixture.replyDir, "target-app-Debug.json")
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(fixture.replyDir, "target-app-conflict.json")
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			t.Fatal(err)
		}
		mutateJSONFile(t, destination, func(value map[string]any) {
			value["type"] = "STATIC_LIBRARY"
		})
		mutateJSONFile(t, filepath.Join(fixture.replyDir, "codemodel-v2.json"), func(value map[string]any) {
			configurations := value["configurations"].([]any)
			configuration := configurations[0].(map[string]any)
			targets := configuration["targets"].([]any)
			configuration["targets"] = append(targets, map[string]any{
				"name":     "app",
				"id":       "app::@fixture",
				"jsonFile": "target-app-conflict.json",
			})
		})
		expectFileAPIReadError(t, fixture)
	})
}

func TestFileAPIReplyEnforcesFixedLimits(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, fileAPIReplyFixture)
	}{
		{
			name: "file bytes",
			setup: func(t *testing.T, fixture fileAPIReplyFixture) {
				path := filepath.Join(fixture.replyDir, "cache-v2.json")
				data := `{"kind":"cache","version":{"major":2,"minor":0},"entries":[],"padding":"` +
					strings.Repeat("x", 512*1024) + `"}`
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "index objects",
			setup: func(t *testing.T, fixture fileAPIReplyFixture) {
				mutateJSONFile(t, filepath.Join(fixture.replyDir, "index-2026-07-26.json"), func(value map[string]any) {
					objects := value["objects"].([]any)
					for index := 0; index < 65; index++ {
						objects = append(objects, map[string]any{
							"kind":     "future",
							"version":  map[string]any{"major": 1, "minor": index},
							"jsonFile": "future.json",
						})
					}
					value["objects"] = objects
				})
			},
		},
		{
			name: "configurations",
			setup: func(t *testing.T, fixture fileAPIReplyFixture) {
				mutateJSONFile(t, filepath.Join(fixture.replyDir, "codemodel-v2.json"), func(value map[string]any) {
					configurations := make([]any, 65)
					for index := range configurations {
						configurations[index] = map[string]any{
							"name":    "Config-" + strings.Repeat("x", index+1),
							"targets": []any{},
						}
					}
					value["configurations"] = configurations
				})
			},
		},
		{
			name: "targets",
			setup: func(t *testing.T, fixture fileAPIReplyFixture) {
				mutateJSONFile(t, filepath.Join(fixture.replyDir, "codemodel-v2.json"), func(value map[string]any) {
					configurations := value["configurations"].([]any)
					configuration := configurations[0].(map[string]any)
					target := configuration["targets"].([]any)[0]
					targets := make([]any, 1025)
					for index := range targets {
						targets[index] = target
					}
					configuration["targets"] = targets
				})
			},
		},
		{
			name: "artifacts",
			setup: func(t *testing.T, fixture fileAPIReplyFixture) {
				mutateJSONFile(t, filepath.Join(fixture.replyDir, "target-app-Debug.json"), func(value map[string]any) {
					artifacts := make([]any, 129)
					for index := range artifacts {
						artifacts[index] = map[string]any{"path": "bin/app"}
					}
					value["artifacts"] = artifacts
				})
			},
		},
		{
			name: "cmake inputs",
			setup: func(t *testing.T, fixture fileAPIReplyFixture) {
				mutateJSONFile(t, filepath.Join(fixture.replyDir, "cmakeFiles-v1.json"), func(value map[string]any) {
					inputs := make([]any, 2049)
					for index := range inputs {
						inputs[index] = map[string]any{"path": "CMakeLists.txt"}
					}
					value["inputs"] = inputs
				})
			},
		},
		{
			name: "toolchains",
			setup: func(t *testing.T, fixture fileAPIReplyFixture) {
				mutateJSONFile(t, filepath.Join(fixture.replyDir, "toolchains-v1.json"), func(value map[string]any) {
					toolchain := value["toolchains"].([]any)[0]
					toolchains := make([]any, 65)
					for index := range toolchains {
						toolchains[index] = toolchain
					}
					value["toolchains"] = toolchains
				})
			},
		},
		{
			name: "cache entries",
			setup: func(t *testing.T, fixture fileAPIReplyFixture) {
				mutateJSONFile(t, filepath.Join(fixture.replyDir, "cache-v2.json"), func(value map[string]any) {
					entry := value["entries"].([]any)[0]
					entries := make([]any, 4097)
					for index := range entries {
						entries[index] = entry
					}
					value["entries"] = entries
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFileAPIReplyFixture(t)
			test.setup(t, fixture)
			expectFileAPIReadError(t, fixture)
		})
	}
}

func TestFileAPIReplyEnforcesTargetDetailTotalFileAndTotalByteLimits(t *testing.T) {
	tests := []struct {
		name    string
		count   int
		padding int
	}{
		{name: "target detail files", count: 257},
		{name: "total files", count: 136},
		{name: "total bytes", count: 10, padding: 450 * 1024},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFileAPIReplyFixture(t)
			installTargetDetailCopies(t, fixture, test.count, test.padding)
			expectFileAPIReadError(t, fixture)
		})
	}
}

type fileAPIReplyFixture struct {
	sourceDir string
	buildDir  string
	replyDir  string
	profile   BuildProfile
}

type fileAPISnapshotCase struct {
	name     string
	relative string
	absolute func(fileAPIReplyFixture) string
}

func (file fileAPISnapshotCase) absolutePath(fixture fileAPIReplyFixture) string {
	if file.absolute == nil {
		return ""
	}
	return file.absolute(fixture)
}

func newFileAPIReaderForTest(t *testing.T, fixture fileAPIReplyFixture) *fileAPIReader {
	t.Helper()
	roots, err := openFileAPIAllowedRoots([]string{fixture.sourceDir, fixture.buildDir})
	if err != nil {
		t.Fatal(err)
	}
	replyRoot, err := workspace.OpenRoot(fixture.replyDir)
	if err != nil {
		t.Fatal(err)
	}
	return &fileAPIReader{
		replyRoot:    replyRoot,
		allowedRoots: roots,
		snapshots:    make(map[string]*fileSnapshot),
		data:         make(map[string][]byte),
		replyFiles:   make(map[string]struct{}),
	}
}

func newFileAPIReplyFixture(t *testing.T) fileAPIReplyFixture {
	t.Helper()
	root := t.TempDir()
	fixture := fileAPIReplyFixture{
		sourceDir: filepath.Join(root, "source"),
		buildDir:  filepath.Join(root, "build"),
		profile: BuildProfile{
			ID:            strings.Repeat("a", 64),
			ProjectID:     "fixture",
			Generator:     "Ninja",
			Configuration: "Debug",
		},
	}
	fixture.replyDir = filepath.Join(fixture.buildDir, ".cmake", "api", "v1", "reply")
	for _, directory := range []string{
		fixture.replyDir,
		filepath.Join(fixture.sourceDir, "cmake"),
		filepath.Join(fixture.sourceDir, "tools"),
		filepath.Join(fixture.buildDir, "CMakeFiles", "4.3.4"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		filepath.Join(fixture.sourceDir, "CMakeLists.txt"):                          "cmake_minimum_required(VERSION 4.3)\n",
		filepath.Join(fixture.sourceDir, "cmake", "options.cmake"):                  "set(FIXTURE ON)\n",
		filepath.Join(fixture.buildDir, "CMakeFiles", "4.3.4", "CMakeSystem.cmake"): "set(CMAKE_SYSTEM_NAME fixture)\n",
		filepath.Join(fixture.buildDir, "CMakeCache.txt"):                           "CMAKE_BUILD_TYPE:STRING=Debug\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(filepath.Join("testdata", "fileapi", "reply"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join("testdata", "fileapi", "reply", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		content := strings.ReplaceAll(string(data), "$SOURCE$", filepath.ToSlash(fixture.sourceDir))
		content = strings.ReplaceAll(content, "$BUILD$", filepath.ToSlash(fixture.buildDir))
		if err := os.WriteFile(filepath.Join(fixture.replyDir, entry.Name()), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func readWithFixture(t *testing.T, fixture fileAPIReplyFixture) FileAPIReply {
	t.Helper()
	reply, err := ReadReply(
		fixture.buildDir,
		[]string{fixture.sourceDir, fixture.buildDir},
		fixture.profile,
	)
	if err != nil {
		t.Fatalf("ReadReply() error = %v", err)
	}
	return reply
}

func expectFileAPIReadError(t *testing.T, fixture fileAPIReplyFixture) {
	t.Helper()
	if _, err := ReadReply(
		fixture.buildDir,
		[]string{fixture.sourceDir, fixture.buildDir},
		fixture.profile,
	); err == nil {
		t.Fatal("ReadReply() error = nil, want rejection")
	}
}

func mutateFileAPIReference(
	t *testing.T,
	fixture fileAPIReplyFixture,
	kind string,
	jsonFile string,
) {
	t.Helper()
	mutateJSONFile(t, filepath.Join(fixture.replyDir, "index-2026-07-26.json"), func(value map[string]any) {
		objects := value["objects"].([]any)
		for _, candidate := range objects {
			reference := candidate.(map[string]any)
			if reference["kind"] == kind {
				reference["jsonFile"] = jsonFile
			}
		}
		reply := value["reply"].(map[string]any)
		client := reply["client-unit-test-ide"].(map[string]any)
		query := client["query.json"].(map[string]any)
		responses := query["responses"].([]any)
		for _, candidate := range responses {
			reference := candidate.(map[string]any)
			if reference["kind"] == kind {
				reference["jsonFile"] = jsonFile
			}
		}
	})
}

func installTargetDetailCopies(
	t *testing.T,
	fixture fileAPIReplyFixture,
	count int,
	padding int,
) {
	t.Helper()
	templatePath := filepath.Join(fixture.replyDir, "target-support-Debug.json")
	templateData, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(templateData, &object); err != nil {
		t.Fatal(err)
	}
	if padding > 0 {
		object["futurePadding"] = strings.Repeat("x", padding)
	}
	templateData, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	references := make([]any, count)
	for index := 0; index < count; index++ {
		name := "target-detail-" + leftPadDecimal(index, 4) + ".json"
		if err := os.WriteFile(filepath.Join(fixture.replyDir, name), templateData, 0o600); err != nil {
			t.Fatal(err)
		}
		references[index] = map[string]any{
			"name":     "support",
			"id":       "support::@fixture",
			"jsonFile": name,
		}
	}
	mutateJSONFile(t, filepath.Join(fixture.replyDir, "codemodel-v2.json"), func(value map[string]any) {
		configurations := value["configurations"].([]any)
		configurations[0].(map[string]any)["targets"] = references
	})
}

func leftPadDecimal(value, width int) string {
	text := strconv.Itoa(value)
	return strings.Repeat("0", width-len(text)) + text
}

func targetNames(targets []Target) []string {
	names := make([]string, len(targets))
	for index, target := range targets {
		names[index] = target.Name
	}
	return names
}

func targetIDs(targets []Target) []string {
	identities := make([]string, len(targets))
	for index, target := range targets {
		identities[index] = target.ID
	}
	return identities
}
