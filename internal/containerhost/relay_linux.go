package containerhost

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/MontFerret/playground/internal/egress"
)

func inheritedListener() (net.Listener, error) {
	file := os.NewFile(3, "listener")
	defer file.Close()

	return net.FileListener(file)
}

func runProxy(ctx context.Context) error {
	listener, err := inheritedListener()
	if err != nil {
		return errors.New("proxy_descriptor_failed")
	}
	defer listener.Close()
	server := &http.Server{Handler: egress.Handler(), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	stop := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stop()
	if err := server.Serve(listener); err != nil && ctx.Err() == nil {
		return errors.New("proxy_serving_failed")
	}

	return nil
}

func runRelay(ctx context.Context, network, target string) error {
	ctx, cancel := context.WithCancel(ctx)
	listener, err := inheritedListener()
	if err != nil {
		cancel()
		return errors.New("relay_descriptor_failed")
	}
	defer listener.Close()
	var connections sync.Map
	closeConnections := func() {
		_ = listener.Close()
		connections.Range(func(key, _ any) bool { _ = key.(net.Conn).Close(); return true })
	}
	stop := context.AfterFunc(ctx, closeConnections)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		closeConnections()
		stop()
		wg.Wait()
	}()
	for {
		client, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return errors.New("relay_accept_failed")
		}
		connections.Store(client, struct{}{})
		wg.Go(func() {
			defer client.Close()
			defer connections.Delete(client)
			upstream, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, target)
			if err != nil {
				return
			}
			defer upstream.Close()
			closeUpstream := context.AfterFunc(ctx, func() { _ = upstream.Close() })
			defer closeUpstream()
			done := make(chan struct{})
			go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close(); close(done) }()
			_, _ = io.Copy(client, upstream)
			_ = client.Close()
			<-done
		})
	}
}
