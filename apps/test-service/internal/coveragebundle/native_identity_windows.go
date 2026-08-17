//go:build windows

package coveragebundle

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

type nativeIdentityToken struct {
	volume uint32
	index  uint64
}

func captureNativeIdentity(file *os.File) (any, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return nil, err
	}
	token := nativeIdentityToken{volume: info.VolumeSerialNumber, index: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)}
	if token.volume == 0 || token.index == 0 {
		return nil, errors.New("native identity unavailable")
	}
	return token, nil
}

func verifyNativeIdentity(path string, expected any) error {
	file, err := openPinnedWindowsObject(path, true)
	if err != nil {
		return err
	}
	defer file.Close()
	actual, err := captureNativeIdentity(file)
	if err != nil || actual != expected {
		return errors.New("native identity changed")
	}
	return nil
}
