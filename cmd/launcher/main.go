package main

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/providah-community/internal/launcher"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	launcher.StartCleanup(ctx)
	trust, err := launcher.ReadRuntimeTrust(os.Getenv("PROVIDER_TRUST_KEYS_FILE"), os.Getenv("PROVIDER_RELEASES_FILE"))
	if err != nil {
		return err
	}
	runtimes, err := launcher.LoadRuntimes(ctx, os.Getenv("PROVIDER_RUNTIMES"), os.Getenv("PROVIDER_IMAGE"), trust)
	if err != nil {
		return err
	}
	handler, err := launcher.NewRuntimes(runtimes)
	if err != nil {
		return err
	}
	if err = handler.ConfigureAutomation(ctx, os.Getenv("AUTOMATION_RUNTIMES"), os.Getenv("AUTOMATION_PROXY_IMAGE")); err != nil {
		return err
	}
	socket := os.Getenv("LAUNCHER_SOCKET")
	if socket == "" {
		return fmt.Errorf("LAUNCHER_SOCKET is required")
	}
	if err = os.MkdirAll(filepath.Dir(socket), 0755); err != nil { // #nosec G703 -- Socket path is trusted deployment configuration; existing non-sockets are rejected.
		return err
	}
	if info, err := os.Lstat(socket); err == nil { // #nosec G703 -- Socket path is trusted deployment configuration; existing non-sockets are rejected.
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refusing to replace non-socket")
		}
		if err = os.Remove(socket); err != nil { // #nosec G703 -- Socket path is trusted deployment configuration; existing non-sockets are rejected.
			return err
		}
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	if os.Geteuid() == 0 {
		if err = os.Chown(socket, 65532, 0); err != nil { // #nosec G703 -- Socket path is trusted deployment configuration; existing non-sockets are rejected.
			return err
		}
	}
	if err = os.Chmod(socket, 0660); err != nil { // #nosec G703 -- Socket path is trusted deployment configuration; existing non-sockets are rejected.
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 140 * time.Second, WriteTimeout: 140 * time.Second}
	go func() { <-ctx.Done(); _ = server.Close() }()
	fmt.Fprintln(os.Stderr, "Provider launcher ready (pinned image, isolated containers)")
	if err = server.Serve(listener); err != http.ErrServerClosed {
		return err
	}
	return nil
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "health" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := provider.LoadRuntimes(ctx, os.Getenv("LAUNCHER_SOCKET")); err != nil {
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
