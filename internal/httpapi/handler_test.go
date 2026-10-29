package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MontFerret/playground/internal/config"
	"github.com/MontFerret/playground/internal/playground"
	"github.com/MontFerret/playground/internal/runtimehost"
)

func TestEnvironmentAndConcurrentRequests(t *testing.T) {
	host, err := runtimehost.New()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = host.Close(context.Background()) })
	cfg, err := config.Load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}

	version := "service-test"
	handler := New(host, playground.Service{Name: "ferret-playground", Version: &version}, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	version = "mutated"
	server := httptest.NewServer(handler)
	defer server.Close()

	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			response, err := server.Client().Get(server.URL + "/api/v1/environment")
			if err != nil {
				t.Error(err)

				return
			}

			defer response.Body.Close()
			var body playground.Environment
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Error(err)

				return
			}

			want := playground.Capabilities{Environment: true, OptimizationLevels: []string{}, InspectionArtifacts: []string{}}
			if response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/json" || body.APIVersion != "v1" || body.Runtime.Name != "ferret" || body.Runtime.StandardLibrary != "safe" || body.Runtime.Modules == nil || len(body.Runtime.Modules) != 0 || !reflect.DeepEqual(body.Capabilities, want) || body.Service.Version == nil || *body.Service.Version != "service-test" || body.Limits.HTTP != httpLimits(cfg) {
				t.Errorf("incorrect environment response: status=%d body=%+v", response.StatusCode, body)
			}
		})
	}
	wg.Wait()
}

func TestRoutesAndPublicErrors(t *testing.T) {
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	provider := testProvider{call: func(context.Context) (playground.Runtime, error) {
		t.Error("unknown paths and unsupported methods must not call the host")

		return playground.Runtime{}, nil
	}}
	handler := New(provider, playground.Service{}, cfg, nil)
	for _, test := range []struct {
		method, path, code string
		status             int
	}{
		{"GET", "/", "not_found", 404},
		{"GET", "/api/v1/environment/", "not_found", 404},
		{"GET", "/api/v2/environment", "not_found", 404},
		{"POST", "/api/v1/analyze", "not_found", 404},
		{"POST", "/api/v1/format", "not_found", 404},
		{"POST", "/api/v1/compile", "not_found", 404},
		{"POST", "/api/v1/execute", "not_found", 404},
		{"POST", "/api/v1/environment", "method_not_allowed", 405},
		{"OPTIONS", "/api/v1/environment", "method_not_allowed", 405},
		{"HEAD", "/api/v1/environment", "method_not_allowed", 405},
		{"HEAD", "/unknown", "not_found", 404},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, strings.NewReader("private source")))
			if response.Code != test.status || response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("status/headers = %d, %v", response.Code, response.Header())
			}

			if test.status == 405 && response.Header().Get("Allow") != "GET" {
				t.Fatal("missing Allow header")
			}

			if test.method == "HEAD" {
				if response.Body.Len() != 0 {
					t.Fatal("HEAD response has a body")
				}

				return
			}

			var body errorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error.Code != test.code || body.Error.Message == "" || strings.Contains(response.Body.String(), "private source") {
				t.Fatalf("public error = %s, %v", response.Body, err)
			}
		})
	}
}

func TestProviderErrorsAndPanicsAreSanitized(t *testing.T) {
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	for _, panicInstead := range []bool{false, true} {
		var logs bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logs, nil))
		provider := testProvider{call: func(context.Context) (playground.Runtime, error) {
			if panicInstead {
				panic("private source, credentials, and stack")
			}

			return playground.Runtime{}, errors.New("private source, credentials, and stack")
		}}
		response := httptest.NewRecorder()
		New(provider, playground.Service{}, cfg, logger).ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/environment", nil))
		var body errorEnvelope
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != 500 || body.Error.Code != "internal_error" {
			t.Fatalf("internal failure response: %d %s, %v", response.Code, response.Body, err)
		}

		if strings.Contains(response.Body.String()+logs.String(), "private source") || !strings.Contains(logs.String(), "internal_error") {
			t.Fatalf("unsafe operational output: %s %s", response.Body, &logs)
		}
	}
}

func TestRequestContextIsPropagated(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	provider := testProvider{call: func(got context.Context) (playground.Runtime, error) {
		called = true
		if got != ctx || !errors.Is(got.Err(), context.Canceled) {
			t.Error("request context was replaced or cancellation lost")
		}

		return playground.Runtime{}, got.Err()
	}}
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	New(provider, playground.Service{}, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/environment", nil).WithContext(ctx))
	if !called {
		t.Fatal("provider was not called")
	}
}

func TestCommittedResponsePanicAbortsWithoutLeakingDetails(t *testing.T) {
	var logs bytes.Buffer
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	host := testProvider{call: func(context.Context) (playground.Runtime, error) {
		return playground.Runtime{Name: "ferret", StandardLibrary: "safe", Modules: []playground.Module{}}, nil
	}}
	handler := New(host, playground.Service{Name: "ferret-playground"}, cfg, slog.New(slog.NewTextHandler(&logs, nil)))
	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Errorf("committed panic did not request connection abort: %v", recovered)
		}

		if strings.Contains(logs.String(), "private writer failure") {
			t.Error("writer panic leaked into logs")
		}
	}()
	handler.ServeHTTP(panickingWriter{ResponseWriter: httptest.NewRecorder()}, httptest.NewRequest("GET", "/api/v1/environment", nil))
}
