package runtime

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type recordingDataDirGuard struct {
	name       string
	closeCalls *[]string
	closed     int
}

func (g *recordingDataDirGuard) Close() error {
	g.closed++
	*g.closeCalls = append(*g.closeCalls, g.name)
	return nil
}

func TestPrepareDataDirReturnsFixedAbsoluteLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private", "service-data")
	layout, err := PrepareDataDir(root)
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Root != absolute || layout.Database != filepath.Join(absolute, "history.sqlite3") ||
		layout.Artifacts != filepath.Join(absolute, "artifacts") ||
		layout.Build != filepath.Join(absolute, "build") ||
		layout.Coverage != filepath.Join(absolute, "coverage") ||
		layout.Controls != filepath.Join(absolute, "controls") ||
		layout.Lock != filepath.Join(absolute, "service.lock") {
		t.Fatalf("layout = %#v", layout)
	}
	for _, directory := range []string{
		layout.Root,
		layout.Build,
		layout.Coverage,
		layout.Controls,
	} {
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			t.Fatalf("data directory %q = %#v, %v", directory, info, err)
		}
	}
}

func TestCoverageDataDirectoryIsServiceOwned(t *testing.T) {
	layout, err := PrepareDataDir(filepath.Join(t.TempDir(), "private", "service-data"))
	if err != nil {
		t.Fatal(err)
	}
	if layout.Coverage == "" || filepath.Dir(layout.Coverage) != layout.Root {
		t.Fatalf("coverage layout = %#v", layout)
	}
	info, err := os.Stat(layout.Coverage)
	if err != nil || !info.IsDir() {
		t.Fatalf("coverage data directory = %#v, %v", info, err)
	}
	assertOwnerOnlyDirectoryForTest(t, layout.Coverage)
	anchor, err := CoverageAuthority(layout)
	if err != nil {
		t.Fatal(err)
	}
	if anchor.Root() != layout.Root || anchor.Verify(layout.Coverage) != nil {
		t.Fatalf("coverage anchor is not bound to layout: root=%q coverage=%q", anchor.Root(), layout.Coverage)
	}
}

func TestPrepareDataDirGuardCoverageFailureCleansUpInReverseOrder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private", "service-data")
	var calls []string
	var closeCalls []string
	guards := make(map[string]*recordingDataDirGuard)
	pin := func(path string) (io.Closer, error) {
		name := filepath.Base(path)
		calls = append(calls, name)
		if name == "coverage" {
			return nil, errors.New("injected coverage pin failure")
		}
		guard := &recordingDataDirGuard{name: name, closeCalls: &closeCalls}
		guards[name] = guard
		return guard, nil
	}

	_, guard, err := prepareDataDirGuardWithPin(root, pin)
	if !errors.Is(err, ErrUnsafeDataDir) {
		t.Fatalf("prepareDataDirGuardWithPin() error = %v, want ErrUnsafeDataDir", err)
	}
	if guard != nil {
		t.Fatal("coverage failure returned a cleanup guard")
	}
	if got, want := calls, []string{"service-data", "build", "controls", "coverage"}; !slices.Equal(got, want) {
		t.Fatalf("pin calls = %#v, want %#v", got, want)
	}
	if got, want := closeCalls, []string{"controls", "build", "service-data"}; !slices.Equal(got, want) {
		t.Fatalf("cleanup order = %#v, want %#v", got, want)
	}
	for name, guard := range guards {
		if guard.closed != 1 {
			t.Fatalf("guard %q closed %d times, want once", name, guard.closed)
		}
	}
}

func TestPrepareDataDirRejectsARegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDataDir(path); !errors.Is(err, ErrUnsafeDataDir) {
		t.Fatalf("PrepareDataDir() error = %v, want ErrUnsafeDataDir", err)
	}
}

func TestPrepareDataDirRejectsNULWithoutPanicking(t *testing.T) {
	if _, err := PrepareDataDir(filepath.Join(t.TempDir(), "bad") + "\x00suffix"); !errors.Is(err, ErrUnsafeDataDir) {
		t.Fatalf("PrepareDataDir() error = %v, want ErrUnsafeDataDir", err)
	}
}
