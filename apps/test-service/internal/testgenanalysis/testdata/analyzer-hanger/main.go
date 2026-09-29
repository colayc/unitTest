package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	if len(os.Args) > 1 && os.Args[1] == "child" {
		markerRoot := root
		if len(os.Args) > 2 && os.Args[2] != "" {
			markerRoot = os.Args[2]
		}
		_ = os.WriteFile(filepath.Join(markerRoot, "child-entered"), []byte(fmt.Sprintf("pid=%d\ncwd=%s\nexe=%s\nargs=%s\n", os.Getpid(), root, os.Args[0], strings.Join(os.Args, "\x00"))), 0600)
		for {
			if err := os.WriteFile(filepath.Join(markerRoot, "heartbeat"), []byte(strconv.FormatInt(time.Now().UnixNano(), 10)), 0600); err != nil {
				_ = os.WriteFile(filepath.Join(markerRoot, "child-error"), []byte(err.Error()), 0600)
				os.Exit(5)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		os.Exit(3)
	}
	child := exec.Command(executable, "child", root)
	child.Dir = root
	child.Env = []string{}
	if err := child.Start(); err != nil {
		_ = os.WriteFile(filepath.Join(root, "start-error"), []byte(err.Error()), 0600)
		os.Exit(4)
	}
	_ = os.WriteFile(filepath.Join(root, "parent-started"), []byte(fmt.Sprintf("pid=%d\nchild=%d\ncwd=%s\n", os.Getpid(), child.Process.Pid, root)), 0600)
	for {
		time.Sleep(time.Second)
	}
}
