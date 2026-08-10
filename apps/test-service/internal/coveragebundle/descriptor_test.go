package coveragebundle

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDescriptorWriteAtomicIsClosedAndDeterministic(t *testing.T) {
	coverageRoot := filepath.Join(t.TempDir(), "coverage")
	if err := os.MkdirAll(coverageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "source")
	objects := filepath.Join(t.TempDir(), "objects")
	gcov := filepath.Join(t.TempDir(), "gcov.exe")
	for _, directory := range []string{root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	taskRoot := filepath.Join(coverageRoot, "task-1")
	descriptor, err := NewDescriptor(
		root,
		objects,
		gcov,
		filepath.Join(taskRoot, "coverage.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := descriptor.WriteAtomic(coverageRoot, "task-1", descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov))
	if err != nil {
		t.Fatal(err)
	}
	if owned.Path() == "" || filepath.Dir(owned.Path()) != taskRoot {
		t.Fatalf("descriptor path = %q, want task-owned root %q", owned.Path(), taskRoot)
	}
	contents, err := os.ReadFile(owned.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(contents), "\n") {
		t.Fatal("descriptor is not newline terminated")
	}
	var decoded map[string]any
	if err := json.Unmarshal(contents, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 5 || decoded["schemaVersion"] != float64(1) {
		t.Fatalf("descriptor fields = %#v", decoded)
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
	base := t.TempDir()
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
			if _, err := test.make().WriteAtomic(coverageRoot, "task", DescriptorCapabilities{}); err == nil {
				t.Fatal("WriteAtomic accepted unsafe descriptor")
			}
		})
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
}

func TestDescriptorRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires elevated Windows privilege")
	}
	base := t.TempDir()
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
	if _, err := descriptor.WriteAtomic(coverageRoot, "task", DescriptorCapabilities{}); err == nil {
		t.Fatal("WriteAtomic accepted symlink root escape")
	}
}

func TestVerifiedDirectoryRejectsAncestorReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ancestor replacement fixture requires symlink support")
	}
	base := t.TempDir()
	parent := filepath.Join(base, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	capability, err := NewVerifiedDirectory(child)
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

func TestDescriptorDetectsTamperBeforeCloseAndClosesOnce(t *testing.T) {
	base := t.TempDir()
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
	descriptor := Descriptor{SchemaVersion: 1, Root: root, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "task", "out.json")}
	owned, err := descriptor.WriteAtomic(coverageRoot, "task", descriptorCapabilitiesForTest(t, coverageRoot, root, objects, gcov))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(owned.Path(), []byte(`{"schemaVersion":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := owned.Verify(); !errors.Is(err, ErrDescriptorIntegrity) {
		t.Fatalf("Verify after tamper = %v, want ErrDescriptorIntegrity", err)
	}
	if err := owned.Close(); !errors.Is(err, ErrDescriptorIntegrity) {
		t.Fatalf("Close after tamper = %v, want ErrDescriptorIntegrity", err)
	}
	if err := owned.Close(); err != nil {
		t.Fatalf("second Close after tamper = %v", err)
	}
}

func descriptorCapabilitiesForTest(t *testing.T, coverageRoot, root, objects, gcov string) DescriptorCapabilities {
	t.Helper()
	provenancePath := filepath.Dir(coverageRoot)
	for !pathWithin(provenancePath, root) || !pathWithin(provenancePath, objects) || !pathWithin(provenancePath, gcov) {
		provenancePath = filepath.Dir(provenancePath)
	}
	provenance, err := NewVerifiedDirectory(provenancePath)
	if err != nil {
		t.Fatal(err)
	}
	relative := func(path string) string {
		value, _ := filepath.Rel(provenancePath, path)
		return value
	}
	coverageCapability, err := NewVerifiedDirectoryFrom(provenance, relative(coverageRoot))
	if err != nil {
		t.Fatal(err)
	}
	rootCapability, err := NewVerifiedDirectoryFrom(provenance, relative(root))
	if err != nil {
		t.Fatal(err)
	}
	objectCapability, err := NewVerifiedDirectoryFrom(provenance, relative(objects))
	if err != nil {
		t.Fatal(err)
	}
	gcovCapability, err := NewVerifiedExecutableFrom(provenance, relative(gcov))
	if err != nil {
		t.Fatal(err)
	}
	return DescriptorCapabilities{
		Provenance:   provenance,
		CoverageRoot: coverageCapability, Root: rootCapability,
		ObjectDirectory: objectCapability, GcovExecutable: gcovCapability,
	}
}
