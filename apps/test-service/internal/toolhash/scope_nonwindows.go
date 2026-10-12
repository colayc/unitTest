//go:build !windows

package toolhash

import (
	"context"
	"errors"
	"os"
)

func Begin(ctx context.Context) (context.Context, *Scope, error) {
	if ctx == nil {
		return nil, nil, errors.New("nil tool hash context")
	}
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	return ctx, nil, nil
}
func Digest(ctx context.Context, _ string, _ *os.File, _ int64) (Result, bool, error) {
	if ctx == nil {
		return Result{}, false, errors.New("nil tool hash context")
	}
	return Result{}, false, ctx.Err()
}

func (e *Evidence) Verify(context.Context, string, *os.File, int64) (string, error) {
	return "", errors.New("tool hash evidence unavailable")
}
