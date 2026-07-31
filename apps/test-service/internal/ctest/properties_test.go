package ctest

import (
	"slices"
	"testing"
)

func TestClassifyPropertiesAcceptsClosedCaseLevelAllowlist(t *testing.T) {
	properties := []Property{
		{Name: "WORKING_DIRECTORY", Value: PropertyValue{Kind: PropertyString, String: "/build/tests"}},
		{Name: "ENVIRONMENT", Value: PropertyValue{Kind: PropertyStrings, Strings: []string{"MODE=fast", "EMPTY="}}},
		{Name: "ENVIRONMENT_MODIFICATION", Value: PropertyValue{
			Kind: PropertyStrings,
			Strings: []string{
				"PATH=path_list_prepend:/trusted/bin",
				"MODE=string_append:-debug",
				"OLD=unset:",
			},
		}},
		{Name: "TIMEOUT", Value: PropertyValue{Kind: PropertyNumber, Number: "30.5"}},
		{Name: "LABELS", Value: PropertyValue{Kind: PropertyStrings, Strings: []string{"fast", "unit"}}},
		{Name: "DISABLED", Value: PropertyValue{Kind: PropertyBoolean, Boolean: true}},
		{Name: "SKIP_RETURN_CODE", Value: PropertyValue{Kind: PropertyNumber, Number: "77"}},
		{Name: "RUN_SERIAL", Value: PropertyValue{Kind: PropertyBoolean, Boolean: true}},
	}
	settings, compatibility := ClassifyProperties(properties)
	if !compatibility.CaseLevel || !compatibility.RunSerial || len(compatibility.Reasons) != 0 {
		t.Fatalf("compatibility = %#v", compatibility)
	}
	if settings.WorkingDirectory != "/build/tests" ||
		len(settings.Environment) != 2 ||
		len(settings.EnvironmentModifications) != 3 ||
		settings.TimeoutSeconds == nil || *settings.TimeoutSeconds != 30.5 ||
		!settings.Disabled ||
		settings.SkipReturnCode == nil || *settings.SkipReturnCode != 77 ||
		!slices.Equal(settings.Labels, []string{"fast", "unit"}) {
		t.Fatalf("settings = %#v", settings)
	}
}

func TestClassifyPropertiesDegradesBehaviorChangingAndUnknownProperties(t *testing.T) {
	for _, property := range []string{
		"FIXTURES_SETUP", "FIXTURES_CLEANUP", "FIXTURES_REQUIRED", "DEPENDS",
		"RESOURCE_LOCK", "RESOURCE_GROUPS", "WILL_FAIL",
		"PASS_REGULAR_EXPRESSION", "FAIL_REGULAR_EXPRESSION", "SKIP_REGULAR_EXPRESSION",
		"REQUIRED_FILES", "TIMEOUT_AFTER_MATCH", "PROCESSORS", "UNKNOWN_FUTURE_PROPERTY",
	} {
		t.Run(property, func(t *testing.T) {
			_, compatibility := ClassifyProperties([]Property{{
				Name: property, Value: PropertyValue{Kind: PropertyString, String: "value"},
			}})
			if compatibility.CaseLevel ||
				!slices.Contains(compatibility.Reasons, ReasonUnsupportedProperty) {
				t.Fatalf("compatibility = %#v", compatibility)
			}
		})
	}
}

func TestClassifyPropertiesRejectsInvalidAllowedPropertyValues(t *testing.T) {
	cases := map[string]Property{
		"working directory type": {
			Name: "WORKING_DIRECTORY", Value: PropertyValue{Kind: PropertyBoolean, Boolean: true},
		},
		"environment key": {
			Name: "ENVIRONMENT", Value: PropertyValue{Kind: PropertyStrings, Strings: []string{"BAD-NAME=value"}},
		},
		"environment reserved key": {
			Name: "ENVIRONMENT", Value: PropertyValue{Kind: PropertyStrings, Strings: []string{"UTIDE_CONTROL=value"}},
		},
		"environment duplicate key": {
			Name: "ENVIRONMENT", Value: PropertyValue{Kind: PropertyStrings, Strings: []string{"MODE=a", "MODE=b"}},
		},
		"environment modification operation": {
			Name:  "ENVIRONMENT_MODIFICATION",
			Value: PropertyValue{Kind: PropertyStrings, Strings: []string{"MODE=future:value"}},
		},
		"timeout": {
			Name: "TIMEOUT", Value: PropertyValue{Kind: PropertyNumber, Number: "-1"},
		},
		"disabled": {
			Name: "DISABLED", Value: PropertyValue{Kind: PropertyString, String: "true"},
		},
		"skip return code": {
			Name: "SKIP_RETURN_CODE", Value: PropertyValue{Kind: PropertyNumber, Number: "1.5"},
		},
		"run serial": {
			Name: "RUN_SERIAL", Value: PropertyValue{Kind: PropertyString, String: "ON"},
		},
	}
	for name, property := range cases {
		t.Run(name, func(t *testing.T) {
			_, compatibility := ClassifyProperties([]Property{property})
			if compatibility.CaseLevel ||
				!slices.Contains(compatibility.Reasons, ReasonInvalidProperty) {
				t.Fatalf("compatibility = %#v", compatibility)
			}
		})
	}
}

func TestClassifyPropertiesDegradesDuplicateProperty(t *testing.T) {
	_, compatibility := ClassifyProperties([]Property{
		{Name: "LABELS", Value: PropertyValue{Kind: PropertyStrings}},
		{Name: "LABELS", Value: PropertyValue{Kind: PropertyStrings}},
	})
	if compatibility.CaseLevel ||
		!slices.Contains(compatibility.Reasons, ReasonDuplicateProperty) {
		t.Fatalf("compatibility = %#v", compatibility)
	}
}
