//go:build windows

package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/windows"
	"unit-test-ide.local/test-service/internal/toolchain"
)

func testCoverageToolchainEvidence(t *testing.T, instance toolchain.Instance) toolchain.Instance {
	t.Helper()
	paths := []string{instance.CXXCompiler, instance.Coverage.LLVMProfdata, instance.Coverage.LLVMCov}
	evidence := make([]toolchain.ExecutableEvidence, len(paths))
	for index, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		pointer, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := windows.CreateFile(pointer, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		var info windows.ByHandleFileInformation
		err = windows.GetFileInformationByHandle(handle, &info)
		_ = windows.CloseHandle(handle)
		if err != nil {
			t.Fatal(err)
		}
		evidence[index] = toolchain.ExecutableEvidence{FileIdentity: fmt.Sprintf("windows:%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), SHA256: hex.EncodeToString(sum[:])}
	}
	instance.Coverage.CompilerEvidence, instance.Coverage.ProfdataEvidence, instance.Coverage.CovEvidence = evidence[0], evidence[1], evidence[2]
	instance.Coverage.ToolsetIdentity = toolchain.LLVMToolsetIdentity(instance.Version, paths, evidence)
	return instance
}
