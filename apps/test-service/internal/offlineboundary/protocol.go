package offlineboundary

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const maxGuardianFramePayloadSize = 64

var (
	errGuardianFrameTooLarge = errors.New("guardian frame too large")
	errGuardianFrameInvalid  = errors.New("guardian frame invalid")
)

type guardianFrameKind byte

const (
	guardianFrameHello guardianFrameKind = iota + 1
	guardianFrameReady
	guardianFrameRelease
	guardianFrameError
	guardianFrameBye
	guardianFrameAuthenticate
)

type guardianErrorCode byte

const (
	guardianErrorStartup            guardianErrorCode = 1
	guardianErrorWFPAccessDenied    guardianErrorCode = 2
	guardianErrorSessionCloseFailed guardianErrorCode = 3
)

type guardianFrame struct {
	Kind                 guardianFrameKind
	Code                 guardianErrorCode
	GuardianPID          uint32
	GuardianCreationTime uint64
	OwnerPID             uint32
	OwnerCreationTime    uint64
	Proof                [32]byte
}

func readGuardianFrame(reader io.Reader) (guardianFrame, error) {
	payload, err := readGuardianWireFrame(reader)
	if err != nil {
		return guardianFrame{}, err
	}
	if len(payload) == 0 || len(payload) > maxGuardianFramePayloadSize {
		return guardianFrame{}, errGuardianFrameTooLarge
	}
	frame := guardianFrame{Kind: guardianFrameKind(payload[0])}
	switch frame.Kind {
	case guardianFrameHello, guardianFrameReady, guardianFrameRelease, guardianFrameBye:
		if len(payload) != 1 {
			return guardianFrame{}, errGuardianFrameInvalid
		}
	case guardianFrameError:
		if len(payload) != 2 {
			return guardianFrame{}, errGuardianFrameInvalid
		}
		frame.Code = guardianErrorCode(payload[1])
		if !validGuardianErrorCode(frame.Code) {
			return guardianFrame{}, errGuardianFrameInvalid
		}
	case guardianFrameAuthenticate:
		if len(payload) != 57 {
			return guardianFrame{}, errGuardianFrameInvalid
		}
		frame.GuardianPID = binary.LittleEndian.Uint32(payload[1:5])
		frame.GuardianCreationTime = binary.LittleEndian.Uint64(payload[5:13])
		frame.OwnerPID = binary.LittleEndian.Uint32(payload[13:17])
		frame.OwnerCreationTime = binary.LittleEndian.Uint64(payload[17:25])
		copy(frame.Proof[:], payload[25:57])
		if frame.GuardianPID == 0 || frame.GuardianCreationTime == 0 || frame.OwnerPID == 0 || frame.OwnerCreationTime == 0 {
			return guardianFrame{}, errGuardianFrameInvalid
		}
	default:
		return guardianFrame{}, errGuardianFrameInvalid
	}
	return frame, nil
}

func writeGuardianFrame(writer io.Writer, frame guardianFrame) error {
	payload := []byte{byte(frame.Kind)}
	switch frame.Kind {
	case guardianFrameHello, guardianFrameReady, guardianFrameRelease, guardianFrameBye:
	case guardianFrameError:
		if !validGuardianErrorCode(frame.Code) {
			return errGuardianFrameInvalid
		}
		payload = append(payload, byte(frame.Code))
	case guardianFrameAuthenticate:
		if frame.GuardianPID == 0 || frame.GuardianCreationTime == 0 || frame.OwnerPID == 0 || frame.OwnerCreationTime == 0 {
			return errGuardianFrameInvalid
		}
		payload = make([]byte, 57)
		payload[0] = byte(frame.Kind)
		binary.LittleEndian.PutUint32(payload[1:5], frame.GuardianPID)
		binary.LittleEndian.PutUint64(payload[5:13], frame.GuardianCreationTime)
		binary.LittleEndian.PutUint32(payload[13:17], frame.OwnerPID)
		binary.LittleEndian.PutUint64(payload[17:25], frame.OwnerCreationTime)
		copy(payload[25:57], frame.Proof[:])
	default:
		return fmt.Errorf("%w: kind %d", errGuardianFrameInvalid, frame.Kind)
	}
	if len(payload) > maxGuardianFramePayloadSize {
		return errGuardianFrameTooLarge
	}
	return writeGuardianWireFrame(writer, payload)
}

func readGuardianWireFrame(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(header[:])
	if size == 0 {
		return nil, errGuardianFrameInvalid
	}
	if size > maxGuardianFramePayloadSize {
		return nil, errGuardianFrameTooLarge
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeGuardianWireFrame(writer io.Writer, payload []byte) error {
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err := writer.Write(payload)
	return err
}

func validGuardianErrorCode(code guardianErrorCode) bool {
	switch code {
	case guardianErrorStartup, guardianErrorWFPAccessDenied, guardianErrorSessionCloseFailed:
		return true
	default:
		return false
	}
}
