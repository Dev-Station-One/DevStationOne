// Command devstationone-runtime runs the DevStationOne Development Runtime agent.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"devstationone/internal/agent"
	"devstationone/internal/config"
	"devstationone/internal/logging"
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

	err = agent.Run(ctx, cfg, logger)

	logger.Info("runtime stopped")
	return err
}
