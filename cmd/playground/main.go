package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/MontFerret/playground/internal/containerhost"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	var err error
	if len(os.Args) > 1 && os.Args[1] == "container" {
		mode := ""
		if len(os.Args) == 3 {
			mode = os.Args[2]
		} else if len(os.Args) > 3 {
			os.Exit(2)
		}

		err = containerhost.Run(ctx, mode)
		if err != nil {
			// Container roles return fixed codes, never raw child diagnostics.
			logger.Error("Container stopped", "code", err.Error())
		} else if mode == "probe" {
			logger.Info("Container isolation probe passed")
		}
	} else {
		err = run(ctx, logger)
	}
	stop()
	if err != nil {
		logger.Error("Service failed", "code", "service_failure")
		os.Exit(1)
	}
}
