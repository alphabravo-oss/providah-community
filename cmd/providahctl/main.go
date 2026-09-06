package main

import (
	"context"
	"github.com/alphabravo-oss/providah-community/internal/consolecli"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(consolecli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
