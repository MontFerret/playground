package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/MontFerret/playground/internal/config"
)

func TestInstalledServerSettings(t *testing.T) {
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	cfg.ReadHeaderTimeout = time.Nanosecond
	server := NewServer(cfg, http.NotFoundHandler(), nil)
	limits := httpLimits(cfg)
	if server.http.MaxHeaderBytes != limits.MaxHeaderBytes || float64(server.http.ReadHeaderTimeout)/float64(time.Millisecond) != limits.ReadHeaderTimeoutMs || float64(server.http.ReadTimeout)/float64(time.Millisecond) != limits.ReadTimeoutMs || float64(server.http.WriteTimeout)/float64(time.Millisecond) != limits.WriteTimeoutMs || float64(server.http.IdleTimeout)/float64(time.Millisecond) != limits.IdleTimeoutMs || limits.ReadHeaderTimeoutMs <= 0 {
		t.Fatal("discovery limits differ from installed server settings")
	}
}

func TestServerDrainsAndForcesShutdown(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "graceful", true: "forced"}[force], func(t *testing.T) {
			cfg, _ := config.Load(func(string) (string, bool) { return "", false })
			if force {
				cfg.ShutdownTimeout = 20 * time.Millisecond
			}

			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(finished)
				close(entered)
				if force {
					<-r.Context().Done()

					return
				}

				<-release
				_, _ = io.WriteString(w, "drained")
			})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := NewServer(cfg, handler, slog.New(slog.NewTextHandler(io.Discard, nil)))
			served := make(chan error, 1)
			go func() { served <- server.Serve(ctx, listener) }()

			requestDone := make(chan error, 1)
			client := &http.Client{Timeout: 2 * time.Second}
			defer client.CloseIdleConnections()
			go func() {
				response, err := client.Get("http://" + listener.Addr().String())
				if err == nil {
					body, readErr := io.ReadAll(response.Body)
					response.Body.Close()
					if !force && (readErr != nil || string(body) != "drained") {
						err = errors.New("active response was not drained")
					}
				}

				requestDone <- err
			}()

			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("server did not accept local request")
			}

			cancel()
			if !force {
				close(release)
			}

			select {
			case err := <-served:
				if force && !errors.Is(err, context.DeadlineExceeded) || !force && err != nil {
					t.Fatalf("shutdown result: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown exceeded its bound")
			}

			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("handler did not settle")
			}

			if err := <-requestDone; !force && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServeFailureAndAlreadyCanceledContext(t *testing.T) {
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	for _, canceled := range []bool{false, true} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		if canceled {
			cancel()
		} else {
			_ = listener.Close()
		}

		err = NewServer(cfg, http.NotFoundHandler(), nil).Serve(ctx, listener)
		cancel()
		if canceled && err != nil || !canceled && err == nil {
			t.Fatalf("canceled=%v serve result=%v", canceled, err)
		}

		if connection, err := net.DialTimeout("tcp", listener.Addr().String(), 50*time.Millisecond); err == nil {
			connection.Close()
			t.Fatal("listener still accepting after Serve returned")
		}
	}
}
