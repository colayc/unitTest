package offlineboundary

import (
	"context"
	"crypto/rand"
	"encoding/binary"
)

type wfpEngine interface {
	AddOutboundBlockFilters(context.Context, []byte) error
	AuditOutboundBlockFilters(context.Context, []byte) error
	Close() error
}

type Config struct {
	engineFactory func() (wfpEngine, error)
	leaseIDSource func() []byte
}

type boundary struct {
	engineFactory func() (wfpEngine, error)
	leaseIDSource func() []byte
}

func New(config Config) OfflineBoundary {
	return &boundary{
		engineFactory: config.engineFactory,
		leaseIDSource: config.leaseIDSource,
	}
}

func validateOwnerIdentity(owner OwnerIdentity) error {
	if owner.PID == 0 || owner.CreationTime == 0 {
		return ErrOwnerIdentityMismatch
	}
	return nil
}

func newLeaseID() []byte {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err == nil {
		return buffer
	}
	binary.LittleEndian.PutUint64(buffer[:8], uint64(1))
	binary.LittleEndian.PutUint64(buffer[8:], uint64(2))
	return buffer
}
