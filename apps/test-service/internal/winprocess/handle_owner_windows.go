//go:build windows

package winprocess

import (
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// HandleOwner serializes native use and closure of one Windows handle. A
// failed close leaves the raw handle owned and available for another attempt.
type HandleOwner struct {
	mu          sync.RWMutex
	handle      windows.Handle
	closeHandle func(windows.Handle) error
	retrying    bool
}

func NewHandleOwner(handle windows.Handle, closeHandle func(windows.Handle) error) *HandleOwner {
	return &HandleOwner{handle: handle, closeHandle: closeHandle}
}

// Use borrows the raw handle while preventing transfer or closure.
func (owner *HandleOwner) Use(operation func(windows.Handle) error) (bool, error) {
	if owner == nil || operation == nil {
		return false, nil
	}
	owner.mu.RLock()
	defer owner.mu.RUnlock()
	if !validHandle(owner.handle) {
		return false, nil
	}
	return true, operation(owner.handle)
}

// UseExclusive serializes an operation against all users and closure.
func (owner *HandleOwner) UseExclusive(operation func(windows.Handle) error) (bool, error) {
	if owner == nil || operation == nil {
		return false, nil
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if !validHandle(owner.handle) {
		return false, nil
	}
	return true, operation(owner.handle)
}

// UseExclusiveAndCloseEventually performs an exclusive final operation and a
// close as one ownership transaction. A failed close is retained for retry.
func (owner *HandleOwner) UseExclusiveAndCloseEventually(operation func(windows.Handle) error) (bool, error, error) {
	if owner == nil {
		return false, nil, nil
	}
	owner.mu.Lock()
	if !validHandle(owner.handle) {
		owner.mu.Unlock()
		return false, nil, nil
	}
	var operationErr error
	if operation != nil {
		operationErr = operation(owner.handle)
	}
	closeErr := owner.closeLocked()
	if closeErr != nil && !owner.retrying {
		owner.retrying = true
		go owner.retryClose()
	}
	owner.mu.Unlock()
	return true, operationErr, closeErr
}

// Take transfers ownership of the raw handle to the caller.
func (owner *HandleOwner) Take() windows.Handle {
	if owner == nil {
		return 0
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	handle := owner.handle
	owner.handle = 0
	return handle
}

func (owner *HandleOwner) Open() bool {
	if owner == nil {
		return false
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return validHandle(owner.handle)
}

// Close attempts one serialized close. Ownership is cleared only on success.
func (owner *HandleOwner) Close() error {
	if owner == nil {
		return nil
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.closeLocked()
}

// CloseEventually attempts closure now and retains ownership in a close-only
// reaper if the native close fails.
func (owner *HandleOwner) CloseEventually() error {
	if owner == nil {
		return nil
	}
	owner.mu.Lock()
	err := owner.closeLocked()
	if err != nil && !owner.retrying {
		owner.retrying = true
		go owner.retryClose()
	}
	owner.mu.Unlock()
	return err
}

func (owner *HandleOwner) closeLocked() error {
	if !validHandle(owner.handle) {
		return nil
	}
	if owner.closeHandle == nil {
		return windows.ERROR_INVALID_HANDLE
	}
	if err := owner.closeHandle(owner.handle); err != nil {
		return err
	}
	owner.handle = 0
	return nil
}

func (owner *HandleOwner) retryClose() {
	for {
		time.Sleep(10 * time.Millisecond)
		owner.mu.Lock()
		err := owner.closeLocked()
		if err == nil {
			owner.retrying = false
			owner.mu.Unlock()
			return
		}
		owner.mu.Unlock()
	}
}

func validHandle(handle windows.Handle) bool {
	return handle != 0 && handle != windows.InvalidHandle
}
