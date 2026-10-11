package runtime

import (
	"context"

	"unit-test-ide.local/test-service/internal/toolhash"
)

type coverageToolScope interface {
	Err() error
	Close() error
}

type coverageToolScopeFactory func(context.Context) (context.Context, coverageToolScope, error)

func beginCoverageToolScope(ctx context.Context) (context.Context, coverageToolScope, error) {
	if borrowed, present, err := borrowProductionToolScope(ctx); present || err != nil {
		return ctx, borrowed, err
	}
	scoped, s, err := toolhash.Begin(ctx)
	if err != nil {
		return ctx, nil, err
	}
	if s == nil {
		return scoped, nil, nil
	}
	return scoped, s, nil
}
