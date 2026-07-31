package testframework

import "errors"

var ErrInvalidCapabilities = errors.New("invalid test framework capabilities")

// Capabilities describes only behavior that an Adapter has verified for one
// concrete CTest execution descriptor.
type Capabilities struct {
	CanRunContainer         bool
	CanDiscoverCases        bool
	CanRunCase              bool
	CanReportSkipped        bool
	CanReportSourceLocation bool
	CanReportMockDetails    bool
}

func (capabilities Capabilities) validate() error {
	if !capabilities.CanRunContainer {
		return ErrInvalidCapabilities
	}
	if capabilities.CanRunCase && !capabilities.CanDiscoverCases {
		return ErrInvalidCapabilities
	}
	if !capabilities.CanDiscoverCases &&
		(capabilities.CanReportSkipped ||
			capabilities.CanReportSourceLocation ||
			capabilities.CanReportMockDetails) {
		return ErrInvalidCapabilities
	}
	return nil
}

func opaqueCapabilities() Capabilities {
	return Capabilities{CanRunContainer: true}
}
