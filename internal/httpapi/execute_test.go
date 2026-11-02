package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MontFerret/playground/internal/playground"
)

func TestExecuteNativeResponses(t *testing.T) {
	handler, _ := toolingHandler(t)
	for _, tc := range []struct{ query, params, output, code string }{
		{"return @order.id", `{"order":{"id":"A1"}}`, `"A1"`, ""},
		{"RETURN @value", `{"value":null}`, "null", ""},
		{"RETURN @value", `{"value":[null,{"n":9007199254740993},9223372036854775807,-9223372036854775808]}`, `[null,{"n":9007199254740993},9223372036854775807,-9223372036854775808]`, ""},
		{"RETURN @value", "{}", "", "runtime_error"},
		{"RETURN MISSING_FUNCTION()", "{}", "", "runtime_error"},
	} {
		body, err := json.Marshal(map[string]any{"source": map[string]string{"text": tc.query}, "params": json.RawMessage(tc.params)})
		if err != nil {
			t.Fatal(err)
		}

		response := toolingRequest(handler, "execute", string(body))
		var value executionResponse
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || response.Code != 200 || string(value.Output) != tc.output {
			t.Fatalf("%s: %d %s, %v", tc.query, response.Code, response.Body, err)
		}

		timings := value.Timings
		if timings.CompileMs == nil || timings.ExecuteMs == nil || timings.TotalMs < *timings.CompileMs+*timings.ExecuteMs {
			t.Fatalf("missing or inconsistent timings: %s", response.Body)
		}

		if tc.code == "" {
			if value.Status != "ok" || value.Diagnostics == nil || len(value.Diagnostics) != 0 {
				t.Fatalf("unexpected success: %s", response.Body)
			}
		} else if value.Status != "failed" || len(value.Diagnostics) != 1 || value.Diagnostics[0].Code != tc.code || value.Diagnostics[0].Phase != "execute" {
			t.Fatalf("unexpected failure: %s", response.Body)
		}
	}
}

func TestExecuteCompileDiagnostics(t *testing.T) {
	handler, _ := toolingHandler(t)
	for _, body := range []string{
		`{"source":{"name":"../label","text":"RETURN [1,"}}`,
		`{"source":{"name":"../label","text":"LET s = \"😀\"\r\nRETURN missing"}}`,
	} {
		var compiled toolingResponse
		var executed executionResponse
		compileResponse := toolingRequest(handler, "compile", body)
		executeResponse := toolingRequest(handler, "execute", body)
		if err := json.Unmarshal(compileResponse.Body.Bytes(), &compiled); err != nil {
			t.Fatal(err)
		}

		if err := json.Unmarshal(executeResponse.Body.Bytes(), &executed); err != nil {
			t.Fatal(err)
		}

		if executeResponse.Code != 200 || executed.Status != "invalid" || executed.Output != nil ||
			executed.Timings.ExecuteMs != nil || executed.Timings.CompileMs == nil {
			t.Fatalf("invalid outcome: %d %s", executeResponse.Code, executeResponse.Body)
		}

		if len(executed.Diagnostics) == 0 || !reflect.DeepEqual(compiled.Diagnostics, executed.Diagnostics) {
			t.Fatalf("diagnostics differ: %s %s", compileResponse.Body, executeResponse.Body)
		}
	}
}

