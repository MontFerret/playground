package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MontFerret/playground/internal/config"
	"github.com/MontFerret/playground/internal/playground"
	"github.com/MontFerret/playground/internal/runtimehost"
)

func TestToolingHTTPOutcomes(t *testing.T) {
	handler, _ := toolingHandler(t)
	for _, test := range []struct {
		path, body, status string
		artifacts          bool
	}{
		{"analyze", `{"source":{"text":"RETURN 1"}}`, "ok", false},
		{"analyze", `{"source":{"text":"RETURN @value"}}`, "ok", false},
		{"analyze", `{"source":{"text":"RETURN MISSING_FUNCTION()"}}`, "ok", false},
		{"analyze", `{"source":{"text":""}}`, "invalid", false},
		{"analyze", `{"source":{"text":"RETURN missing"}}`, "invalid", false},
		{"compile", `{"source":{"text":"RETURN @value"},"include":["parameters","bytecode"]}`, "ok", true},
		{"compile", `{"source":{"text":"RETURN 1"}}`, "ok", true},
		{"compile", `{"source":{"text":"RETURN [1,"},"include":["bytecode"]}`, "invalid", false},
	} {
		response := toolingRequest(handler, test.path, test.body)
		var body map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != 200 || string(body["status"]) != `"`+test.status+`"` {
			t.Fatalf("%s: %d %s %v", test.path, response.Code, response.Body, err)
		}

		if _, ok := body["artifacts"]; ok != test.artifacts {
			t.Fatalf("wrong artifact presence: %s", response.Body)
		}

		if _, ok := body["timings"]; ok != (test.path == "compile") {
			t.Fatalf("wrong timing presence: %s", response.Body)
		}
	}
}

func TestToolingStrictRequests(t *testing.T) {
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	provider := testProvider{analyze: func(context.Context, playground.Source) (playground.AnalysisResult, error) {
		t.Fatal("invalid request reached runtime")

		return playground.AnalysisResult{}, nil
	}, compile: func(context.Context, playground.Source, playground.CompileOptions) (playground.CompileResult, error) {
		t.Fatal("invalid request reached runtime")

		return playground.CompileResult{}, nil
	}}
	handler := New(provider, playground.Service{}, cfg, nil)
	for _, body := range []string{
		``, `null`, `[]`, `1`, `{`, `{}`, `{"source":null}`, `{"source":{}}`,
		`{"source":{"text":null}}`, `{"source":{"text":1}}`, `{"source":{"Text":"RETURN 1"}}`,
		`{"source":{"text":"RETURN 1","name":null}}`, `{"source":{"text":"RETURN 1","hostFile":"secret"}}`,
		`{"source":{"text":"RETURN 1"}} {}`, `{"source":{"text":"RETURN 1"},"params":{}}`,
		`{"source":{"text":"RETURN 1"},"options":null}`, `{"source":{"text":"RETURN 1"},"options":{"optimization":null}}`,
		`{"source":{"text":"RETURN 1"},"options":{"optimization":""}}`, `{"source":{"text":"RETURN 1"},"options":{"optimization":"aggressive"}}`,
		`{"source":{"text":"RETURN 1"},"options":{"debug":true}}`, `{"source":{"text":"RETURN 1"},"include":null}`,
		`{"source":{"text":"RETURN 1"},"include":[null]}`, `{"source":{"text":"RETURN 1"},"include":["ast"]}`,
		`{"source":{"text":"RETURN 1"},"include":["bytecode","bytecode"]}`, `{"source":{"text":"RETURN 1"},"inspect":[]}`,
		`{"source":{"text":"RETURN 1","text":"RETURN 2"}}`, `{"source":{"text":"RETURN 1"},"Source":{"text":"RETURN 2"}}`,
	} {
		response := toolingRequest(handler, "compile", body)
		assertHTTPError(t, response, 400, "invalid_request")
	}

	for _, field := range []string{`"options":{}`, `"include":[]`} {
		response := toolingRequest(handler, "analyze", `{"source":{"text":"RETURN 1"},`+field+`}`)
		assertHTTPError(t, response, 400, "invalid_request")
	}
}

