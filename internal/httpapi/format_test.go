package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MontFerret/playground/internal/config"
	"github.com/MontFerret/playground/internal/playground"
	"github.com/MontFerret/playground/internal/runtimehost"
)

func TestFormatHTTPOutcomes(t *testing.T) {
	handler, _ := toolingHandler(t)
	for _, test := range []struct{ text, status, formatted string }{
		{"RETURN 1", "ok", "return 1"},
		{"// 😀\r\nRETURN \"é\"", "ok", "// 😀\r\nreturn \"é\""},
		{"RETURN UNKNOWN(@value)", "ok", "return UNKNOWN(@value)"},
		{"RETURN missing", "ok", "return missing"},
		{"", "invalid", ""},
		{`RETURN "😀" +`, "invalid", ""},
	} {
		body, _ := json.Marshal(map[string]any{"source": map[string]string{"text": test.text}})
		response := toolingRequest(handler, "format", string(body))
		var result toolingResponse
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || result.Status != test.status || result.Artifacts != nil || result.Timings != nil {
			t.Fatalf("format HTTP: %d %s %v", response.Code, response.Body, err)
		}

		if test.status == "ok" {
			if result.FormattedSource == nil || *result.FormattedSource != test.formatted || len(result.Diagnostics) != 0 {
				t.Fatalf("formatted source: %s", response.Body)
			}
		} else if result.FormattedSource != nil || len(result.Diagnostics) == 0 || result.Diagnostics[0].Location.Source != "anonymous" {
			t.Fatalf("invalid result: %s", response.Body)
		}
	}
}

