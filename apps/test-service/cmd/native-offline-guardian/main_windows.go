//go:build windows

package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"unit-test-ide.local/test-service/internal/offlineboundary"
)

func main() {
	var ownerPID uint
	var ownerCreationTime uint64
	var ipcAddress string
	var printOwnerCreationTime uint

	flag.UintVar(&ownerPID, "owner-pid", 0, "owner process id")
	flag.Uint64Var(&ownerCreationTime, "owner-creation-time", 0, "owner process creation time")
	flag.StringVar(&ipcAddress, "ipc-address", "", "guardian ipc address")
	flag.UintVar(&printOwnerCreationTime, "print-owner-creation-time", 0, "print numeric owner creation time for PID")
	flag.Parse()
	if printOwnerCreationTime != 0 {
		if printOwnerCreationTime > uint(^uint32(0)) {
			fmt.Fprintln(os.Stderr, "owner creation time unavailable")
			os.Exit(1)
		}
		creationTime, err := offlineboundary.OwnerCreationTime(uint32(printOwnerCreationTime))
		if err != nil || creationTime == 0 {
			fmt.Fprintln(os.Stderr, "owner creation time unavailable")
			os.Exit(1)
		}
		fmt.Printf("%d\n", creationTime)
		return
	}

	bootstrap := make([]byte, 80)
	if _, err := io.ReadFull(os.Stdin, bootstrap); err != nil {
		fmt.Fprintln(os.Stderr, offlineboundary.GuardianStartFailed)
		os.Exit(1)
	}
	err := offlineboundary.RunNativeGuardian(
		offlineboundary.OwnerIdentity{PID: uint32(ownerPID), CreationTime: ownerCreationTime},
		ipcAddress,
		bootstrap[:32],
		bootstrap[32:48],
		bootstrap[48:80],
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
