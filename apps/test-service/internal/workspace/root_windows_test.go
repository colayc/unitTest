//go:build windows

package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsResolveRelativeRejectsJunctionEscapeWithMissingTail(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(rootPath, "external")
	createTestJunction(t, junction, outside)
	marker := filepath.Join(outside, "marker.txt")
	if err := os.WriteFile(marker, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(junction, "marker.txt")); err != nil || string(data) != "outside" {
		t.Fatalf("junction did not expose outside marker: data = %q, error = %v", data, err)
	}
	root, err := OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, relative := range []string{
		filepath.Join("external", "marker.txt"),
		filepath.Join("external", "missing", "tail.cpp"),
	} {
		if _, err := root.ResolveRelative(relative); !errors.Is(err, ErrPathOutsideRoot) {
			t.Fatalf("ResolveRelative(%q) error = %v, want ErrPathOutsideRoot", relative, err)
		}
		if root.Contains(filepath.Join(rootPath, relative)) {
			t.Fatalf("Contains(%q) = true for junction escape", relative)
		}
	}
}

func TestWindowsJunctionAliasHasSameStableIdentity(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	alias := filepath.Join(base, "alias")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	createTestJunction(t, alias, target)

	direct, err := OpenRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	throughAlias, err := OpenRoot(alias)
	if err != nil {
		t.Fatal(err)
	}
	if direct != throughAlias {
		t.Fatalf("junction alias changed stable root:\ndirect = %#v\nalias  = %#v", direct, throughAlias)
	}
}

func TestWindowsDriveLetterComparisonIsCaseInsensitive(t *testing.T) {
	rootPath := t.TempDir()
	volume := filepath.VolumeName(rootPath)
	if len(volume) != 2 || volume[1] != ':' {
		t.Fatalf("temporary root volume = %q, drive-letter coverage requires a DOS drive", volume)
	}
	alternateCase := strings.ToLower(volume[:1]) + rootPath[len(volume[:1]):]
	root, err := OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if !root.Contains(alternateCase) {
		t.Fatalf("Contains(%q) = false for alternate drive-letter case", alternateCase)
	}
	alternate, err := OpenRoot(alternateCase)
	if err != nil {
		t.Fatal(err)
	}
	if root != alternate {
		t.Fatalf("drive letter case changed stable root:\noriginal  = %#v\nalternate = %#v", root, alternate)
	}
}

func TestWindowsVolumeRootContainsMissingDescendant(t *testing.T) {
	volume := filepath.VolumeName(t.TempDir())
	if len(volume) != 2 || volume[1] != ':' {
		t.Fatalf("temporary root volume = %q, drive-root coverage requires a DOS drive", volume)
	}
	volumeRoot := volume + string(filepath.Separator)
	root := Root{NativePath: volumeRoot}
	descendant := filepath.Join(volumeRoot, "unit-test-ide-missing-descendant")
	if !root.Contains(descendant) {
		t.Fatalf("Contains(%q) = false for drive-root descendant", descendant)
	}
}

func TestWindowsUNCFinalPathUsesShareIdentityWithoutVolumeGUID(t *testing.T) {
	query := func(flags uint32) (string, error) {
		if flags == 0 {
			return `\\?\UNC\BuildServer\Team Share\Workspace`, nil
		}
		return "", windows.ERROR_PATH_NOT_FOUND
	}

	nativePath, volumeIdentity, err := canonicalWindowsPathAndVolume(query)
	if err != nil {
		t.Fatal(err)
	}
	if nativePath != `\\BuildServer\Team Share\Workspace` {
		t.Fatalf("native path = %q", nativePath)
	}
	if volumeIdentity != "buildserver/team share" {
		t.Fatalf("volume identity = %q, want buildserver/team share", volumeIdentity)
	}
}

func TestWindowsUNCIdentityIsStableAcrossAliasCase(t *testing.T) {
	firstPath, firstVolume, err := canonicalWindowsPathAndVolume(func(flags uint32) (string, error) {
		if flags == 0 {
			return `\\?\UNC\BuildServer\TeamShare\Workspace`, nil
		}
		return "", windows.ERROR_PATH_NOT_FOUND
	})
	if err != nil {
		t.Fatal(err)
	}
	secondPath, secondVolume, err := canonicalWindowsPathAndVolume(func(flags uint32) (string, error) {
		if flags == 0 {
			return `\\?\UNC\buildserver\teamshare\workspace`, nil
		}
		return "", windows.ERROR_PATH_NOT_FOUND
	})
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := rootID(firstPath, firstVolume)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := rootID(secondPath, secondVolume)
	if err != nil {
		t.Fatal(err)
	}
	if firstVolume != "buildserver/teamshare" || secondVolume != firstVolume {
		t.Fatalf("UNC identities = %q and %q", firstVolume, secondVolume)
	}
	if firstID != secondID {
		t.Fatalf("UNC alias IDs differ: %q != %q", firstID, secondID)
	}
}

func TestWindowsLocalFinalPathKeepsVolumeGUIDIdentity(t *testing.T) {
	nativePath, volumeIdentity, err := canonicalWindowsPathAndVolume(func(flags uint32) (string, error) {
		switch flags {
		case 0:
			return `\\?\C:\Workspace`, nil
		case volumeNameGUID:
			return `\\?\Volume{ABCDEF}\Workspace`, nil
		default:
			return "", windows.ERROR_INVALID_PARAMETER
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if nativePath != `C:\Workspace` {
		t.Fatalf("native path = %q", nativePath)
	}
	if volumeIdentity != `\\?\Volume{ABCDEF}` {
		t.Fatalf("volume identity = %q", volumeIdentity)
	}
}

func createTestJunction(t *testing.T, link, target string) {
	t.Helper()
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"create directory junction: %v\ncommand output: %s\nOS: %s\nlink parent writable: %t",
			err,
			strings.TrimSpace(string(output)),
			os.Getenv("OS"),
			directoryWritable(filepath.Dir(link)),
		)
	}
	linkInfo, linkErr := os.Stat(link)
	targetInfo, targetErr := os.Stat(target)
	if linkErr != nil || targetErr != nil || !os.SameFile(linkInfo, targetInfo) {
		t.Fatalf(
			"junction target verification failed: link error = %v, target error = %v, same file = %t",
			linkErr,
			targetErr,
			linkErr == nil && targetErr == nil && os.SameFile(linkInfo, targetInfo),
		)
	}
}

func directoryWritable(path string) bool {
	probe, err := os.CreateTemp(path, "junction-probe-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(name)
	return closeErr == nil && removeErr == nil
}
