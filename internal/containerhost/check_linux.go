package containerhost

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/MontFerret/playground/internal/config"
)

// The root-only operator socket runs the same probe inside the real namespace.
// It accepts no commands, arguments, destinations, source, or parameters.
func serveChecks(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		file := os.NewFile(5, "checks")
		listener, err := net.FileListener(file)
		_ = file.Close()
		if err != nil {
			return
		}
		defer listener.Close()
		server := &http.Server{ReadHeaderTimeout: time.Second, MaxHeaderBytes: 1024, ErrorLog: log.New(io.Discard, "", 0)}
		server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/network" {
				w.WriteHeader(http.StatusNotFound)
				return
			}

			probe, cancel := context.WithTimeout(r.Context(), 6*time.Second)
			defer cancel()
			child, err := launch("network-check", browserUID, browserUID, nil, containerEnvironment(), false)
			if err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			select {
			case <-child.done:
				if child.err == nil {
					w.WriteHeader(http.StatusNoContent)
					return
				}
			case <-probe.Done():
				child.stop(probe)
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		stop := context.AfterFunc(ctx, func() { _ = server.Close() })
		defer stop()
		_ = server.Serve(listener)
	}()

	return done
}

func requestNetworkCheck(ctx context.Context) error {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", stateDir+"/check.sock")
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 7 * time.Second}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://operator/network", nil)
	response, err := client.Do(request)
	if err != nil {
		return errors.New("namespace_check_unavailable")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.New("namespace_check_failed")
	}

	return nil
}

func checkAPI(ctx context.Context) error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return errors.New("invalid_health_configuration")
	}
	_, port, _ := net.SplitHostPort(cfg.ListenAddr)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/api/v1/environment", nil)
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("api_unavailable")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("api_unavailable")
	}

	return nil
}