func TestExecuteAdvertisedLimits(t *testing.T) {
	handler, cfg := toolingHandler(t)
	cfg.MaxOutputBytes = 4
	cfg.MaxExecutionTimeout = 1000*time.Millisecond + 250*time.Microsecond
	handler = New(handler.provider, playground.Service{}, cfg, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/environment", nil))
	var env playground.Environment
	if json.Unmarshal(rec.Body.Bytes(), &env) != nil || !env.Capabilities.Execute || env.Limits.Execution.MaxOutputBytes != 4 || env.Limits.Execution.MaxTimeoutMs != 1000.25 {
		t.Fatalf("execution limits: %s", rec.Body)
	}

	exact := toolingRequest(handler, "execute", `{"source":{"text":"RETURN null"}}`)
	over := toolingRequest(handler, "execute", `{"source":{"text":"RETURN false"}}`)
	var value executionResponse
	if exact.Code != 200 || !strings.Contains(exact.Body.String(), `"output":null`) || over.Code != 200 || json.Unmarshal(over.Body.Bytes(), &value) != nil {
		t.Fatalf("unexpected exact/over responses: %s / %s", exact.Body, over.Body)
	}

	if value.Status != "failed" || value.Output != nil || len(value.Diagnostics) != 1 || value.Diagnostics[0].Code != "output_too_large" {
		t.Fatalf("output limit: %s / %s", exact.Body, over.Body)
	}

	cfg.MaxResponseBytes = 256
	cfg.MaxOutputBytes = 512
	handler = New(handler.provider, playground.Service{}, cfg, nil)
	rec = toolingRequest(handler, "execute", `{"source":{"text":"RETURN @s"},"params":{"s":"`+strings.Repeat("x", 256)+`"}}`)
	assertHTTPError(t, rec, 422, "response_too_large")
}

func TestExecuteRejectsInvalidInputsBeforeRun(t *testing.T) {
	handler, cfg := toolingHandler(t)
	rec := toolingRequest(handler, "execute", `{"source":{"text":"RETURN UNKNOWN_FUNCTION()"},"params":{"x":1e999}}`)
	assertHTTPError(t, rec, 400, "invalid_request")

	handler = New(testProvider{}, playground.Service{}, cfg, nil)
	for _, extra := range []string{
		`"params":null`, `"params":[]`, `"params":{"x":1,"x":2}`,
		`"params":{"obj":[{"x":1,"\u0078":2}]}`, `"Params":{}`, `"include":[]`,
		`"options":{"optimization":"aggressive"}`, `"options":{"fsRoot":"."}`,
		`"options":{"timeoutMs":0}`, `"options":{"timeoutMs":-1}`, `"options":{"timeoutMs":0.5}`,
		`"options":{"timeoutMs":null}`, `"options":{"timeoutMs":"10"}`,
	} {
		assertHTTPError(t, toolingRequest(handler, "execute", `{"source":{"text":"RETURN 1"},`+extra+`}`), 400, "invalid_request")
	}

	handler.toolingLimits.MaxSourceBytes = 1
	assertHTTPError(t, toolingRequest(handler, "execute", `{"source":{"text":"RETURN 1"}}`), 413, "request_too_large")
	handler.limits.MaxRequestBodyBytes = 1
	assertHTTPError(t, toolingRequest(handler, "execute", `{"source":{"text":"x"}}`), 413, "request_too_large")
}

func TestExecuteEffectiveDeadline(t *testing.T) {
	_, cfg := toolingHandler(t)
	cfg.MaxExecutionTimeout = time.Minute
	for _, test := range []struct {
		name, options    string
		parent, expected time.Duration
	}{
		{"server cap", "", 2 * time.Minute, time.Minute},
		{"long client cap", `,"options":{"timeoutMs":9223372036854775807}`, 2 * time.Minute, time.Minute},
		{"short client", `,"options":{"timeoutMs":3000}`, 2 * time.Minute, 3 * time.Second},
		{"parent", `,"options":{"timeoutMs":3000}`, time.Second, time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			start := time.Now()
			ctx, cancel := context.WithDeadline(t.Context(), start.Add(test.parent))
			defer cancel()

			provider := testProvider{execute: func(ctx context.Context, _ playground.Source, options playground.ExecuteOptions) (playground.ExecuteResult, error) {
				deadline, ok := ctx.Deadline()
				if !ok || deadline.Before(start.Add(test.expected)) || deadline.After(time.Now().Add(test.expected)) || options.MaxOutputBytes != cfg.MaxOutputBytes {
					t.Errorf("unexpected deadline %v or limit %d", deadline, options.MaxOutputBytes)
				}

				return playground.ExecuteResult{Output: json.RawMessage("1")}, nil
			}}
			h := New(provider, playground.Service{}, cfg, nil)
			r := httptest.NewRequest(http.MethodPost, "/api/v1/execute", strings.NewReader(`{"source":{"text":"RETURN 1"}`+test.options+`}`)).WithContext(ctx)
			r.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != 200 {
				t.Fatal(rec.Body)
			}
		})
	}
}