func TestToolingMediaTypes(t *testing.T) {
	handler, _ := toolingHandler(t)
	for _, media := range []string{"", "text/plain", "application/json; charset=latin-1", "application/json; other=value", "application/json; charset=utf-8; other=value", "application/json;" + "="} {
		request := httptest.NewRequest("POST", "/api/v1/analyze", strings.NewReader(`{"source":{"text":"RETURN 1"}}`))
		request.Header.Set("Content-Type", media)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		assertHTTPError(t, response, 415, "unsupported_media_type")
	}

	request := httptest.NewRequest("POST", "/api/v1/analyze", strings.NewReader(`{"source":{"text":"RETURN 1"}}`))
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("UTF-8 JSON rejected: %s", response.Body)
	}
}

func TestToolingRequestAndResponseLimits(t *testing.T) {
	handler, cfg := toolingHandler(t)
	body := `{"source":{"text":"RETURN 1"}}`
	handler.limits.MaxRequestBodyBytes = len(body)
	if response := toolingRequest(handler, "analyze", body); response.Code != 200 {
		t.Fatalf("exact body limit rejected: %d %s", response.Code, response.Body)
	}

	// A reader without a known length exercises the chunked/unknown-length path.
	request := httptest.NewRequest("POST", "/api/v1/analyze", io.NopCloser(strings.NewReader(body+" ")))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertHTTPError(t, response, 413, "request_too_large")
	handler.limits.MaxRequestBodyBytes = cfg.MaxRequestBodyBytes

	handler.toolingLimits.MaxSourceBytes = len("RETURN 1")
	if response := toolingRequest(handler, "analyze", body); response.Code != 200 {
		t.Fatalf("exact source limit rejected: %d %s", response.Code, response.Body)
	}

	assertHTTPError(t, toolingRequest(handler, "analyze", `{"source":{"text":"RETURN 1 "}}`), 413, "request_too_large")
	handler.toolingLimits.MaxSourceBytes = cfg.MaxSourceBytes
	handler.toolingLimits.MaxBytecodeBytes = 1
	assertHTTPError(t, toolingRequest(handler, "compile", `{"source":{"text":"RETURN 1"},"include":["bytecode"]}`), 422, "response_too_large")
	if response := toolingRequest(handler, "compile", body); response.Code != 200 {
		t.Fatalf("unrequested artifact limit applied: %d %s", response.Code, response.Body)
	}

	// Escaping can expand a small artifact. The complete encoded envelope,
	// including its newline, is checked independently from raw artifact size.
	fake := testProvider{compile: func(context.Context, playground.Source, playground.CompileOptions) (playground.CompileResult, error) {
		return playground.CompileResult{Artifacts: playground.Artifacts{Bytecode: &playground.TextArtifact{Format: "text", Text: strings.Repeat("<", 60)}}}, nil
	}}
	cfg.MaxResponseBytes = 256
	escaping := New(fake, playground.Service{}, cfg, nil)
	assertHTTPError(t, toolingRequest(escaping, "compile", body), 422, "response_too_large")
	cfg.MaxResponseBytes = 4096
	escaping = New(fake, playground.Service{}, cfg, nil)
	complete := toolingRequest(escaping, "compile", body)
	escaping.limits.MaxResponseBytes = complete.Body.Len()
	if response := toolingRequest(escaping, "compile", body); response.Code != 200 {
		t.Fatalf("exact response limit rejected: %s", response.Body)
	}

	escaping.limits.MaxResponseBytes--
	assertHTTPError(t, toolingRequest(escaping, "compile", body), 422, "response_too_large")
}

