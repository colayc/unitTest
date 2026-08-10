package coveragebundle

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	owned, err := descriptor.WriteAtomic(coverageRoot, "task-1")
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
			if _, err := test.make().WriteAtomic(coverageRoot, "task"); err == nil {
				t.Fatal("WriteAtomic accepted unsafe descriptor")
			}
		})
	}
}

func TestDescriptorRejectsSymlinkEscape(t *testing.T) {
	if runtimeGOOS() == "windows" {
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
	if _, err := descriptor.WriteAtomic(coverageRoot, "task"); err == nil {
		t.Fatal("WriteAtomic accepted symlink root escape")
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
	owned, err := descriptor.WriteAtomic(coverageRoot, "task")
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
