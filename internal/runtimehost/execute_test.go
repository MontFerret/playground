package runtimehost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MontFerret/ferret/v2/pkg/module"
	"github.com/MontFerret/ferret/v2/pkg/runtime"

	"github.com/MontFerret/playground/internal/config"
	"github.com/MontFerret/playground/internal/httpapi"
	"github.com/MontFerret/playground/internal/playground"
)

func TestConcurrentExecutionsCompileAndRunOnce(t *testing.T) {
	var compiles, runs, sessionCloses, planCloses atomic.Int32
	entered, release := make(chan struct{}, 2), make(chan struct{})
	host := toolingTestHost(t, func(boot module.Bootstrap) error {
		boot.Hooks().Plan().BeforeCompile(func(context.Context) error { compiles.Add(1); return nil })
		boot.Hooks().Plan().OnClose(func() error { planCloses.Add(1); return nil })
		boot.Hooks().Session().OnClose(func() error { sessionCloses.Add(1); return nil })
		boot.Host().Library().Function().A1().Add("OBSERVE", func(ctx context.Context, value runtime.Value) (runtime.Value, error) {
			runs.Add(1)
			entered <- struct{}{}
			select {
			case <-release:
				return value, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})

		return nil
	})
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	handler := httpapi.New(host, playground.Service{}, cfg, nil)
	replies := make(chan string, 2)
	for _, name := range []string{"first", "second"} {
		go func() {
			request := httptest.NewRequest("POST", "/api/v1/execute", strings.NewReader(`{"source":{"text":"RETURN OBSERVE(@value)"},"params":{"value":"`+name+`"}}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var value struct{ Output string }
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &value) != nil || value.Output != name {
				t.Errorf("request %s: %s", name, response.Body)
			}

			replies <- name
		}()
	}

	for range 2 {
		select {
		case <-entered:
		case reply := <-replies:
			t.Fatalf("%s returned before both runs were admitted", reply)
		}
	}
	close(release)
	<-replies
	<-replies
	if compiles.Load() != 2 || runs.Load() != 2 || sessionCloses.Load() != 2 || planCloses.Load() != 2 {
		t.Fatalf("compile/run/session close/plan close = %d/%d/%d/%d", compiles.Load(), runs.Load(), sessionCloses.Load(), planCloses.Load())
	}
}

func TestDisconnectAndDeadlineSettleNativeWork(t *testing.T) {
	for _, mode := range []string{"disconnect", "parent deadline"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			observed := make(chan error, 1)
			closed := make(chan string, 4)
			host := toolingTestHost(t, func(boot module.Bootstrap) error {
				boot.Host().Library().Function().A0().Add("BLOCK", func(ctx context.Context) (runtime.Value, error) {
					close(entered)
					<-ctx.Done()
					observed <- ctx.Err()

					return nil, ctx.Err()
				})
				boot.Hooks().Session().OnClose(func() error { closed <- "session"; return nil })
				boot.Hooks().Plan().OnClose(func() error { closed <- "plan"; return nil })

				return nil
			})
			cfg, _ := config.Load(func(string) (string, bool) { return "", false })
			handler := httpapi.New(host, playground.Service{}, cfg, nil)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "parent deadline" {
					ctx, cancel := context.WithTimeout(r.Context(), 300*time.Millisecond)
					defer cancel()
					r = r.WithContext(ctx)
				}

				handler.ServeHTTP(w, r)
			}))
			defer server.Close()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/v1/execute", strings.NewReader(`{"source":{"text":"RETURN BLOCK()"}}`))
			if err != nil {
				t.Fatal(err)
			}

			req.Header.Set("Content-Type", "application/json")
			done := make(chan error, 1)
			go func() {
				res, err := server.Client().Do(req)
				if err == nil {
					defer res.Body.Close()
					body, readErr := io.ReadAll(res.Body)
					err = readErr
					if mode == "parent deadline" && (res.StatusCode != 200 || !strings.Contains(string(body), `"code":"execution_timeout"`) || strings.Contains(string(body), `"output":`)) {
						t.Errorf("parent deadline: %d %s", res.StatusCode, body)
					}
				}

				done <- err
			}()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("request ended before function entry: %v", err)
			}

			want := context.DeadlineExceeded
			if mode == "disconnect" {
				cancel()
				want = context.Canceled
			}

			if err := <-observed; err != want {
				t.Fatalf("native work observed %v, want %v", err, want)
			}

			if err := <-done; (mode == "disconnect" && !errors.Is(err, context.Canceled)) || (mode == "parent deadline" && err != nil) {
				t.Fatalf("client outcome: %v", err)
			}

			if <-closed != "session" || <-closed != "plan" {
				t.Fatal("plan closed ahead of its session")
			}

			result, err := host.Execute(t.Context(), playground.Source{Text: "RETURN 42"}, playground.ExecuteOptions{MaxOutputBytes: 2})
			if err != nil || string(result.Output) != "42" {
				t.Fatalf("host reuse: %s, %v", result.Output, err)
			}
		})
	}
}

func TestExecutionCleanupPreservesPrimaryFailure(t *testing.T) {
	sessionErr, planErr := errors.New("private session close error"), errors.New("private plan close error")
	host := toolingTestHost(t, func(boot module.Bootstrap) error {
		boot.Hooks().Session().OnClose(func() error { return sessionErr })
		boot.Hooks().Plan().OnClose(func() error { return planErr })

		return nil
	})

	for _, tc := range []struct {
		source  string
		failure playground.ExecutionFailure
	}{
		{"RETURN 1", ""},
		{"RETURN 100", playground.OutputTooLarge},
		{"RETURN MISSING_FUNCTION()", playground.RuntimeError},
	} {
		result, err := host.Execute(t.Context(), playground.Source{Text: tc.source}, playground.ExecuteOptions{MaxOutputBytes: 1})
		if !errors.Is(err, sessionErr) || !errors.Is(err, planErr) || result.Output != nil || result.Failure != tc.failure || (tc.failure == "" && !errors.Is(err, playground.ErrInternal)) {
			t.Fatalf("cleanup outcome for %s: %+v %v", tc.source, result, err)
		}
	}
}

func TestShutdownWaitsForExecutionCleanup(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var planClosed, engineClosed atomic.Bool
	host := toolingTestHost(t, func(boot module.Bootstrap) error {
		boot.Hooks().Session().OnClose(func() error {
			close(entered)
			<-release

			return nil
		})
		boot.Hooks().Plan().OnClose(func() error { planClosed.Store(true); return nil })
		boot.Hooks().Engine().OnClose(func() error { engineClosed.Store(true); return nil })

		return nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := host.Execute(t.Context(), playground.Source{Text: "RETURN 1"}, playground.ExecuteOptions{MaxOutputBytes: 1})
		done <- err
	}()
	<-entered
	wait, cancel := context.WithCancel(t.Context())
	cancel()
	if err := host.Close(wait); !errors.Is(err, context.Canceled) || planClosed.Load() || engineClosed.Load() {
		t.Error("host closed before the admitted session settled")
	}

	if _, err := host.Execute(t.Context(), playground.Source{Text: "RETURN 2"}, playground.ExecuteOptions{MaxOutputBytes: 1}); !errors.Is(err, playground.ErrClosed) {
		t.Error("closing host admitted another execution")
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if err := host.Close(t.Context()); err != nil || !planClosed.Load() || !engineClosed.Load() {
		t.Fatalf("shutdown failed to settle descendants: %v", err)
	}
}
