package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFixtureListsAndRunsFixedCppUTestScenarios(t *testing.T) {
	tests := map[string]struct {
		args     []string
		wantCode int
		want     []string
	}{
		"list normal": {
			args: []string{"--fixture-scenario", "normal", "-ln"},
			want: []string{"Fixture.passes", "Fixture.fails"},
		},
		"run passing case": {
			args: []string{
				"--fixture-scenario", "normal",
				"-v", "-sg", "Fixture", "-sn", "passes",
			},
			want: []string{
				"TEST(Fixture, passes) - 1 ms",
				"OK (1 tests, 1 ran, 1 checks, 0 ignored, 0 filtered out, 1 ms)",
			},
		},
		"run failing case": {
			args: []string{
				"--fixture-scenario", "normal",
				"-v", "-sg", "Fixture", "-sn", "fails",
			},
			wantCode: 1,
			want: []string{
				"Failure in TEST(Fixture, fails)",
				"Errors (1 failures, 1 tests, 1 ran, 1 checks, 0 ignored, 0 filtered out, 2 ms)",
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(test.args, &stdout, &stderr, func() {})
			if code != test.wantCode || stderr.Len() != 0 {
				t.Fatalf(
					"run(%q) = %d, stdout=%q, stderr=%q",
					test.args,
					code,
					stdout.String(),
					stderr.String(),
				)
			}
			for _, fragment := range test.want {
				if !strings.Contains(stdout.String(), fragment) {
					t.Fatalf("stdout %q does not contain %q", stdout.String(), fragment)
				}
			}
		})
	}
}

func TestFixtureProducesDeterministicCrashAndTimeoutPrefixes(t *testing.T) {
	t.Run("crash", func(t *testing.T) {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run(
			[]string{"--fixture-scenario", "crash", "-v"},
			&stdout,
			&stderr,
			func() {},
		)
		if code != fixtureCrashExitCode ||
			stderr.Len() != 0 ||
			!strings.Contains(stdout.String(), "TEST(Fixture, passes) - 1 ms") ||
			!strings.HasSuffix(stdout.String(), "TEST(Fixture, crashes)\n") ||
			strings.Contains(stdout.String(), "OK (") ||
			strings.Contains(stdout.String(), "Errors (") {
			t.Fatalf(
				"crash result = %d, stdout=%q, stderr=%q",
				code,
				stdout.String(),
				stderr.String(),
			)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		blocked := false
		code := run(
			[]string{"--fixture-scenario", "timeout", "-v"},
			&stdout,
			&stderr,
			func() { blocked = true },
		)
		if code != 0 || !blocked || stderr.Len() != 0 ||
			!strings.Contains(stdout.String(), "TEST(Fixture, passes) - 1 ms") ||
			!strings.HasSuffix(stdout.String(), "TEST(Fixture, timesOut)\n") ||
			strings.Contains(stdout.String(), "OK (") ||
			strings.Contains(stdout.String(), "Errors (") {
			t.Fatalf(
				"timeout result = %d, blocked=%t, stdout=%q, stderr=%q",
				code,
				blocked,
				stdout.String(),
				stderr.String(),
			)
		}
	})
}

func TestFixtureRejectsUnknownScenarioAndArguments(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown scenario": {
			"--fixture-scenario", "client-command", "-ln",
		},
		"missing scenario": {
			"--fixture-scenario", "-ln",
		},
		"unknown option": {
			"--fixture-scenario", "normal", "--command", "calc.exe",
		},
		"client working directory": {
			"--fixture-scenario", "normal", "--cwd", "..",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(args, &stdout, &stderr, func() {})
			if code != 2 || stdout.Len() != 0 ||
				!strings.Contains(stderr.String(), "unsupported arguments") {
				t.Fatalf(
					"run(%q) = %d, stdout=%q, stderr=%q",
					args,
					code,
					stdout.String(),
					stderr.String(),
				)
			}
		})
	}
}
