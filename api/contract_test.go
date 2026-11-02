package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/MontFerret/ferret/v2/pkg/engine"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/MontFerret/playground/internal/config"
	"github.com/MontFerret/playground/internal/httpapi"
	"github.com/MontFerret/playground/internal/playground"
	"github.com/MontFerret/playground/internal/runtimehost"
)

func TestOpenAPIDocumentAndExamples(t *testing.T) {
	doc := loadContract(t)
	if doc.OpenAPI != "3.0.3" || doc.Info.Version != "v1" || doc.Paths.Len() != 5 {
		t.Fatal("unexpected contract version or surface")
	}

	for name, schema := range doc.Components.Schemas {
		if schema.Value.Example != nil {
			if err := schema.Value.VisitJSON(schema.Value.Example); err != nil {
				t.Errorf("schema %s example: %v", name, err)
			}
		}
	}

	for path, item := range doc.Paths.Map() {
		for method, operation := range item.Operations() {
			want := "planned"
			if path == "/api/v1/environment" || path == "/api/v1/analyze" || path == "/api/v1/format" || path == "/api/v1/compile" {
				want = "implemented"
			}

			if operation.Extensions["x-implementation-status"] != want {
				t.Errorf("%s %s has wrong implementation status", method, path)
			}

			if operation.RequestBody != nil {
				validateExamples(t, path+" request", operation.RequestBody.Value.Content)
			}

			for status, response := range operation.Responses.Map() {
				validateExamples(t, path+" "+status, response.Value.Content)
			}
		}
	}

	want := []any{engine.OptimizationNone.String(), engine.OptimizationBasic.String(), engine.OptimizationFull.String()}
	if got := doc.Components.Schemas["Optimization"].Value.Enum; !reflect.DeepEqual(got, want) {
		t.Fatalf("optimization values drifted from native Ferret: %v, want %v", got, want)
	}
}

func TestOpenAPIImplementedResponses(t *testing.T) {
	doc := loadContract(t)
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}

	host, err := runtimehost.New()
	if err != nil {
		t.Fatal(err)
	}

	defer host.Close(context.Background())
	cfg, _ := config.Load(func(string) (string, bool) { return "", false })
	handler := httpapi.New(host, playground.Service{Name: "ferret-playground"}, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, test := range []struct {
		method, path, body string
		status             int
		canceled           bool
	}{
		{"GET", "/api/v1/environment", "", 200, false},
		{"GET", "/unknown", "", 404, false},
		{"POST", "/api/v1/execute", "", 404, false},
		{"POST", "/api/v1/environment", "", 405, false},
		{"GET", "/api/v1/environment", "", 408, true},
		{"POST", "/api/v1/format", `{"source":{"text":"RETURN 1"}}`, 200, false},
		{"POST", "/api/v1/format", `{"source":{"text":""}}`, 200, false},
		{"POST", "/api/v1/format", `{"source":{"name":"example.fql","text":"RETURN [1,"}}`, 200, false},
		{"POST", "/api/v1/format", `{"source":{"text":"RETURN 1"},"options":{}}`, 400, false},
		{"GET", "/api/v1/format", "", 405, false},
		{"POST", "/api/v1/format", `{"source":{"text":"RETURN 1"}}`, 408, true},
		{"POST", "/api/v1/analyze", `{"source":{"text":"RETURN 1"}}`, 200, false},
		{"POST", "/api/v1/analyze", `{"source":{"text":""}}`, 200, false},
		{"POST", "/api/v1/analyze", `{"source":{"text":"RETURN missing"}}`, 200, false},
		{"POST", "/api/v1/compile", `{"source":{"text":"RETURN @x"},"include":["bytecode","parameters"]}`, 200, false},
		{"POST", "/api/v1/compile", `{"source":{"text":"RETURN 1"}}`, 200, false},
		{"POST", "/api/v1/compile", `{"source":{"text":"RETURN 1"},"include":["parameters"]}`, 200, false},
		{"POST", "/api/v1/compile", `{"source":{"text":"RETURN [1,"},"include":["bytecode"]}`, 200, false},
		{"POST", "/api/v1/compile", `{"source":{"text":"RETURN 1"},"include":["ast"]}`, 400, false},
		{"POST", "/api/v1/compile", `{`, 400, false},
		{"POST", "/api/v1/compile", "", 415, false},
		{"POST", "/api/v1/compile", `{"source":{"text":"RETURN 1"}}`, 408, true},
	} {
		request := httptest.NewRequest(test.method, "http://127.0.0.1:8080"+test.path, bytes.NewBufferString(test.body))
		if test.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		if test.canceled {
			ctx, cancel := context.WithCancel(request.Context())
			cancel()
			request = request.WithContext(ctx)
		}

		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s %s: status %d, want %d", test.method, test.path, response.Code, test.status)
		}

		if test.status == 404 || test.status == 405 {
			var value any
			if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
				t.Fatal(err)
			}

			if err := doc.Components.Schemas["Error"].Value.VisitJSON(value); err != nil {
				t.Fatalf("implemented error does not match contract: %v", err)
			}

			continue
		}

		route, params, err := router.FindRoute(request)
		if err != nil {
			t.Fatal(err)
		}

		err = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
			RequestValidationInput: &openapi3filter.RequestValidationInput{Request: request, PathParams: params, Route: route},
			Status:                 response.Code, Header: response.Header(), Body: io.NopCloser(bytes.NewReader(response.Body.Bytes())),
			Options: &openapi3filter.Options{IncludeResponseStatus: true},
		})
		if err != nil {
			t.Fatalf("implemented response does not match contract: %v", err)
		}
	}
}

