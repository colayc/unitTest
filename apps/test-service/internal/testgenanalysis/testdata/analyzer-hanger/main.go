package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	if len(os.Args) > 1 && os.Args[1] == "child" {
		for {
			_ = os.WriteFile(filepath.Join(root, "heartbeat"), []byte(strconv.FormatInt(time.Now().UnixNano(), 10)), 0600)
			time.Sleep(20 * time.Millisecond)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		os.Exit(3)
	}
	child := exec.Command(executable, "child")
	child.Dir = root
	child.Env = []string{}
	if err := child.Start(); err != nil {
		_ = os.WriteFile(filepath.Join(root, "start-error"), []byte(err.Error()), 0600)
		os.Exit(4)
	}
	for {
		time.Sleep(time.Second)
	}
}
