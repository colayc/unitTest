//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"

	"unit-test-ide.local/test-service/internal/offlineboundary"
)

func main() {
	var ownerPID uint
	var ownerCreationTime uint64
	var ipcHandle uint64

	flag.UintVar(&ownerPID, "owner-pid", 0, "owner process id")
	flag.Uint64Var(&ownerCreationTime, "owner-creation-time", 0, "owner process creation time")
	flag.Uint64Var(&ipcHandle, "ipc-handle", 0, "inherited guardian ipc handle")
	flag.Parse()

	err := offlineboundary.RunNativeGuardian(
		offlineboundary.OwnerIdentity{PID: uint32(ownerPID), CreationTime: ownerCreationTime},
		uintptr(ipcHandle),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