func TestToolingServiceFailuresAndContexts(t *testing.T) {
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{playground.ErrClosed, 503, "service_unavailable"},
		{context.Canceled, 408, "request_canceled"},
		{context.DeadlineExceeded, 408, "request_timeout"},
		{errors.New("private source and stack"), 500, "internal_error"},
		{errors.Join(playground.ErrInternal, context.Canceled), 500, "internal_error"},
	} {
		var logs bytes.Buffer
		provider := testProvider{analyze: func(context.Context, playground.Source) (playground.AnalysisResult, error) {
			return playground.AnalysisResult{}, test.err
		}}
		handler := New(provider, playground.Service{}, cfg, slog.New(slog.NewJSONHandler(&logs, nil)))
		response := toolingRequest(handler, "analyze", `{"source":{"text":"RETURN 1"}}`)
		assertHTTPError(t, response, test.status, test.code)
		if strings.Contains(logs.String()+response.Body.String(), "private source") {
			t.Fatal("private failure leaked")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	provider := testProvider{compile: func(got context.Context, src playground.Source, options playground.CompileOptions) (playground.CompileResult, error) {
		if got != ctx || src.Name != "../../display.fql" || options.MaxBytecodeBytes != cfg.MaxBytecodeBytes || options.Optimization != playground.DefaultOptimization {
			t.Error("context, display label, or operator options changed")
		}
		cancel()

		return playground.CompileResult{}, nil
	}}
	handler := New(provider, playground.Service{}, cfg, nil)
	request := httptest.NewRequest("POST", "/api/v1/compile", strings.NewReader(`{"source":{"text":"RETURN 1","name":"../../display.fql"}}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertHTTPError(t, response, 408, "request_canceled")

	// The already canceled context must now be rejected before provider access.
	handler.provider = testProvider{}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertHTTPError(t, response, 408, "request_canceled")
}

func TestConcurrentHTTPDiagnostics(t *testing.T) {
	handler, _ := toolingHandler(t)
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Go(func() {
			name := fmt.Sprintf("file-%d.fql", i)
			body, _ := json.Marshal(map[string]any{"source": map[string]string{"name": name, "text": "LET text = \"😀\"\r\nRETURN missing"}})
			analysis := toolingRequest(handler, "analyze", string(body))
			compiled := toolingRequest(handler, "compile", string(body))
			var a, c toolingResponse
			if json.Unmarshal(analysis.Body.Bytes(), &a) != nil || json.Unmarshal(compiled.Body.Bytes(), &c) != nil || a.Status != "invalid" || !reflect.DeepEqual(a.Diagnostics, c.Diagnostics) || len(a.Diagnostics) != 1 || a.Diagnostics[0].Location.Source != name {
				t.Errorf("diagnostics differ or leak: %s %s", analysis.Body, compiled.Body)

				return
			}

			want := diagnosticRange{Start: diagnosticPosition{Line: 1, Character: 7}, End: diagnosticPosition{Line: 1, Character: 14}}
			if a.Diagnostics[0].Location.Range == nil || *a.Diagnostics[0].Location.Range != want {
				t.Errorf("wrong native diagnostic range: %+v", a.Diagnostics[0])
			}
		})
	}
	wg.Wait()
}

func TestToolingDeadlineBeforeAdmission(t *testing.T) {
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	handler := New(testProvider{}, playground.Service{}, cfg, nil)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	request := httptest.NewRequest("POST", "/api/v1/analyze", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertHTTPError(t, response, 408, "request_timeout")
}

func toolingHandler(t *testing.T) (*Handler, config.Config) {
	t.Helper()
	host, err := runtimehost.New()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = host.Close(context.Background()) })
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })

	return New(host, playground.Service{Name: "ferret-playground"}, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))), cfg
}

func toolingRequest(handler *Handler, operation, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "/api/v1/"+operation, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func assertHTTPError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var value errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || response.Code != status || value.Error.Code != code || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("expected %d/%s: %d %s, %v", status, code, response.Code, response.Body, err)
	}
}

func TestToolingCancellationDuringResponseEncoding(t *testing.T) {
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	handler := New(testProvider{}, playground.Service{}, cfg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("POST", "/api/v1/compile", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.writeJSON(response, request, 200, cancelingJSON{cancel: cancel})
	assertHTTPError(t, response, 408, "request_canceled")
}
