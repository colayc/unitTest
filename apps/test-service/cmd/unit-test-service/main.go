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
	"time"

	serviceruntime "unit-test-ide.local/test-service/internal/runtime"
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

var probeSupervisorEntry = func(stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "platform probe supervisor is unavailable")
	return 1
}

var listenTransport = transport.Listen
var prepareTokenFileForRun = prepareTokenFile
var consumeTokenFileForRun = consumeTokenFile

type explicitBool struct{ value bool }

func (b *explicitBool) String() string { return strconv.FormatBool(b.value) }

func (b *explicitBool) Set(value string) error {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	b.value = parsed
	return nil
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("unit-test-service", flag.ContinueOnError)
	flags.SetOutput(stderr)
	endpoint := flags.String("endpoint", "", "local IPC endpoint")
	tokenFile := flags.String("token-file", "", "authentication token file")
	dataDir := flags.String("data-dir", "", "owner-only service data directory")
	workspaceRoot := flags.String("workspace-root", "", "workspace root")
	var trustedWorkspace explicitBool
	flags.Var(&trustedWorkspace, "trusted-workspace", "allow workspace build execution (explicit true or false)")
	cmakeBundleRoot := flags.String("cmake-bundle-root", "", "verified CMake bundle root")
	devCMakeExecutable := flags.String("dev-cmake-executable", "", "development CMake executable")
	prepareTokenFilePath := flags.String("prepare-token-file", "", "create an empty owner-only authentication token file")
	processHost := flags.Bool("process-host", false, "run the internal process host")
	probeSupervisor := flags.Bool("probe-supervisor", false, "run the internal probe supervisor")
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
	probeSupervisorFlagProvided := false
	taskFixtureFlagProvided := false
	taskFixtureChildFlagProvided := false
	flags.Visit(func(parsedFlag *flag.Flag) {
		switch parsedFlag.Name {
		case "prepare-token-file":
			prepareModeFlagProvided = true
		case "endpoint", "token-file", "data-dir", "workspace-root", "trusted-workspace",
			"cmake-bundle-root", "dev-cmake-executable":
			serviceModeFlagProvided = true
		case "process-host":
			processHostFlagProvided = true
		case "probe-supervisor":
			probeSupervisorFlagProvided = true
		case "task-fixture":
			taskFixtureFlagProvided = true
		case "task-fixture-child":
			taskFixtureChildFlagProvided = true
		}
	})
	internalModeCount := 0
	for _, provided := range []bool{
		processHostFlagProvided,
		probeSupervisorFlagProvided,
		taskFixtureFlagProvided,
		taskFixtureChildFlagProvided,
	} {
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
		case probeSupervisorFlagProvided:
			if !*probeSupervisor {
				fmt.Fprintln(stderr, "probe supervisor mode must be enabled")
				return 2
			}
			return probeSupervisorEntry(stdin, stdout, stderr)
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
		if err := prepareTokenFileForRun(*prepareTokenFilePath); err != nil {
			fmt.Fprintln(stderr, "authentication token file preparation failed")
			return 1
		}
		return 0
	}
	if *endpoint == "" || *tokenFile == "" || *dataDir == "" || *workspaceRoot == "" {
		fmt.Fprintln(stderr, "--endpoint, --token-file, --data-dir, and --workspace-root are required")
		return 2
	}
	token, err := consumeTokenFileForRun(*tokenFile)
	if err != nil {
		fmt.Fprintln(stderr, "authentication token unavailable")
		return 1
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "service executable is unavailable")
		return 1
	}
	active, err := serviceruntime.Open(serviceruntime.Config{
		DataDir: *dataDir, ServiceExecutable: executable,
		WorkspaceRoot: *workspaceRoot, TrustedWorkspace: trustedWorkspace.value,
		CMakeBundleRoot: *cmakeBundleRoot, DevCMakeExecutable: *devCMakeExecutable,
		Platform: transport.PlatformName(),
		Clock:    task.RealClock{}, NewID: task.NewID, TerminationGrace: 2 * time.Second,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer active.Close()
	listener, err := listenTransport(*endpoint)
	if err != nil {
		fmt.Fprintln(stderr, "local transport unavailable")
		return 1
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	service := server.NewServiceWithCoverage(listener, token, transport.PlatformName(), transport.TransportName(), active, active.CoverageBackend(), server.ServiceConfig{MaxConnections: 64})
	go func() { <-ctx.Done(); service.Shutdown() }()
	fmt.Fprintf(stdout, "READY %s\n", *endpoint)
	if err := service.Serve(); err != nil {
		fmt.Fprintln(stderr, "service transport failed")
		return 1
	}
	if err := active.Close(); err != nil {
		fmt.Fprintln(stderr, "service runtime shutdown failed")
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
