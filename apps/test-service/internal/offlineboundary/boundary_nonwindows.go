//go:build !windows

package offlineboundary

import "context"

func (boundary *boundary) Start(_ context.Context, owner OwnerIdentity) (Lease, error) {
	if err := validateOwnerIdentity(owner); err != nil {
		return nil, err
	}
	return nil, ErrUnsupported
}
