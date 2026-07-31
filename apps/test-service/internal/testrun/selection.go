package testrun

import (
	"context"

	"unit-test-ide.local/test-service/internal/testdomain"
)

type TestRunReader interface {
	GetRun(context.Context, string) (testdomain.TestRun, error)
}

func Resolve(
	ctx context.Context,
	catalog testdomain.Catalog,
	request testdomain.Selection,
	previous TestRunReader,
	limits testdomain.Limits,
) (testdomain.SelectionSnapshot, error) {
	if ctx == nil {
		return testdomain.SelectionSnapshot{}, testdomain.ErrInvalidSelection
	}
	if err := ctx.Err(); err != nil {
		return testdomain.SelectionSnapshot{}, err
	}
	selection, err := testdomain.NewSelection(request)
	if err != nil {
		return testdomain.SelectionSnapshot{}, err
	}
	if selection.Mode != testdomain.SelectionFailedFromRun {
		return testdomain.ResolveSelection(catalog, selection, limits)
	}
	if previous == nil {
		return testdomain.SelectionSnapshot{},
			testdomain.ErrFailedRunResolverRequired
	}
	run, err := previous.GetRun(ctx, selection.RunID)
	if err != nil {
		return testdomain.SelectionSnapshot{}, err
	}
	return resolveFailedRun(catalog, selection.RunID, run, limits)
}
