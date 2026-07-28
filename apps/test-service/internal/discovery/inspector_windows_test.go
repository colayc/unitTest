//go:build windows

package discovery

import (
	"os/exec"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestInspectorBuildRootBoundaryIsLexicalAndDoesNotFollowJunction(t *testing.T) {
	root := openProjectRoot(t, ".")
	outside := t.TempDir()
	alias := filepath.Join(root.NativePath, "outside-alias")
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", alias, outside)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("directory junction unavailable: %v (%s)", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("cmd.exe", "/d", "/c", "rmdir", alias).Run()
	})
	registry, err := toolchain.NewRegistry(fakeAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewInspector(
		root, fakeRunner{}, cmake.ResolverConfig{}, registry,
		filepath.Join(alias, "service-build"),
	)
	if err == nil {
		t.Fatal("constructor followed junction and accepted a lexical workspace child")
	}
}
