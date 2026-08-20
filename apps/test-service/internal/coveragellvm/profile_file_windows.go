//go:build windows

package coveragellvm

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type sealedFileIdentity = nativeFileIdentity

func openSealedProfile(
	path string,
) (*os.File, os.FileInfo, sealedFileIdentity, error) {
	file, err := openWindowsObject(
		path,
		false,
		windows.GENERIC_READ|windows.FILE_READ_ATTRIBUTES,
	)
	if err != nil {
		return nil, nil, sealedFileIdentity{}, err
	}
	fail := func(cause error) (*os.File, os.FileInfo, sealedFileIdentity, error) {
		_ = file.Close()
		return nil, nil, sealedFileIdentity{}, cause
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fail(errors.New("profile is not regular"))
	}
	identity, err := identityFromHandle(windows.Handle(file.Fd()))
	if err != nil || identity.attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		identity.links != 1 || identity.indexHigh == 0 && identity.indexLow == 0 {
		return fail(errors.New("profile is indirect or multiply linked"))
	}
	if err := verifySealedProfile(path, file, info, identity); err != nil {
		return fail(err)
	}
	return file, info, identity, nil
}

func verifySealedProfile(
	path string,
	file *os.File,
	info os.FileInfo,
	identity sealedFileIdentity,
) error {
	if file == nil || info == nil ||
		rejectReparseAncestors(filepath.Dir(path)) != nil {
		return errors.New("profile snapshot is closed")
	}
	before, err := windowsPathIdentity(path, false)
	if err != nil || before != identity {
		return errors.New("profile path identity changed")
	}
	current, err := identityFromHandle(windows.Handle(file.Fd()))
	if err != nil || current != identity || current.links != 1 ||
		current.attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("profile handle identity changed")
	}
	currentInfo, err := file.Stat()
	if err != nil || !currentInfo.Mode().IsRegular() ||
		!os.SameFile(info, currentInfo) || currentInfo.Size() != info.Size() {
		return errors.New("profile information changed")
	}
	after, err := windowsPathIdentity(path, false)
	if err != nil || after != identity {
		return errors.New("profile changed while validating")
	}
	return nil
}
