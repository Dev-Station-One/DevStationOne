// Command devstation-runtime runs the DevStation Development Runtime agent.
//
// Slice 0 scope: process startup, configuration, logging, and graceful
// shutdown only. The outbound Control Server connection is implemented in
// Slice 2.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"devstation/internal/config"
	"devstation/internal/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	logger := logging.New(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("runtime starting", "server_url", cfg.ServerURL, "env", cfg.Environment)

	<-ctx.Done()

	logger.Info("runtime stopped")
	return nil
}