func TestFormatHTTPStrictRequests(t *testing.T) {
	handler, _ := toolingHandler(t)
	for _, body := range []string{
		`null`, `[]`, `{}`, `{"source":null}`, `{"source":{}}`, `{"source":{"text":null}}`,
		`{"source":{"Text":"RETURN 1"}}`, `{"source":{"text":"RETURN 1","text":"RETURN 2"}}`,
		`{"source":{"text":"RETURN 1"}} {}`, `{"source":{"text":"RETURN 1"},"options":{}}`,
		`{"source":{"text":"RETURN 1"},"options":{"singleQuote":true}}`,
		`{"source":{"text":"RETURN 1"},"include":[]}`, `{"source":{"text":"RETURN 1"},"params":{}}`,
	} {
		assertHTTPError(t, toolingRequest(handler, "format", body), 400, "invalid_request")
	}

	request := httptest.NewRequest("POST", "/api/v1/format", strings.NewReader(`{"source":{"text":"RETURN 1"}}`))
	request.Header.Set("Content-Type", "application/json; charset=latin-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertHTTPError(t, response, 415, "unsupported_media_type")
}

func TestFormatHTTPLimits(t *testing.T) {
	handler, cfg := toolingHandler(t)
	body := `{"source":{"text":"RETURN 1"}}`
	handler.limits.MaxRequestBodyBytes = len(body)
	handler.toolingLimits.MaxSourceBytes = len("RETURN 1")
	handler.toolingLimits.MaxFormattedSourceBytes = len("return 1")
	if response := toolingRequest(handler, "format", body); response.Code != 200 {
		t.Fatalf("exact limits rejected: %s", response.Body)
	}

	assertHTTPError(t, toolingRequest(handler, "format", body+" "), 413, "request_too_large")
	handler.limits.MaxRequestBodyBytes = cfg.MaxRequestBodyBytes
	assertHTTPError(t, toolingRequest(handler, "format", `{"source":{"text":"RETURN 1 "}}`), 413, "request_too_large")
	handler.toolingLimits.MaxSourceBytes = cfg.MaxSourceBytes
	handler.toolingLimits.MaxFormattedSourceBytes--
	assertHTTPError(t, toolingRequest(handler, "format", body), 422, "response_too_large")

	handler.toolingLimits.MaxFormattedSourceBytes = cfg.MaxFormattedSourceBytes
	handler.limits.MaxResponseBytes = 256
	escaping := `{"source":{"text":"RETURN '` + strings.Repeat("<", 60) + `'"}}`
	assertHTTPError(t, toolingRequest(handler, "format", escaping), 422, "response_too_large")
	handler.limits.MaxResponseBytes = cfg.MaxResponseBytes
	complete := toolingRequest(handler, "format", escaping)
	if complete.Code != 200 {
		t.Fatalf("formatted source unavailable: %s", complete.Body)
	}

	handler.limits.MaxResponseBytes = complete.Body.Len()
	if response := toolingRequest(handler, "format", escaping); response.Code != 200 {
		t.Fatalf("exact encoded response limit rejected: %s", response.Body)
	}

	handler.limits.MaxResponseBytes--
	assertHTTPError(t, toolingRequest(handler, "format", escaping), 422, "response_too_large")
}

func TestFormatHTTPFailuresAndContext(t *testing.T) {
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{playground.ErrClosed, 503, "service_unavailable"},
		{context.Canceled, 408, "request_canceled"},
		{context.DeadlineExceeded, 408, "request_timeout"},
		{playground.ErrResponseTooLarge, 422, "response_too_large"},
		{errors.New("private source and stack"), 500, "internal_error"},
		{errors.Join(playground.ErrInternal, context.Canceled), 500, "internal_error"},
	} {
		var logs bytes.Buffer
		provider := testProvider{format: func(context.Context, playground.Source, playground.FormatOptions) (playground.FormatResult, error) {
			return playground.FormatResult{FormattedSource: "private partial text"}, test.err
		}}
		handler := New(provider, playground.Service{}, cfg, slog.New(slog.NewJSONHandler(&logs, nil)))
		response := toolingRequest(handler, "format", `{"source":{"text":"RETURN 1"}}`)
		assertHTTPError(t, response, test.status, test.code)
		if strings.Contains(logs.String()+response.Body.String(), "private") {
			t.Fatal("internal error or partial output leaked")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := testProvider{format: func(got context.Context, src playground.Source, options playground.FormatOptions) (playground.FormatResult, error) {
		if got != ctx || src.Name != "../../display.fql" || options.MaxFormattedSourceBytes != cfg.MaxFormattedSourceBytes {
			t.Error("context, label or configured limit changed")
		}
		cancel()

		return playground.FormatResult{FormattedSource: "return 1"}, nil
	}}
	handler := New(provider, playground.Service{}, cfg, nil)
	request := httptest.NewRequest("POST", "/api/v1/format", strings.NewReader(`{"source":{"text":"RETURN 1","name":"../../display.fql"}}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertHTTPError(t, response, 408, "request_canceled")
	handler.provider = testProvider{}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertHTTPError(t, response, 408, "request_canceled")
}

func TestConcurrentFormatHTTPDiagnostics(t *testing.T) {
	handler, _ := toolingHandler(t)
	var wg sync.WaitGroup
	for i := range 18 {
		wg.Go(func() {
			name := fmt.Sprintf("file-%d.fql", i)
			for _, text := range []string{`RETURN "😀" +`, "// é😀\r\nRETURN \"中\" +", "RETURN [\"😀\"\r\n"} {
				body, _ := json.Marshal(map[string]any{"source": map[string]string{"name": name, "text": text}})
				analysis := toolingRequest(handler, "analyze", string(body))
				formatted := toolingRequest(handler, "format", string(body))
				var a, f toolingResponse
				if json.Unmarshal(analysis.Body.Bytes(), &a) != nil || json.Unmarshal(formatted.Body.Bytes(), &f) != nil || f.Status != "invalid" || f.FormattedSource != nil || !reflect.DeepEqual(a.Diagnostics, f.Diagnostics) || len(f.Diagnostics) == 0 || f.Diagnostics[0].Location.Source != name {
					t.Errorf("format diagnostic mismatch: %s %s", analysis.Body, formatted.Body)
				}
			}
		})
	}
	wg.Wait()
}

func TestFormatAdvertisedLimitIsEnforced(t *testing.T) {
	host, err := runtimehost.New()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = host.Close(context.Background()) })
	cfg, err := config.Load(func(key string) (string, bool) {
		return "8", key == "PLAYGROUND_MAX_FORMATTED_SOURCE_BYTES"
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := New(host, playground.Service{}, cfg, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/environment", nil))
	var environment playground.Environment
	if err := json.Unmarshal(response.Body.Bytes(), &environment); err != nil || response.Code != 200 || !environment.Capabilities.Format || environment.Limits.Tooling.MaxFormattedSourceBytes != 8 {
		t.Fatalf("format discovery: %d %s %v", response.Code, response.Body, err)
	}

	if response := toolingRequest(handler, "format", `{"source":{"text":"RETURN 1"}}`); response.Code != 200 {
		t.Fatalf("output at advertised limit rejected: %s", response.Body)
	}

	assertHTTPError(t, toolingRequest(handler, "format", `{"source":{"text":"RETURN 10"}}`), 422, "response_too_large")
}
