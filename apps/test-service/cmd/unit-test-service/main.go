package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"unit-test-ide.local/test-service/internal/server"
	"unit-test-ide.local/test-service/internal/transport"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("unit-test-service", flag.ContinueOnError)
	flags.SetOutput(stderr)
	endpoint := flags.String("endpoint", "", "local IPC endpoint")
	tokenFile := flags.String("token-file", "", "authentication token file")
	prepareTokenFilePath := flags.String("prepare-token-file", "", "create an empty owner-only authentication token file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "positional arguments are not supported")
		return 2
	}
	if *prepareTokenFilePath != "" {
		if *endpoint != "" || *tokenFile != "" {
			fmt.Fprintln(stderr, "--prepare-token-file cannot be combined with --endpoint or --token-file")
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
	service := server.NewService(listener, token, transport.PlatformName(), transport.TransportName(), server.ServiceConfig{MaxConnections: 64})
	go func() { <-ctx.Done(); service.Shutdown() }()
	fmt.Fprintf(stdout, "READY %s\n", *endpoint)
	if err := service.Serve(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
