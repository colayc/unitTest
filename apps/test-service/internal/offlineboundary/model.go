package offlineboundary

import "context"

type OwnerIdentity struct {
	PID          uint32
	CreationTime uint64
}

type OfflineBoundary interface {
	Start(context.Context, OwnerIdentity) (Lease, error)
}

type Lease interface {
	Ready() <-chan struct{}
	Close() error
	Wait() error
}
