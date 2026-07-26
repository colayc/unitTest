//go:build linux

package linuxprocess

import (
	"errors"
	"os/exec"
	"runtime"
	"sync"
)

// ParentThread keeps the OS thread that created a Pdeathsig child alive until
// the child has been reaped. Linux ties Pdeathsig to that thread, not to the
// lifetime of the whole Go process.
type ParentThread struct {
	release chan struct{}
	once    sync.Once
}

// Start starts command on a dedicated locked OS thread. The caller must call
// Release only after command.Wait has returned.
func Start(command *exec.Cmd) (*ParentThread, error) {
	if command == nil {
		return nil, errors.New("invalid command")
	}
	parent := &ParentThread{release: make(chan struct{})}
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := command.Start()
		started <- err
		if err == nil {
			<-parent.release
		}
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return parent, nil
}

// Release is idempotent.
func (parent *ParentThread) Release() {
	if parent == nil {
		return
	}
	parent.once.Do(func() { close(parent.release) })
}
