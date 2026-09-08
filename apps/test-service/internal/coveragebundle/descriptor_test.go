package coveragebundle

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coverageplatform"
)

func strictTestTempDir(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..", ".task4-scratch")
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(root, "case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func TestDescriptorWriteAtomicIsClosedAndDeterministic(t *testing.T) {
	coverageRoot := filepath.Join(strictTestTempDir(t), "coverage")
	if err := os.MkdirAll(coverageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(strictTestTempDir(t), "source")
	objects := filepath.Join(strictTestTempDir(t), "objects")
	gcov := filepath.Join(strictTestTempDir(t), "gcov.exe")
	for _, directory := range []string{root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	taskRoot := filepath.Join(coverageRoot, "gcovr")
	descriptor, err := NewDescriptor(
		root,
		objects,
		gcov,
		filepath.Join(taskRoot, "coverage.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := descriptor.WriteAtomic(descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov))
	if err != nil {
		t.Fatal(err)
	}
	if owned.Path() == "" || filepath.Dir(owned.Path()) != taskRoot {
		t.Fatalf("descriptor path = %q, want task-owned root %q", owned.Path(), taskRoot)
	}
	decoded, err := owned.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if decoded != descriptor {
		t.Fatalf("descriptor = %#v, want %#v", decoded, descriptor)
	}
	if err := owned.Verify(); err != nil {
		t.Fatalf("Verify() = %v", err)
	}
	if err := owned.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if err := owned.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if err := owned.Verify(); !errors.Is(err, ErrDescriptorClosed) {
		t.Fatalf("Verify after Close() = %v, want ErrDescriptorClosed", err)
	}
}

func TestDescriptorRejectsClosedContractAndNativeEscapes(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot := filepath.Join(base, "coverage")
	root := filepath.Join(base, "root")
	objects := filepath.Join(base, "objects")
	gcov := filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	validOutput := filepath.Join(coverageRoot, "task", "out.json")
	tests := []struct {
		name string
		make func() Descriptor
	}{
		{name: "unknown schema", make: func() Descriptor {
			return Descriptor{SchemaVersion: 2, Root: root, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: validOutput}
		}},
		{name: "relative root", make: func() Descriptor {
			return Descriptor{SchemaVersion: 1, Root: "root", ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: validOutput}
		}},
		{name: "workspace output escape", make: func() Descriptor {
			return Descriptor{SchemaVersion: 1, Root: root, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(base, "outside.json")}
		}},
		{name: "workspace script input", make: func() Descriptor {
			return Descriptor{SchemaVersion: 1, Root: filepath.Join(base, "script.py"), ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: validOutput}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.make().WriteAtomic(DescriptorCapabilities{}); err == nil {
				t.Fatal("WriteAtomic accepted unsafe descriptor")
			}
		})
	}
}

func TestDescriptorRejectsUnboundAuthority(t *testing.T) {
	base := strictTestTempDir(t)
	coverage := filepath.Join(base, "coverage")
	if err := os.MkdirAll(coverage, 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor := Descriptor{SchemaVersion: 1, Root: base, ObjectDirectory: base, GcovExecutable: filepath.Join(base, "gcov"), OutputPath: filepath.Join(coverage, "task", "out.json")}
	if _, err := descriptor.WriteAtomic(DescriptorCapabilities{}); err == nil {
		t.Fatal("WriteAtomic accepted an unbound service authority")
	}
}

func TestDescriptorCleanupPreflightFailsBeforeCreatingTaskRoot(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot, root := filepath.Join(base, "coverage"), filepath.Join(base, "root")
	objects, gcov := filepath.Join(base, "objects"), filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor := Descriptor{SchemaVersion: 1, Root: root, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json")}
	original := cleanupAuthorityPreflight
	cleanupAuthorityPreflight = func(*VerifiedDirectory, *pinnedObject) error {
		return errors.New("injected cleanup authority failure")
	}
	t.Cleanup(func() { cleanupAuthorityPreflight = original })
	if owned, err := descriptor.WriteAtomic(descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov)); err == nil || owned != nil {
		t.Fatalf("WriteAtomic() = (%v, %v), want preflight failure", owned, err)
	}
	if _, err := os.Lstat(filepath.Join(coverageRoot, "gcovr")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preflight failure left task root: %v", err)
	}
}

func TestDescriptorDirectoryCleanupPinPreflightFailureLeavesNoResidue(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot, root := filepath.Join(base, "coverage"), filepath.Join(base, "root")
	objects, gcov := filepath.Join(base, "objects"), filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor := Descriptor{SchemaVersion: 1, Root: root, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json")}
	original := validateCreatedCleanupDirectory
	validateCreatedCleanupDirectory = func(*pinnedObject) error {
		return errors.New("injected directory delete pin failure")
	}
	t.Cleanup(func() { validateCreatedCleanupDirectory = original })
	if owned, err := descriptor.WriteAtomic(descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov)); err == nil || owned != nil {
		t.Fatalf("WriteAtomic() = (%v, %v), want directory preflight failure", owned, err)
	}
	entries, err := os.ReadDir(coverageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory pin preflight left collector residue: %#v", entries)
	}
}

func TestDescriptorGcovrFirstCleanupPinFailureRemovesFreshTaskRoot(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot, root := filepath.Join(base, "coverage"), filepath.Join(base, "root")
	objects, gcov := filepath.Join(base, "objects"), filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor := Descriptor{SchemaVersion: 1, Root: root, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json")}
	original := validateCreatedCleanupDirectory
	validateCreatedCleanupDirectory = func(child *pinnedObject) error {
		if filepath.Base(child.path) == "gcovr" {
			return errors.New("injected gcovr first cleanup pin failure")
		}
		return original(child)
	}
	t.Cleanup(func() { validateCreatedCleanupDirectory = original })
	if owned, err := descriptor.WriteAtomic(descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov)); err == nil || owned != nil {
		t.Fatalf("WriteAtomic() = (%v, %v), want gcovr first cleanup pin failure", owned, err)
	}
	if _, err := os.Lstat(filepath.Join(coverageRoot, "gcovr")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("gcovr cleanup pin failure left task root: %v", err)
	}
}

func TestDescriptorTemporaryCleanupFailureIsReturnedWithoutResidue(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot, root := filepath.Join(base, "coverage"), filepath.Join(base, "root")
	objects, gcov := filepath.Join(base, "objects"), filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor := Descriptor{SchemaVersion: 1, Root: root, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json")}
	writeFailure := errors.New("injected temporary write failure")
	cleanupFailure := errors.New("injected surfaced cleanup failure")
	originalWrite, originalRemove := writeDescriptorTemporary, removePinnedChildForCleanup
	writeDescriptorTemporary = func(*os.File, []byte) (int, error) { return 0, writeFailure }
	removePinnedChildForCleanup = func(parent, child *pinnedObject, name string) error {
		return errors.Join(removePinnedChild(parent, child, name), cleanupFailure)
	}
	t.Cleanup(func() {
		writeDescriptorTemporary = originalWrite
		removePinnedChildForCleanup = originalRemove
	})
	if owned, err := descriptor.WriteAtomic(descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov)); err == nil || owned != nil {
		t.Fatalf("WriteAtomic() = (%v, %v), want temporary write failure", owned, err)
	} else if !errors.Is(err, writeFailure) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("WriteAtomic() = %v, want both write and cleanup failures", err)
	}
	if _, err := os.Lstat(filepath.Join(coverageRoot, "gcovr")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary cleanup failure left task root: %v", err)
	}
}

func TestDescriptorPostPublicationFailureCleansTaskRoot(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot, root := filepath.Join(base, "coverage"), filepath.Join(base, "root")
	objects, gcov := filepath.Join(base, "objects"), filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor := Descriptor{SchemaVersion: 1, Root: root, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json")}
	original := descriptorPostPublication
	descriptorPostPublication = func() error { return errors.New("injected post-publication failure") }
	t.Cleanup(func() { descriptorPostPublication = original })
	if owned, err := descriptor.WriteAtomic(descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov)); err == nil || owned != nil {
		t.Fatalf("WriteAtomic() = (%v, %v), want post-publication failure", owned, err)
	}
	if _, err := os.Lstat(filepath.Join(coverageRoot, "gcovr")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("post-publication failure left task root: %v", err)
	}
}

func TestDescriptorCapabilitiesRejectTypedNilWithoutPanic(t *testing.T) {
	base := strictTestTempDir(t)
	collector, root, objects, gcov := filepath.Join(base, "collector"), filepath.Join(base, "root"), filepath.Join(base, "objects"), filepath.Join(base, "gcov")
	for _, directory := range []string{collector, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor := Descriptor{SchemaVersion: 1, Root: root, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(collector, "gcovr", "coverage.json")}
	for _, mutate := range []func(*DescriptorCapabilities){
		func(capabilities *DescriptorCapabilities) {
			var value *VerifiedDirectory
			capabilities.CollectorRoot = value
		},
		func(capabilities *DescriptorCapabilities) { var value *VerifiedDirectory; capabilities.Root = value },
		func(capabilities *DescriptorCapabilities) {
			var value *VerifiedDirectory
			capabilities.ObjectDirectory = value
		},
		func(capabilities *DescriptorCapabilities) {
			var value *VerifiedExecutable
			capabilities.GcovExecutable = value
		},
	} {
		capabilities := descriptorCapabilitiesForTest(t, collector, root, objects, gcov)
		mutate(&capabilities)
		if _, err := descriptor.WriteAtomic(capabilities); err == nil {
			t.Fatal("WriteAtomic accepted typed-nil capability")
		}
	}
}

type retainedDirectoryFacade struct{ source *VerifiedDirectory }

func (facade retainedDirectoryFacade) Path() string {
	if facade.source == nil {
		return ""
	}
	return facade.source.Path()
}

func (facade retainedDirectoryFacade) Verify() error {
	if facade.source == nil {
		return ErrDescriptorIntegrity
	}
	return facade.source.Verify()
}

func (facade retainedDirectoryFacade) RetainDirectory() (coverageplatform.RetainedDirectory, error) {
	if facade.source == nil {
		return nil, ErrDescriptorIntegrity
	}
	return facade.source.RetainDirectory()
}

type finalVerifyFailureDirectoryFacade struct {
	source *VerifiedDirectory
	fail   *bool
	err    error
}

func (facade finalVerifyFailureDirectoryFacade) Path() string { return facade.source.Path() }

func (facade finalVerifyFailureDirectoryFacade) Verify() error {
	if *facade.fail {
		return facade.err
	}
	return facade.source.Verify()
}

func (facade finalVerifyFailureDirectoryFacade) RetainDirectory() (coverageplatform.RetainedDirectory, error) {
	return facade.source.RetainDirectory()
}

func TestDescriptorFinalVerifyReturnsCleanupFailure(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot, root := filepath.Join(base, "coverage"), filepath.Join(base, "root")
	objects, gcov := filepath.Join(base, "objects"), filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor := Descriptor{SchemaVersion: 1, Root: root, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json")}
	capabilities := descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov)
	source := capabilities.CollectorRoot.(*VerifiedDirectory)
	shouldFail := false
	verifyFailure := errors.New("injected final verification failure")
	capabilities.CollectorRoot = finalVerifyFailureDirectoryFacade{source: source, fail: &shouldFail, err: verifyFailure}
	cleanupFailure := errors.New("injected final cleanup failure")
	originalPostPublication, originalRemove := descriptorPostPublication, removePinnedChildForCleanup
	descriptorPostPublication = func() error { shouldFail = true; return nil }
	removePinnedChildForCleanup = func(parent, child *pinnedObject, name string) error {
		return errors.Join(removePinnedChild(parent, child, name), cleanupFailure)
	}
	t.Cleanup(func() {
		descriptorPostPublication = originalPostPublication
		removePinnedChildForCleanup = originalRemove
		_ = source.Close()
	})
	if owned, err := descriptor.WriteAtomic(capabilities); err == nil || owned != nil {
		t.Fatalf("WriteAtomic() = (%v, %v), want final verification failure", owned, err)
	} else if !strings.Contains(err.Error(), verifyFailure.Error()) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("WriteAtomic() = %v, want final verification and cleanup failures", err)
	}
}

func TestDescriptorBridgesRetainedCollectorWithoutOwningSource(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot := filepath.Join(base, "coverage")
	root := filepath.Join(base, "root")
	objects := filepath.Join(base, "objects")
	gcov := filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	capabilities := descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov)
	source, ok := capabilities.CollectorRoot.(*VerifiedDirectory)
	if !ok {
		t.Fatal("test collector is not retained")
	}
	capabilities.CollectorRoot = retainedDirectoryFacade{source: source}
	descriptor, err := NewDescriptor(root, objects, gcov, filepath.Join(coverageRoot, "gcovr", "coverage.json"))
	if err != nil {
		t.Fatal(err)
	}
	owned, err := descriptor.WriteAtomic(capabilities)
	if err != nil {
		t.Fatalf("WriteAtomic() = %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owned.Close(); err == nil {
		t.Fatal("Close() hid external source closure")
	}
	if _, err := os.Lstat(filepath.Join(coverageRoot, "gcovr")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Close() left task root after source close: %v", err)
	}
	if err := owned.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
}

func TestOwnedDescriptorRejectsEveryEarlyClosedProducerCapability(t *testing.T) {
	for _, test := range []struct {
		name  string
		close func(DescriptorCapabilities) error
	}{
		{"collector", func(capabilities DescriptorCapabilities) error {
			return capabilities.CollectorRoot.(*VerifiedDirectory).Close()
		}},
		{"source", func(capabilities DescriptorCapabilities) error { return capabilities.Root.(*VerifiedDirectory).Close() }},
		{"objects", func(capabilities DescriptorCapabilities) error {
			return capabilities.ObjectDirectory.(*VerifiedDirectory).Close()
		}},
		{"gcov", func(capabilities DescriptorCapabilities) error {
			return capabilities.GcovExecutable.(*VerifiedExecutable).Close()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := strictTestTempDir(t)
			collector := filepath.Join(base, "collector")
			root := filepath.Join(base, "root")
			objects := filepath.Join(base, "objects")
			gcov := filepath.Join(base, "gcov")
			for _, directory := range []string{collector, root, objects} {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
				t.Fatal(err)
			}
			capabilities := descriptorCapabilitiesForTest(t, collector, root, objects, gcov)
			descriptor, err := NewDescriptor(root, objects, gcov, filepath.Join(collector, "gcovr", "coverage.json"))
			if err != nil {
				t.Fatal(err)
			}
			owned, err := descriptor.WriteAtomic(capabilities)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.close(capabilities); err != nil {
				t.Fatal(err)
			}
			if err := owned.Verify(); err == nil {
				t.Fatal("Verify accepted an early-closed producer capability")
			}
			if _, err := owned.PinnedOutput(); err == nil {
				t.Fatal("PinnedOutput accepted an early-closed producer capability")
			}
			_ = owned.Close()
		})
	}
}

func TestDescriptorRejectsUnretainedRootAndObjectCapabilities(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot, root := filepath.Join(base, "coverage"), filepath.Join(base, "root")
	objects, gcov := filepath.Join(base, "objects"), filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor, err := NewDescriptor(root, objects, gcov, filepath.Join(coverageRoot, "gcovr", "coverage.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*DescriptorCapabilities){
		func(capabilities *DescriptorCapabilities) { capabilities.Root = bareDirectoryCapability{path: root} },
		func(capabilities *DescriptorCapabilities) {
			capabilities.ObjectDirectory = bareDirectoryCapability{path: objects}
		},
	} {
		capabilities := descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov)
		mutate(&capabilities)
		owned, err := descriptor.WriteAtomic(capabilities)
		if owned != nil {
			_ = owned.Close()
		}
		if err == nil {
			t.Fatal("WriteAtomic accepted an unretained directory capability")
		}
	}
}

func TestDescriptorRejectsExistingGcovrCollisionWithoutRemovingIt(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot, root := filepath.Join(base, "coverage"), filepath.Join(base, "root")
	objects, gcov := filepath.Join(base, "objects"), filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects, filepath.Join(coverageRoot, "gcovr")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(coverageRoot, "gcovr", "existing.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor, err := NewDescriptor(root, objects, gcov, filepath.Join(coverageRoot, "gcovr", "coverage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if owned, err := descriptor.WriteAtomic(descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov)); err == nil || owned != nil {
		if owned != nil {
			_ = owned.Close()
		}
		t.Fatal("WriteAtomic accepted an existing gcovr collision")
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "keep" {
		t.Fatalf("collision content changed: %q, %v", contents, err)
	}
}

func TestDescriptorUsesAtomicCleanupAuthority(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot, root := filepath.Join(base, "coverage"), filepath.Join(base, "root")
	objects, gcov := filepath.Join(base, "objects"), filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor, err := NewDescriptor(root, objects, gcov, filepath.Join(coverageRoot, "gcovr", "coverage.json"))
	if err != nil {
		t.Fatal(err)
	}
	owned, err := descriptor.WriteAtomic(descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov))
	if err != nil || owned == nil {
		t.Fatalf("WriteAtomic() = (%v, %v), want cleanup-authorized descriptor", owned, err)
	}
	if err := owned.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(coverageRoot, "gcovr")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe cleanup authority left gcovr residue: %v", err)
	}
}

func TestParseDescriptorRejectsUnknownAndDuplicateMembers(t *testing.T) {
	valid := `{"schemaVersion":1,"root":"C:/root","objectDirectory":"C:/objects","gcovExecutable":"C:/gcov.exe","outputPath":"C:/task/coverage.json"}`
	if _, err := ParseDescriptor([]byte(strings.Replace(valid, `"outputPath"`, `"unknown":true,"outputPath"`, 1))); err == nil {
		t.Fatal("ParseDescriptor accepted unknown member")
	}
	if _, err := ParseDescriptor([]byte(strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1))); err == nil {
		t.Fatal("ParseDescriptor accepted duplicate member")
	}
	if _, err := ParseDescriptor([]byte(strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":2`, 1))); err == nil {
		t.Fatal("ParseDescriptor accepted unsupported schema")
	}
	if _, err := ParseDescriptor([]byte(`{"root":"C:/root","objectDirectory":"C:/objects","gcovExecutable":"C:/gcov.exe","outputPath":"C:/task/coverage.json"}`)); err == nil {
		t.Fatal("ParseDescriptor accepted missing schemaVersion")
	}
}

func TestDescriptorRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires elevated Windows privilege")
	}
	base := strictTestTempDir(t)
	coverageRoot := filepath.Join(base, "coverage")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(coverageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(coverageRoot, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	descriptor := Descriptor{
		SchemaVersion:   1,
		Root:            link,
		ObjectDirectory: outside,
		GcovExecutable:  filepath.Join(outside, "gcov"),
		OutputPath:      filepath.Join(coverageRoot, "task", "out.json"),
	}
	if _, err := descriptor.WriteAtomic(DescriptorCapabilities{}); err == nil {
		t.Fatal("WriteAtomic accepted symlink root escape")
	}
}

func TestVerifiedDirectoryRejectsAncestorReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ancestor replacement fixture requires symlink support")
	}
	base := strictTestTempDir(t)
	parent := filepath.Join(base, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	authority, err := testNewServiceAnchor(filepath.Dir(child))
	if err != nil {
		t.Fatal(err)
	}
	capability, err := NewVerifiedDirectoryFromAnchor(authority, filepath.Base(child))
	if err != nil {
		t.Fatal(err)
	}
	defer capability.Close()
	replacement := filepath.Join(base, "replacement")
	if err := os.Rename(parent, replacement); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(replacement, parent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := capability.Verify(); err == nil {
		t.Fatal("VerifiedDirectory accepted replaced ancestor")
	}
}

func TestVerifiedChildCloseDoesNotCloseOwnedParent(t *testing.T) {
	base := strictTestTempDir(t)
	parentPath := filepath.Join(base, "parent")
	childPath := filepath.Join(parentPath, "child")
	if err := os.MkdirAll(childPath, 0o700); err != nil {
		t.Fatal(err)
	}
	anchor, err := testNewServiceAnchor(base)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := NewVerifiedDirectoryFromAnchor(anchor, "parent")
	if err != nil {
		t.Fatal(err)
	}
	child, err := NewVerifiedDirectoryFrom(parent, "child")
	if err != nil {
		_ = parent.Close()
		t.Fatal(err)
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := parent.Verify(); err != nil {
		t.Fatalf("parent Verify after child Close() = %v", err)
	}
	if err := child.Close(); err != nil {
		t.Fatalf("second child Close() = %v", err)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAnchorRelativeChildCloseReleasesImplicitParent(t *testing.T) {
	base := strictTestTempDir(t)
	childPath := filepath.Join(base, "child")
	if err := os.MkdirAll(childPath, 0o700); err != nil {
		t.Fatal(err)
	}
	anchor, err := testNewServiceAnchor(base)
	if err != nil {
		t.Fatal(err)
	}
	child, err := NewVerifiedDirectoryFromAnchor(anchor, "child")
	if err != nil {
		t.Fatal(err)
	}
	parent := child.parent
	if parent == nil || !child.ownsParent {
		t.Fatal("anchor-relative child did not retain ownership of implicit parent")
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := parent.Verify(); !errors.Is(err, ErrDescriptorClosed) {
		t.Fatalf("implicit parent Verify after child Close() = %v, want ErrDescriptorClosed", err)
	}
}

func TestDescriptorDetectsTamperBeforeCloseAndClosesOnce(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot := filepath.Join(base, "coverage")
	root := filepath.Join(base, "root")
	objects := filepath.Join(base, "objects")
	gcov := filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor := Descriptor{SchemaVersion: 1, Root: root, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json")}
	owned, err := descriptor.WriteAtomic(descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov))
	if err != nil {
		t.Fatal(err)
	}
	writeErr := os.WriteFile(owned.Path(), []byte(`{"schemaVersion":1}`), 0o600)
	if runtime.GOOS == "windows" {
		if writeErr == nil {
			t.Fatal("raw descriptor replacement was not blocked by retained delete pin")
		}
		if err := owned.Verify(); err != nil {
			t.Fatalf("Verify after blocked tamper = %v", err)
		}
		if err := owned.Close(); err != nil {
			t.Fatalf("Close after blocked tamper = %v", err)
		}
	} else {
		if writeErr != nil {
			t.Fatalf("Unix in-place write = %v", writeErr)
		}
		if err := owned.Verify(); !errors.Is(err, ErrDescriptorIntegrity) {
			t.Fatalf("Verify after in-place tamper = %v, want ErrDescriptorIntegrity", err)
		}
		if err := owned.Close(); !errors.Is(err, ErrDescriptorIntegrity) {
			t.Fatalf("Close after in-place tamper = %v, want ErrDescriptorIntegrity", err)
		}
	}
	if err := owned.Close(); err != nil {
		t.Fatalf("second Close after tamper = %v", err)
	}
}

func TestDescriptorRetainedDeletePinBlocksReplacementBeforeCleanup(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows DELETE-handle behavior")
	}
	base := strictTestTempDir(t)
	coverageRoot, root := filepath.Join(base, "coverage"), filepath.Join(base, "root")
	objects, gcov := filepath.Join(base, "objects"), filepath.Join(base, "gcov")
	for _, directory := range []string{coverageRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor, err := NewDescriptor(root, objects, gcov, filepath.Join(coverageRoot, "gcovr", "coverage.json"))
	if err != nil {
		t.Fatal(err)
	}
	owned, err := descriptor.WriteAtomic(descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov))
	if err != nil {
		t.Fatal(err)
	}
	replacement := owned.Path() + ".replacement"
	if err := os.Rename(owned.Path(), replacement); err == nil {
		if err := owned.Close(); err == nil {
			t.Fatal("cleanup deleted after descriptor replacement instead of failing closed")
		}
		if _, err := os.Lstat(replacement); err != nil {
			t.Fatalf("replacement was removed during failed cleanup: %v", err)
		}
		return
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(coverageRoot, "gcovr")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup left task root: %v", err)
	}
}

func descriptorCapabilitiesForTest(t *testing.T, coverageRoot, root, objects, gcov string) DescriptorCapabilities {
	t.Helper()
	directoryCapability := func(path string) *VerifiedDirectory {
		anchor, err := testNewServiceAnchor(path)
		if err != nil {
			t.Fatal(err)
		}
		capability, err := NewVerifiedDirectoryFromAnchor(anchor, ".")
		if err != nil {
			t.Fatal(err)
		}
		return capability
	}
	coverageCapability := directoryCapability(coverageRoot)
	rootCapability := directoryCapability(root)
	objectCapability := directoryCapability(objects)
	gcovAnchor, err := testNewServiceAnchor(filepath.Dir(gcov))
	if err != nil {
		t.Fatal(err)
	}
	gcovCapability, err := NewVerifiedExecutableFromAnchor(gcovAnchor, filepath.Base(gcov))
	if err != nil {
		t.Fatal(err)
	}
	return DescriptorCapabilities{
		CollectorRoot: coverageCapability, Root: rootCapability,
		ObjectDirectory: objectCapability, GcovExecutable: gcovCapability,
	}
}
