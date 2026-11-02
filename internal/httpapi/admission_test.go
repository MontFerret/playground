package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MontFerret/playground/internal/config"
	"github.com/MontFerret/playground/internal/playground"
)

func TestAdmissionPrecedesReadAndSpansCleanup(t *testing.T) {
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	cfg.MaxConcurrentExecutions = 1
	entered, settle := make(chan struct{}), make(chan struct{})
	provider := testProvider{execute: func(context.Context, playground.Source, playground.ExecuteOptions) (playground.ExecuteResult, error) {
		close(entered)
		<-settle
		return playground.ExecuteResult{Output: json.RawMessage("1")}, nil
	}}
	h := New(provider, playground.Service{}, cfg, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := httptest.NewRequest("POST", "/api/v1/execute", strings.NewReader(`{"source":{"text":"RETURN 1"}}`))
		r.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(httptest.NewRecorder(), r)
	}()
	<-entered
	defer func() { close(settle); <-done }()
	req := httptest.NewRequest("POST", "/api/v1/execute", nil)
	req.Body = unreadable{}
	req.Header.Set("Origin", cfg.AllowedOrigins[0])
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assertHTTPError(t, w, http.StatusTooManyRequests, "execution_capacity_exceeded")
	if w.Header().Get("Access-Control-Allow-Origin") != cfg.AllowedOrigins[0] {
		t.Fatal("missing CORS on rejection")
	}
	h.BeginDrain()
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assertHTTPError(t, w, http.StatusServiceUnavailable, "service_unavailable")
}

func TestCORSPreflight(t *testing.T) {
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	h := New(testProvider{}, playground.Service{}, cfg, nil)
	for _, tc := range []struct {
		route, origin, method, headers string
		status                         int
	}{
		{"execute", "https://ferretlang.org", "POST", "content-type", 204},
		{"environment", "https://ferretlang.org", "GET", "", 204},
		{"execute", "https://ferretlang.org.evil.test", "POST", "Content-Type", 403},
		{"execute", "null", "POST", "Content-Type", 403},
		{"compile", "https://ferretlang.org", "GET", "", 403},
		{"execute", "https://ferretlang.org", "POST", "Content-Type, Authorization", 403},
	} {
		r := httptest.NewRequest("OPTIONS", "/api/v1/"+tc.route, nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Access-Control-Request-Method", tc.method)
		if tc.headers != "" {
			r.Header.Set("Access-Control-Request-Headers", tc.headers)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || tc.origin != cfg.AllowedOrigins[0] && w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("preflight %+v: %d", tc, w.Code)
		}
	}
}
