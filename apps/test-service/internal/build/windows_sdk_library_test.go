package build

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/toolchain"
)

func windowsSDKFixture(t *testing.T) (toolchain.Instance, string) {
	t.Helper()
	sdkRoot := filepath.Join(t.TempDir(), "selected SDK root")
	if err := os.Mkdir(sdkRoot, 0700); err != nil {
		t.Fatal(err)
	}
	sdkRoot = canonicalPlannerFile(t, sdkRoot)
	path := filepath.Join(sdkRoot, "Lib", "10.0.26100.0", "um", "x64", "winmm.lib")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture import library bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	return toolchain.Instance{Family: toolchain.FamilyClangCL, TargetArchitecture: "x64", Sysroot: sdkRoot, Environment: []string{"WINDOWSSDKDIR=" + sdkRoot, "WINDOWSSDKVERSION=10.0.26100.0\\"}}, canonicalPlannerFile(t, path)
}

func TestWindowsSDKLibraryBinding(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows SDK authority")
	}
	instance, path := windowsSDKFixture(t)
	t.Setenv("WINDOWSSDKDIR", t.TempDir())
	t.Setenv("WINDOWSSDKVERSION", "10.0.99999.0\\")
	binding, err := captureWindowsWinMMLibrary(instance)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("fixture import library bytes"))
	if binding.File.Path != path || binding.File.SHA256 != hex.EncodeToString(sum[:]) || binding.SDKIdentity == "" {
		t.Fatalf("selected SDK authority not captured: %#v", binding)
	}
	if err := verifyWindowsWinMMLibrary(binding); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []struct {
		name string
		run  func(*toolchain.Instance)
	}{
		{"no selected environment", func(i *toolchain.Instance) { i.Environment = nil }},
		{"wrong architecture", func(i *toolchain.Instance) { i.TargetArchitecture = "x86" }},
		{"unsupported architecture", func(i *toolchain.Instance) { i.TargetArchitecture = "amd64" }},
		{"unsupported family", func(i *toolchain.Instance) { i.Family = toolchain.FamilyGCC }},
		{"sysroot mismatch", func(i *toolchain.Instance) { i.Sysroot = t.TempDir() }},
		{"captured architecture mismatch", func(i *toolchain.Instance) {
			i.Environment = append(append([]string(nil), i.Environment...), "VSCMD_ARG_TGT_ARCH=x86")
		}},
		{"captured library version mismatch", func(i *toolchain.Instance) {
			i.Environment = append(append([]string(nil), i.Environment...), "WINDOWSSDKLIBVERSION=10.0.22000.0\\")
		}},
		{"unsafe version", func(i *toolchain.Instance) {
			i.Environment = []string{"WINDOWSSDKDIR=" + i.Sysroot, "WINDOWSSDKVERSION=../10.0.26100.0"}
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			i := instance
			mutate.run(&i)
			if _, err := captureWindowsWinMMLibrary(i); err == nil {
				t.Fatal("invalid selected SDK accepted")
			}
		})
	}
	changed := *binding
	changed.SDKIdentity = strings.Repeat("0", 64)
	if verifyWindowsWinMMLibrary(&changed) == nil {
		t.Fatal("changed SDK identity accepted")
	}
	changed = *binding
	changed.TargetArchitecture = "x86"
	if verifyWindowsWinMMLibrary(&changed) == nil {
		t.Fatal("changed architecture accepted")
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if verifyWindowsWinMMLibrary(binding) == nil {
		t.Fatal("changed library accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := captureWindowsWinMMLibrary(instance); err == nil {
		t.Fatal("missing library accepted")
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := captureWindowsWinMMLibrary(instance); err == nil {
		t.Fatal("directory library accepted")
	}
}

func TestWindowsSDKLinkFragmentTokens(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  []string
	}{
		{`"C:\Program Files\SDK\winmm.lib" kernel32.lib`, []string{`C:\Program Files\SDK\winmm.lib`, "kernel32.lib"}},
		{`"C:\SDK\\" winmm.lib`, []string{`C:\SDK\`, "winmm.lib"}},
		{`a\"b.lib`, []string{`a"b.lib`}},
		{`a\\\"b.lib`, []string{`a\"b.lib`}},
		{"", []string{}},
	} {
		got, err := windowsLinkFragmentTokens(tc.value)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("tokens(%q)=%#v,%v want %#v", tc.value, got, err, tc.want)
		}
	}
	for _, value := range []string{`"unterminated`, `""`, "a\x00b", "a\nb", `winmm.lib & evil`, "@libs.rsp", `"@libs.rsp"`, "-Wl,@libs.rsp", strings.Repeat("a", 16*1024+1)} {
		if _, err := windowsLinkFragmentTokens(value); err == nil {
			t.Errorf("unsupported fragment accepted: %q", value)
		}
	}
}

func TestWindowsSDKLinkEvidence(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows SDK authority")
	}
	i, _ := windowsSDKFixture(t)
	binding, err := captureWindowsWinMMLibrary(i)
	if err != nil {
		t.Fatal(err)
	}
	full := `"` + filepath.ToSlash(binding.File.Path) + `"`
	for _, tc := range []struct {
		name, fragment, kind string
		consumers            []string
		ok                   bool
	}{
		{"bound", full, "EXECUTABLE", []string{"app"}, true},
		{"default libraries", full + " kernel32.lib user32.lib", "EXECUTABLE", []string{"app"}, true},
		{"bare", "winmm.lib", "EXECUTABLE", []string{"app"}, false},
		{"other SDK", `"C:/other/Lib/10.0.26100.0/um/x64/winmm.lib"`, "EXECUTABLE", []string{"app"}, false},
		{"substring", `"prefix` + filepath.ToSlash(binding.File.Path) + `suffix"`, "EXECUTABLE", []string{"app"}, false},
		{"extra winmm", full + " winmm.lib", "EXECUTABLE", []string{"app"}, false},
		{"implicit option", full + " -lwinmm", "EXECUTABLE", []string{"app"}, false},
		{"prefixed implicit", full + " /WHOLEARCHIVE:winmm.lib", "EXECUTABLE", []string{"app"}, false},
		{"response", full + " @unchecked.rsp", "EXECUTABLE", []string{"app"}, false},
		{"static", full, "STATIC_LIBRARY", []string{"app"}, false},
		{"missing fragments", "", "EXECUTABLE", []string{"app"}, false},
		{"missing consumer", full, "EXECUTABLE", []string{"other"}, false},
		{"empty consumers", full, "EXECUTABLE", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := cmake.Target{Name: "app", Type: tc.kind, LinkFragments: []cmake.LinkCommandFragment{{Role: "libraries", Fragment: tc.fragment}}}
			err := verifyWindowsLinkLibraryEvidence(binding, []cmake.Target{target}, tc.consumers)
			if (err == nil) != tc.ok {
				t.Fatalf("evidence error=%v want success=%v", err, tc.ok)
			}
		})
	}
	short, err := cmakeLaunchPath(binding.File.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(short, binding.File.Path) {
		target := cmake.Target{Name: "app", Type: "EXECUTABLE", LinkFragments: []cmake.LinkCommandFragment{{Role: "libraries", Fragment: `"` + short + `"`}}}
		if verifyWindowsLinkLibraryEvidence(binding, []cmake.Target{target}, []string{"app"}) == nil {
			t.Fatal("alternate pathname spelling accepted as bound library")
		}
	}
}

func TestWindowsSDKLibraryLimitsAndLease(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows SDK authority")
	}
	i, path := windowsSDKFixture(t)
	binding, err := captureWindowsWinMMLibrary(i)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := cmake.RetainLaunchInputs([]cmake.FingerprintFile{binding.File}, 512*1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed while retained"), 0600); err == nil {
		t.Fatal("SDK library write escaped retained launch input")
	}
	if err := os.Remove(path); err == nil {
		t.Fatal("SDK library delete escaped retained launch input")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, 4*1024*1024+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureWindowsWinMMLibrary(i); err == nil {
		t.Fatal("oversized library accepted")
	}
}

func TestWindowsSDKLibraryRejectsRootReplacementAndAliases(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows SDK authority")
	}
	i, path := windowsSDKFixture(t)
	binding, err := captureWindowsWinMMLibrary(i)
	if err != nil {
		t.Fatal(err)
	}
	oldRoot := i.Sysroot + "-old"
	if err := os.Rename(i.Sysroot, oldRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture import library bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if verifyWindowsWinMMLibrary(binding) == nil {
		t.Fatal("same bytes under replaced SDK root accepted")
	}
	alias := filepath.Join(filepath.Dir(i.Sysroot), "alias")
	if err := os.Symlink(i.Sysroot, alias); err != nil {
		t.Logf("directory symlink unavailable: %v", err)
		return
	}
	i.Sysroot = alias
	i.Environment = []string{"WINDOWSSDKDIR=" + alias, "WINDOWSSDKVERSION=10.0.26100.0\\"}
	if _, err := captureWindowsWinMMLibrary(i); err == nil {
		t.Fatal("SDK directory alias accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(oldRoot, "Lib", "10.0.26100.0", "um", "x64", "winmm.lib"), path); err != nil {
		t.Fatal(err)
	}
	i.Sysroot = binding.SDKRoot
	i.Environment = []string{"WINDOWSSDKDIR=" + i.Sysroot, "WINDOWSSDKVERSION=10.0.26100.0\\"}
	if _, err := captureWindowsWinMMLibrary(i); err == nil {
		t.Fatal("SDK library alias accepted")
	}
}
