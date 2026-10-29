package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"

	"github.com/MontFerret/playground/internal/config"
)

type (
	// Server owns HTTP connections and drains them before its owner closes the host.
	Server struct {
		http   *http.Server
		config config.Config
	}

	serverLog struct {
		logger *slog.Logger
	}
)

// NewServer installs the same settings reported by environment discovery. It
// borrows handler and can be served once. Raw net/http log text is excluded.
func NewServer(cfg config.Config, handler http.Handler, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	return &Server{
		config: cfg,
		http: &http.Server{
			Handler: handler, ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout,
			IdleTimeout: cfg.IdleTimeout, MaxHeaderBytes: cfg.MaxHeaderBytes,
			ErrorLog: log.New(serverLog{logger: logger}, "", 0),
		},
	}
}

// Serve takes ownership of listener. Cancellation requests graceful shutdown;
// expiry forces connections closed and returns the drain error. Its shutdown
// context is independent of the canceled serving context.
func (s *Server) Serve(ctx context.Context, listener net.Listener) (resultErr error) {
	defer listener.Close()
	defer func() {
		resultErr = errors.Join(resultErr, s.http.Close())
	}()

	if err := ctx.Err(); err != nil {
		return nil
	}

	served := make(chan error, 1)
	go func() {
		served <- s.http.Serve(listener)
	}()

	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
		defer cancel()

		if err := s.http.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("drain HTTP: %w", err)
		}

		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			return err
		}

		return nil
	}
}

func (w serverLog) Write(body []byte) (int, error) {
	w.logger.Error("HTTP server event", "code", "http_server_error")

	return len(body), nil
}
