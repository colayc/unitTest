//go:build windows

package testgenvalidate

import (
	"golang.org/x/sys/windows"
	"os"
)

func singlyLinked(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var info windows.ByHandleFileInformation
	return windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info) == nil && info.NumberOfLinks == 1
}