func TestOpenAPIOutputPresenceAndRequestOptions(t *testing.T) {
	doc := loadContract(t)
	for _, test := range []struct {
		schema, document string
		valid            bool
	}{
		{"ExecutionResponse", `{"outcome":"ok","diagnostics":[],"output":null,"timings":{"totalMs":0}}`, true},
		{"ExecutionResponse", `{"outcome":"ok","diagnostics":[],"output":{"nested":[null,1,true,"text"]},"timings":{"totalMs":0}}`, true},
		{"ExecutionResponse", `{"outcome":"ok","diagnostics":[],"timings":{"totalMs":0}}`, false},
		{"ExecutionResponse", `{"outcome":"failed","diagnostics":[],"timings":{"totalMs":0}}`, true},
		{"ExecutionResponse", `{"outcome":"failed","diagnostics":[],"output":null,"timings":{"totalMs":0}}`, false},
		{"CompileRequest", `{"source":{"text":"RETURN 1"},"options":{"optimization":"full"},"include":["parameters","bytecode"]}`, true},
		{"CompileRequest", `{"source":{"text":"RETURN 1"},"options":{"optimization":"aggressive"}}`, false},
		{"CompileRequest", `{"source":{"text":"RETURN 1"},"include":["ast"]}`, false},
		{"CompileRequest", `{"source":{"text":"RETURN 1","hostFile":"/etc/passwd"}}`, false},
		{"CompileRequest", `{"source":{}}`, false},
		{"ExecuteRequest", `{"source":{"text":"RETURN @x"},"params":{"x":null}}`, true},
		{"ExecuteRequest", `{"source":{"text":"RETURN 1"},"hostConfig":{}}`, false},
	} {
		var value any
		if err := json.Unmarshal([]byte(test.document), &value); err != nil {
			t.Fatal(err)
		}

		err := doc.Components.Schemas[test.schema].Value.VisitJSON(value)
		if (err == nil) != test.valid {
			t.Errorf("%s %s: valid=%v, err=%v", test.schema, test.document, test.valid, err)
		}
	}
}

func loadContract(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}

	return doc
}

func validateExamples(t *testing.T, label string, content openapi3.Content) {
	t.Helper()
	for _, media := range content {
		if media.Example != nil {
			if err := media.Schema.Value.VisitJSON(media.Example); err != nil {
				t.Errorf("%s example: %v", label, err)
			}
		}

		for name, example := range media.Examples {
			if err := media.Schema.Value.VisitJSON(example.Value.Value); err != nil {
				t.Errorf("%s example %s: %v", label, name, err)
			}
		}
	}
}

func TestOpenAPILimitResponses(t *testing.T) {
	doc := loadContract(t)
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}

	host, err := runtimehost.New()
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())

	for _, test := range []struct {
		name, operation string
		configure       func(*config.Config)
		body            string
		status          int
	}{
		{"body", "compile", func(c *config.Config) { c.MaxRequestBodyBytes = 1 }, `{"source":{"text":"RETURN 1"}}`, 413},
		{"source", "compile", func(c *config.Config) { c.MaxSourceBytes = 1 }, `{"source":{"text":"RETURN 1"}}`, 413},
		{"formatted source", "format", func(c *config.Config) { c.MaxFormattedSourceBytes = 7 }, `{"source":{"text":"RETURN 1"}}`, 422},
		{"artifact", "compile", func(c *config.Config) { c.MaxBytecodeBytes = 1 }, `{"source":{"text":"RETURN 1"},"include":["bytecode"]}`, 422},
		{"response", "compile", func(c *config.Config) { c.MaxResponseBytes = 256 }, `{"source":{"text":"RETURN [1,2,3,4,5,6,7,8,9]"},"include":["bytecode"]}`, 422},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, _ := config.Load(func(string) (string, bool) { return "", false })
			test.configure(&cfg)
			handler := httpapi.New(host, playground.Service{Name: "ferret-playground"}, cfg, nil)
			request := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/v1/"+test.operation, bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("wrong status: %d %s", response.Code, response.Body)
			}

			route, params, err := router.FindRoute(request)
			if err != nil {
				t.Fatal(err)
			}

			err = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
				RequestValidationInput: &openapi3filter.RequestValidationInput{Request: request, PathParams: params, Route: route},
				Status:                 response.Code, Header: response.Header(), Body: io.NopCloser(bytes.NewReader(response.Body.Bytes())),
				Options: &openapi3filter.Options{IncludeResponseStatus: true},
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
