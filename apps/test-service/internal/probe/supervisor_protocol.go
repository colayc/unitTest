package probe

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	supervisorProtocolVersion = 1
	maxSupervisorFrame        = 256 * 1024

	supervisorStatusStarted = "started"
	supervisorStatusExited  = "exited"
	supervisorStatusFailed  = "failed"
)

type supervisorRequest struct {
	Version int  `json:"version"`
	Spec    Spec `json:"spec"`
}

type supervisorStatus struct {
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	PID       int    `json:"pid,omitempty"`
	ExitCode  int    `json:"exitCode,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

type supervisedTarget interface {
	PID() int
	Wait() (int, error)
}

type supervisorTargetStarter func(Spec, io.Writer, io.Writer) (supervisedTarget, error)

func runSupervisorProtocol(
	control io.Reader,
	status io.Writer,
	stdout io.Writer,
	stderr io.Writer,
	start supervisorTargetStarter,
) int {
	if control == nil || status == nil || stdout == nil || stderr == nil || start == nil {
		return 2
	}
	var request supervisorRequest
	if err := readSupervisorFrame(control, &request); err != nil ||
		request.Version != supervisorProtocolVersion {
		return 2
	}
	target, err := start(request.Spec, stdout, stderr)
	if err != nil || target == nil || target.PID() <= 1 {
		_ = writeSupervisorFrame(status, supervisorStatus{
			Version: supervisorProtocolVersion, Kind: supervisorStatusFailed, ErrorCode: "start_failed",
		})
		awaitParentRelease(control)
		return 1
	}
	if err := writeSupervisorFrame(status, supervisorStatus{
		Version: supervisorProtocolVersion, Kind: supervisorStatusStarted, PID: target.PID(),
	}); err != nil {
		awaitParentRelease(control)
		return 1
	}
	released := make(chan struct{})
	go func() {
		awaitParentRelease(control)
		close(released)
	}()
	type targetResult struct {
		exitCode int
		err      error
	}
	targetDone := make(chan targetResult, 1)
	go func() {
		exitCode, waitErr := target.Wait()
		targetDone <- targetResult{exitCode: exitCode, err: waitErr}
	}()
	var result targetResult
	select {
	case <-released:
		return 0
	case result = <-targetDone:
	}
	exitStatus := supervisorStatus{
		Version: supervisorProtocolVersion, Kind: supervisorStatusExited, ExitCode: result.exitCode,
	}
	if result.err != nil {
		exitStatus.ExitCode = -1
		exitStatus.ErrorCode = "wait_failed"
	}
	_ = writeSupervisorFrame(status, exitStatus)
	<-released
	if result.err != nil {
		return 1
	}
	return 0
}

func awaitParentRelease(control io.Reader) {
	var buffer [1]byte
	_, _ = control.Read(buffer[:])
}

func writeSupervisorFrame(writer io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) == 0 || len(encoded) > maxSupervisorFrame {
		return fmt.Errorf("supervisor frame exceeds %d bytes", maxSupervisorFrame)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(encoded)))
	if err := writeSupervisorBytes(writer, header[:]); err != nil {
		return err
	}
	return writeSupervisorBytes(writer, encoded)
}

func writeSupervisorBytes(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(value) {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

func readSupervisorFrame(reader io.Reader, destination any) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > maxSupervisorFrame {
		return fmt.Errorf("supervisor frame length %d is invalid", length)
	}
	encoded := make([]byte, int(length))
	if _, err := io.ReadFull(reader, encoded); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing supervisor JSON value")
		}
		return err
	}
	return nil
}

func terminatePinnedSupervisorBoundary(pinSupervisor, signalGroup func() error) error {
	if pinSupervisor == nil || signalGroup == nil {
		return errors.New("invalid supervisor boundary operations")
	}
	if err := pinSupervisor(); err != nil {
		return fmt.Errorf("pin supervisor boundary: %w", err)
	}
	if err := signalGroup(); err != nil {
		return fmt.Errorf("signal pinned supervisor group: %w", err)
	}
	return nil
}
