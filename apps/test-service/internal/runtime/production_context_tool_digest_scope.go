package runtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/toolhash"
)

type productionToolScopeFactory func(context.Context) (context.Context, *toolhash.Scope, coverageToolScope, error)
type productionToolScopeKey struct{}
type productionToolScopeBinding struct {
	scope *toolhash.Scope
	query coveragedetail.CurrentIndexQuery
}

func beginProductionToolScope(ctx context.Context) (context.Context, *toolhash.Scope, coverageToolScope, error) {
	scoped, scope, err := toolhash.Begin(ctx)
	if scope == nil {
		return scoped, nil, nil, err
	}
	return scoped, scope, scope, err
}
func validateProductionToolScopeQuery(ctx context.Context, q coveragedetail.CurrentIndexQuery) error {
	if ctx == nil {
		return task.ErrInvalidArgument
	}
	raw := ctx.Value(productionToolScopeKey{})
	if raw == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	binding, ok := raw.(*productionToolScopeBinding)
	if !ok || binding == nil || binding.scope == nil {
		return task.ErrStorageUnavailable
	}
	if binding.query.ProjectID != q.ProjectID || binding.query.WorkspaceGeneration != q.WorkspaceGeneration || binding.query.ReportID != q.ReportID {
		return task.ErrInvalidArgument
	}
	if binding.scope.ValidateContext(ctx) != nil {
		return task.ErrStorageUnavailable
	}
	return nil
}
func borrowProductionToolScope(ctx context.Context) (coverageToolScope, bool, error) {
	if ctx == nil {
		return nil, false, task.ErrInvalidArgument
	}
	raw := ctx.Value(productionToolScopeKey{})
	if raw == nil {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	binding, ok := raw.(*productionToolScopeBinding)
	if !ok || binding == nil || binding.scope == nil {
		return nil, true, task.ErrStorageUnavailable
	}
	if binding.scope.ValidateContext(ctx) != nil {
		return nil, true, task.ErrStorageUnavailable
	}
	return &borrowedProductionToolScope{ctx: ctx, scope: binding.scope}, true, nil
}

type borrowedProductionToolScope struct {
	ctx      context.Context
	scope    *toolhash.Scope
	closed   atomic.Bool
	once     sync.Once
	closeErr error
}

func (b *borrowedProductionToolScope) Err() error {
	if b.closed.Load() {
		return errors.New("tool hash borrow ended")
	}
	return b.scope.ValidateContext(b.ctx)
}
func (b *borrowedProductionToolScope) Close() error {
	b.once.Do(func() {
		b.closeErr = b.scope.ValidateContext(b.ctx)
		b.closed.Store(true)
	})
	return b.closeErr
}
