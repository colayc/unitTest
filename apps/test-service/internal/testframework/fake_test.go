package testframework

import (
	"context"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/testdomain"
)

type fakeAdapter struct {
	framework    testdomain.Framework
	version      string
	capabilities Capabilities
	verifyErr    error
	verifyCalls  int
}

func (adapter *fakeAdapter) Framework() testdomain.Framework {
	return adapter.framework
}

func (adapter *fakeAdapter) ContractVersion() string {
	return adapter.version
}

func (adapter *fakeAdapter) Verify(
	_ context.Context,
	_ ctest.ExecutionDescriptor,
) (Capabilities, error) {
	adapter.verifyCalls++
	if adapter.verifyErr != nil {
		return Capabilities{}, adapter.verifyErr
	}
	if adapter.capabilities == (Capabilities{}) {
		return Capabilities{
			CanRunContainer:         true,
			CanDiscoverCases:        true,
			CanRunCase:              true,
			CanReportSkipped:        true,
			CanReportSourceLocation: true,
		}, nil
	}
	return adapter.capabilities, nil
}

func (*fakeAdapter) Discover(context.Context, ctest.ExecutionDescriptor) (DiscoveryResult, error) {
	return DiscoveryResult{}, nil
}

func (*fakeAdapter) PlanRun(context.Context, RunInput) (RunPlan, error) {
	return RunPlan{}, nil
}

func (*fakeAdapter) NewParser(ParseInput) (ResultParser, error) {
	return &fakeParser{}, nil
}

type fakeParser struct{}

func (*fakeParser) Write(Stream, []byte) error {
	return nil
}

func (*fakeParser) Finish(ProcessResult) (ParseResult, error) {
	return ParseResult{}, nil
}
