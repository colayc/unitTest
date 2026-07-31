package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const fixtureCrashExitCode = 86

type fixtureScenario string

const (
	scenarioNormal  fixtureScenario = "normal"
	scenarioCrash   fixtureScenario = "crash"
	scenarioTimeout fixtureScenario = "timeout"
)

type fixtureSelection struct {
	list  bool
	group string
	name  string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, blockForever))
}

func run(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	block func(),
) int {
	scenario, selection, ok := parseArguments(args)
	if !ok || stdout == nil || stderr == nil || block == nil {
		if stderr != nil {
			_, _ = fmt.Fprintf(
				stderr,
				"test-framework-fixture: unsupported arguments: %q\n",
				args,
			)
		}
		return 2
	}
	if selection.list {
		_, err := io.WriteString(stdout, fixtureList(scenario))
		if err != nil {
			_, _ = fmt.Fprintf(
				stderr,
				"test-framework-fixture: write list: %v\n",
				err,
			)
			return 2
		}
		return 0
	}
	output, code, waits, ok := fixtureRun(scenario, selection)
	if !ok {
		_, _ = fmt.Fprintf(
			stderr,
			"test-framework-fixture: unsupported arguments: %q\n",
			args,
		)
		return 2
	}
	if _, err := io.WriteString(stdout, output); err != nil {
		_, _ = fmt.Fprintf(
			stderr,
			"test-framework-fixture: write run: %v\n",
			err,
		)
		return 2
	}
	if waits {
		block()
	}
	return code
}

func parseArguments(
	args []string,
) (fixtureScenario, fixtureSelection, bool) {
	if len(args) < 3 || args[0] != "--fixture-scenario" {
		return "", fixtureSelection{}, false
	}
	scenario := fixtureScenario(args[1])
	switch scenario {
	case scenarioNormal, scenarioCrash, scenarioTimeout:
	default:
		return "", fixtureSelection{}, false
	}
	remaining := args[2:]
	switch {
	case equalArguments(remaining, "-ln"):
		return scenario, fixtureSelection{list: true}, true
	case equalArguments(remaining, "-v"):
		return scenario, fixtureSelection{}, true
	case len(remaining) == 3 &&
		remaining[0] == "-v" &&
		remaining[1] == "-sg" &&
		remaining[2] != "":
		return scenario, fixtureSelection{group: remaining[2]}, true
	case len(remaining) == 5 &&
		remaining[0] == "-v" &&
		remaining[1] == "-sg" &&
		remaining[2] != "" &&
		remaining[3] == "-sn" &&
		remaining[4] != "":
		return scenario, fixtureSelection{
			group: remaining[2],
			name:  remaining[4],
		}, true
	default:
		return "", fixtureSelection{}, false
	}
}

func fixtureList(scenario fixtureScenario) string {
	switch scenario {
	case scenarioNormal:
		return "Fixture.passes Fixture.fails\n"
	case scenarioCrash:
		return "Fixture.passes Fixture.crashes\n"
	case scenarioTimeout:
		return "Fixture.passes Fixture.timesOut\n"
	default:
		return ""
	}
}

func fixtureRun(
	scenario fixtureScenario,
	selection fixtureSelection,
) (string, int, bool, bool) {
	if selection.group != "" && selection.group != "Fixture" {
		return "", 0, false, false
	}
	switch scenario {
	case scenarioNormal:
		return normalRun(selection.name)
	case scenarioCrash:
		return crashRun(selection.name)
	case scenarioTimeout:
		return timeoutRun(selection.name)
	default:
		return "", 0, false, false
	}
}

func normalRun(name string) (string, int, bool, bool) {
	switch name {
	case "":
		return strings.Join([]string{
			"TEST(Fixture, passes) - 1 ms",
			"TEST(Fixture, fails)",
			"fixture_test.cpp:17: error: Failure in TEST(Fixture, fails)",
			"\tExpected <1>",
			"\tbut was  <2>",
			"",
			" - 2 ms",
			"",
			"Errors (1 failures, 2 tests, 2 ran, 2 checks, 0 ignored, 0 filtered out, 3 ms)",
			"",
		}, "\n"), 1, false, true
	case "passes":
		return strings.Join([]string{
			"TEST(Fixture, passes) - 1 ms",
			"",
			"OK (1 tests, 1 ran, 1 checks, 0 ignored, 0 filtered out, 1 ms)",
			"",
		}, "\n"), 0, false, true
	case "fails":
		return strings.Join([]string{
			"TEST(Fixture, fails)",
			"fixture_test.cpp:17: error: Failure in TEST(Fixture, fails)",
			"\tExpected <1>",
			"\tbut was  <2>",
			"",
			" - 2 ms",
			"",
			"Errors (1 failures, 1 tests, 1 ran, 1 checks, 0 ignored, 0 filtered out, 2 ms)",
			"",
		}, "\n"), 1, false, true
	default:
		return "", 0, false, false
	}
}

func crashRun(name string) (string, int, bool, bool) {
	switch name {
	case "":
		return "TEST(Fixture, passes) - 1 ms\nTEST(Fixture, crashes)\n",
			fixtureCrashExitCode, false, true
	case "passes":
		return normalRun("passes")
	case "crashes":
		return "TEST(Fixture, crashes)\n", fixtureCrashExitCode, false, true
	default:
		return "", 0, false, false
	}
}

func timeoutRun(name string) (string, int, bool, bool) {
	switch name {
	case "":
		return "TEST(Fixture, passes) - 1 ms\nTEST(Fixture, timesOut)\n",
			0, true, true
	case "passes":
		return normalRun("passes")
	case "timesOut":
		return "TEST(Fixture, timesOut)\n", 0, true, true
	default:
		return "", 0, false, false
	}
}

func equalArguments(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func blockForever() {
	for {
		time.Sleep(24 * time.Hour)
	}
}
