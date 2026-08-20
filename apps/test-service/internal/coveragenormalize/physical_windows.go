//go:build windows

package coveragenormalize

import (
	"os"

	"golang.org/x/sys/windows"
)

func physicalSourceIdentity(file *os.File) (physicalSourceID, error) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return physicalSourceID{}, ErrSourceIdentity
	}
	var identity windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &identity); err != nil {
		return physicalSourceID{}, ErrSourceIdentity
	}
	return physicalSourceID{
		device: uint64(identity.VolumeSerialNumber),
		file:   uint64(identity.FileIndexHigh)<<32 | uint64(identity.FileIndexLow),
	}, nil
}
