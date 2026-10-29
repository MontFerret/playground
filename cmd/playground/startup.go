package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/MontFerret/playground/internal/config"
	"github.com/MontFerret/playground/internal/httpapi"
	"github.com/MontFerret/playground/internal/playground"
	"github.com/MontFerret/playground/internal/runtimehost"
)

type serviceHost interface {
	Environment(context.Context) (playground.Runtime, error)
	Close(context.Context) error
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}

	host, err := runtimehost.New()
	if err != nil {
		return fmt.Errorf("initialize runtime: %w", err)
	}

	return serve(ctx, cfg, host, serviceIdentity(), logger)
}

// serve takes ownership of host, including on listener startup failure.
func serve(ctx context.Context, cfg config.Config, host serviceHost, identity playground.Service, logger *slog.Logger) (resultErr error) {
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cfg.CleanupTimeout)
		defer cancel()

		if err := host.Close(cleanupCtx); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close runtime: %w", err))
		}
	}()

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}

	handler := httpapi.New(host, identity, cfg, logger)
	server := httpapi.NewServer(cfg, handler, logger)
	logger.Info("Service listening", "address", listener.Addr().String())
	err = server.Serve(ctx, listener)
	logger.Info("HTTP serving stopped")

	return err
}
