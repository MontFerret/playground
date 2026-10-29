package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/MontFerret/playground/internal/config"
	"github.com/MontFerret/playground/internal/playground"
)

func TestStartupFailureReleasesHostAndPreservesCleanupError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	cfg.ListenAddr = listener.Addr().String()
	failure := errors.New("cleanup failure")
	closed := 0
	host := testHost{close: func(ctx context.Context) error {
		closed++
		if ctx.Err() != nil {
			t.Error("cleanup inherited serving cancellation")
		}

		if _, ok := ctx.Deadline(); !ok {
			t.Error("cleanup is not bounded")
		}

		return failure
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = serve(ctx, cfg, host, playground.Service{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "listen HTTP") || !errors.Is(err, failure) || closed != 1 {
		t.Fatalf("startup cleanup: err=%v closed=%d", err, closed)
	}
}

func TestCanceledServingStillRunsBoundedCleanup(t *testing.T) {
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.CleanupTimeout = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	host := testHost{close: func(cleanup context.Context) error {
		if cleanup.Err() != nil {
			t.Error("cleanup started with a canceled context")
		}

		<-cleanup.Done()

		return cleanup.Err()
	}}
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, cfg, host, playground.Service{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cleanup timeout not reported: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup exceeded its bound")
	}
}
