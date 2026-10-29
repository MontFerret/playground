// Package httpapi owns HTTP routing, protocol responses, and serving lifecycle.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/MontFerret/playground/internal/config"
	"github.com/MontFerret/playground/internal/playground"
)

type (
	// Handler serves only the operations implemented by this service.
	Handler struct {
		provider environmentProvider
		service  playground.Service
		limits   playground.HTTPLimits
		logger   *slog.Logger
	}

	environmentProvider interface {
		Environment(context.Context) (playground.Runtime, error)
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
func New(provider environmentProvider, service playground.Service, cfg config.Config, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}

	if service.Version != nil {
		version := *service.Version
		service.Version = &version
	}

	return &Handler{provider: provider, service: service, limits: httpLimits(cfg), logger: logger}
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

	if r.URL.Path != "/api/v1/environment" {
		h.writeError(response, r, http.StatusNotFound, "not_found", "The requested route does not exist.")

		return
	}

	if r.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		h.writeError(response, r, http.StatusMethodNotAllowed, "method_not_allowed", "This route supports GET.")

		return
	}

	description, err := h.provider.Environment(r.Context())
	if err != nil {
		h.logger.Error("Environment discovery failed", "code", "internal_error")
		h.writeError(response, r, http.StatusInternalServerError, "internal_error", "The service could not describe its environment.")

		return
	}

	h.writeJSON(response, r, http.StatusOK, playground.Environment{
		APIVersion: "v1", Service: h.service, Runtime: description,
		Capabilities: playground.Capabilities{
			Environment: true, OptimizationLevels: []string{}, InspectionArtifacts: []string{},
		},
		Limits: playground.Limits{HTTP: h.limits},
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
	body, err := json.Marshal(value)
	if err != nil {
		h.logger.Error("HTTP response encoding failed", "code", "internal_error")
		status = http.StatusInternalServerError
		body = []byte(`{"error":{"code":"internal_error","message":"The service could not encode its response."}}`)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(append(body, '\n'))
	}
}
