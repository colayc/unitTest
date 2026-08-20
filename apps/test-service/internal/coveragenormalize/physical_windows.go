//go:build windows

package coveragenormalize

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func physicalSourceIdentity(path string) (physicalSourceID, error) {
	file, err := os.Open(path)
	if err != nil {
		return physicalSourceID{}, fmt.Errorf("%w: open identity: %v", ErrSourceIdentity, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return physicalSourceID{}, ErrSourceIdentity
	}
	var identity windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &identity); err != nil {
		return physicalSourceID{}, fmt.Errorf("%w: read identity: %v", ErrSourceIdentity, err)
	}
	return physicalSourceID{
		device: uint64(identity.VolumeSerialNumber),
		file:   uint64(identity.FileIndexHigh)<<32 | uint64(identity.FileIndexLow),
	}, nil
}
