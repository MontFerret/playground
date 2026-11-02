// Package httpapi owns HTTP routing, protocol responses, and serving lifecycle.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/MontFerret/playground/internal/config"
	"github.com/MontFerret/playground/internal/playground"
)

type (
	// Handler serves only the operations implemented by this service.
	Handler struct {
		provider            toolingProvider
		service             playground.Service
		limits              playground.HTTPLimits
		logger              *slog.Logger
		toolingLimits       playground.ToolingLimits
		maxExecutionTimeout time.Duration
		maxOutputBytes      int
	}

	toolingProvider interface {
		Environment(context.Context) (playground.Runtime, error)
		Analyze(context.Context, playground.Source) (playground.AnalysisResult, error)
		Format(context.Context, playground.Source, playground.FormatOptions) (playground.FormatResult, error)
		Compile(context.Context, playground.Source, playground.CompileOptions) (playground.CompileResult, error)
		Execute(context.Context, playground.Source, playground.ExecuteOptions) (playground.ExecuteResult, error)
	}

	errorEnvelope struct {
		Error publicError `json:"error"`
	}

	publicError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	responseWriter struct {
		http.ResponseWriter
		committed bool
	}
)

// New borrows provider for the handler's lifetime. Metadata is copied; it must
// describe the same effective settings installed on the HTTP server.
func New(provider toolingProvider, service playground.Service, cfg config.Config, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}

	if service.Version != nil {
		version := *service.Version
		service.Version = &version
	}

	return &Handler{
		provider: provider, service: service, limits: httpLimits(cfg), logger: logger,
		maxExecutionTimeout: cfg.MaxExecutionTimeout, maxOutputBytes: cfg.MaxOutputBytes,
		toolingLimits: playground.ToolingLimits{MaxSourceBytes: cfg.MaxSourceBytes, MaxBytecodeBytes: cfg.MaxBytecodeBytes, MaxFormattedSourceBytes: cfg.MaxFormattedSourceBytes},
	}
}

// ServeHTTP routes exact paths and uses one public error envelope. HEAD is not
// supported, but its error response still obeys HTTP's body suppression rule.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	response := &responseWriter{ResponseWriter: w}
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}

		if recovered == http.ErrAbortHandler {
			panic(http.ErrAbortHandler)
		}

		h.logger.Error("HTTP handler panic", "code", "internal_error")
		if response.committed {
			// An error envelope cannot replace committed bytes. net/http aborts
			// the connection without logging this sentinel's panic or a stack.
			panic(http.ErrAbortHandler)
		}

		h.writeError(response, r, http.StatusInternalServerError, "internal_error", "The service could not complete the request.")
	}()

	method := http.MethodPost
	switch r.URL.Path {
	case "/api/v1/environment":
		method = http.MethodGet
	case "/api/v1/analyze", "/api/v1/format", "/api/v1/compile", "/api/v1/execute":
	default:
		h.writeError(response, r, http.StatusNotFound, "not_found", "The requested route does not exist.")

		return
	}

	if r.Method != method {
		response.Header().Set("Allow", method)
		h.writeError(response, r, http.StatusMethodNotAllowed, "method_not_allowed", "This route supports "+method+".")

		return
	}

	if method == http.MethodPost {
		h.serveTooling(response, r)

		return
	}

	description, err := h.provider.Environment(r.Context())
	if err != nil {
		h.writeOperationError(response, r, err)

		return
	}

	h.writeJSON(response, r, http.StatusOK, playground.Environment{
		APIVersion: "v1", Service: h.service, Runtime: description,
		Capabilities: playground.Capabilities{
			Environment: true, Analyze: true, Format: true, Compile: true, Execute: true,
			OptimizationLevels: playground.OptimizationLevels(), DefaultOptimization: playground.DefaultOptimization,
			InspectionArtifacts: playground.InspectionArtifacts(),
		},
		Limits: playground.Limits{
			HTTP: h.limits, Tooling: h.toolingLimits,
			Execution: playground.ExecutionLimits{
				MaxTimeoutMs:   float64(h.maxExecutionTimeout) / float64(time.Millisecond),
				MaxOutputBytes: h.maxOutputBytes,
			},
		},
	})
}

func (w *responseWriter) WriteHeader(status int) {
	if w.committed {
		return
	}

	w.committed = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(body []byte) (int, error) {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(body)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	h.writeJSON(w, r, status, errorEnvelope{Error: publicError{Code: code, Message: message}})
}

func (h *Handler) writeJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	h.writeBufferedJSON(w, r, status, value, true)
}

func (h *Handler) writeBufferedJSON(w http.ResponseWriter, r *http.Request, status int, value any, checkContext bool) {
	body, err := json.Marshal(value)
	if err != nil {
		h.logger.Error("HTTP response encoding failed", "code", "internal_error")
		status = http.StatusInternalServerError
		body = []byte(`{"error":{"code":"internal_error","message":"The service could not encode its response."}}`)
	}

	// This is a wire-response limit, including the newline. encoding/json has
	// already materialized the document; it is not an encoding allocation cap.
	if len(body) >= h.limits.MaxResponseBytes {
		status = http.StatusUnprocessableEntity
		body = []byte(`{"error":{"code":"response_too_large","message":"The generated response exceeds the configured limit."}}`)
	}

	if checkContext && status == http.StatusOK && r.Context().Err() != nil {
		h.writeOperationError(w, r, r.Context().Err())

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(append(body, '\n'))
	}
}
