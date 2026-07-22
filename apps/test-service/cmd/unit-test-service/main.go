package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"unit-test-ide.local/test-service/internal/server"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskfixture"
	"unit-test-ide.local/test-service/internal/transport"
)

const statusHandleEnvironment = "UNIT_TEST_IDE_STATUS_HANDLE"

var processHostEntry = func(stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "platform process host is unavailable")
	return 1
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("unit-test-service", flag.ContinueOnError)
	flags.SetOutput(stderr)
	endpoint := flags.String("endpoint", "", "local IPC endpoint")
	tokenFile := flags.String("token-file", "", "authentication token file")
	prepareTokenFilePath := flags.String("prepare-token-file", "", "create an empty owner-only authentication token file")
	processHost := flags.Bool("process-host", false, "run the internal process host")
	taskFixtureScenario := flags.String("task-fixture", "", "run a built-in task fixture")
	taskFixtureChild := flags.Bool("task-fixture-child", false, "run the internal task fixture child")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "positional arguments are not supported")
		return 2
	}
	prepareModeFlagProvided := false
	serviceModeFlagProvided := false
	processHostFlagProvided := false
	taskFixtureFlagProvided := false
	taskFixtureChildFlagProvided := false
	flags.Visit(func(parsedFlag *flag.Flag) {
		switch parsedFlag.Name {
		case "prepare-token-file":
			prepareModeFlagProvided = true
		case "endpoint", "token-file":
			serviceModeFlagProvided = true
		case "process-host":
			processHostFlagProvided = true
		case "task-fixture":
			taskFixtureFlagProvided = true
		case "task-fixture-child":
			taskFixtureChildFlagProvided = true
		}
	})
	internalModeCount := 0
	for _, provided := range []bool{processHostFlagProvided, taskFixtureFlagProvided, taskFixtureChildFlagProvided} {
		if provided {
			internalModeCount++
		}
	}
	if internalModeCount > 0 {
		if internalModeCount != 1 || serviceModeFlagProvided || prepareModeFlagProvided {
			fmt.Fprintln(stderr, "internal modes cannot be combined with other modes")
			return 2
		}
		switch {
		case processHostFlagProvided:
			if !*processHost || !validStatusHandleEnvironment() {
				fmt.Fprintln(stderr, "process host requires a valid inherited status handle")
				return 2
			}
			return processHostEntry(stdin, stdout, stderr)
		case taskFixtureFlagProvided:
			scenario := task.Scenario(*taskFixtureScenario)
			if !task.ValidScenario(scenario) {
				fmt.Fprintln(stderr, "unknown fixture scenario")
				return 2
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return taskfixture.Run(ctx, scenario, stdout, stderr)
		case taskFixtureChildFlagProvided:
			if !*taskFixtureChild {
				fmt.Fprintln(stderr, "task fixture child mode must be enabled")
				return 2
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			<-ctx.Done()
			return 0
		}
	}
	if prepareModeFlagProvided {
		if serviceModeFlagProvided {
			fmt.Fprintln(stderr, "--prepare-token-file cannot be combined with --endpoint or --token-file")
			return 2
		}
		if *prepareTokenFilePath == "" {
			fmt.Fprintln(stderr, "--prepare-token-file requires a non-empty path")
			return 2
		}
		if err := prepareTokenFile(*prepareTokenFilePath); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if *endpoint == "" || *tokenFile == "" {
		fmt.Fprintln(stderr, "--endpoint and --token-file are required")
		return 2
	}
	token, err := consumeTokenFile(*tokenFile)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	listener, err := transport.Listen(*endpoint)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	service := server.NewService(listener, token, transport.PlatformName(), transport.TransportName(), nil, server.ServiceConfig{MaxConnections: 64})
	go func() { <-ctx.Done(); service.Shutdown() }()
	fmt.Fprintf(stdout, "READY %s\n", *endpoint)
	if err := service.Serve(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func validStatusHandleEnvironment() bool {
	value, ok := os.LookupEnv(statusHandleEnvironment)
	if !ok || value == "" {
		return false
	}
	handle, err := strconv.ParseUint(value, 10, 64)
	return err == nil && handle != 0
}
