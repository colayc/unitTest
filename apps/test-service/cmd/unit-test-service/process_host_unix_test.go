//go:build linux

package main

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestInterruptibleProcessHostControlSupportsDeadlineAndClose(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()

	control, err := newInterruptibleProcessHostControl(reader)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	if err := control.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := control.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Read error = %v, want deadline exceeded", err)
	}
	if err := control.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("clear read deadline: %v", err)
	}

	reading := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(reading)
		_, err := control.Read(make([]byte, 1))
		done <- err
	}()
	<-reading
	time.Sleep(10 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("Read returned before Close: %v", err)
	default:
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Read returned nil after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock Read")
	}
}
