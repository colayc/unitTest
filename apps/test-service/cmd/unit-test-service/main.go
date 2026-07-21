package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"unit-test-ide.local/test-service/internal/server"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/transport"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("unit-test-service", flag.ContinueOnError)
	flags.SetOutput(stderr)
	endpoint := flags.String("endpoint", "", "local IPC endpoint")
	tokenFile := flags.String("token-file", "", "authentication token file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *endpoint == "" || *tokenFile == "" {
		fmt.Fprintln(stderr, "--endpoint and --token-file are required")
		return 2
	}
	rawToken, err := os.ReadFile(*tokenFile)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	token := strings.TrimSpace(string(rawToken))
	if len(token) < 16 {
		fmt.Fprintln(stderr, "authentication token must contain at least 16 characters")
		return 1
	}
	if err := os.Remove(*tokenFile); err != nil {
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
	shutdown := make(chan struct{})
	var shutdownOnce sync.Once
	go func() {
		select {
		case <-ctx.Done():
		case <-shutdown:
		}
		_ = listener.Close()
	}()
	fmt.Fprintf(stdout, "READY %s\n", *endpoint)

	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			select {
			case <-ctx.Done():
				return 0
			case <-shutdown:
				return 0
			default:
				fmt.Fprintln(stderr, acceptErr)
				return 1
			}
		}
		active := session.New(token, transport.PlatformName(), transport.TransportName())
		go func() {
			server.ServeConnection(connection, active)
			select {
			case <-active.ShutdownRequested():
				shutdownOnce.Do(func() { close(shutdown) })
			default:
			}
		}()
	}
}
