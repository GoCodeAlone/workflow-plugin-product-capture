package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/GoCodeAlone/workflow-plugin-product-capture/internal/conformance"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(conformance.MainContext(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
