package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/MontFerret/playground/internal/playground"
)

type (
	toolingResponse struct {
		Status          string                `json:"status"`
		Diagnostics     []httpDiagnostic      `json:"diagnostics"`
		Artifacts       *playground.Artifacts `json:"artifacts,omitempty"`
		Timings         *compileTimings       `json:"timings,omitempty"`
		FormattedSource *string               `json:"formattedSource,omitempty"`
	}

	compileTimings struct {
		CompileMs float64 `json:"compileMs"`
	}
)

func (h *Handler) serveTooling(w http.ResponseWriter, r *http.Request) {
	if err := r.Context().Err(); err != nil {
		h.writeOperationError(w, r, err)

		return
	}

	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || len(r.Header.Values("Content-Type")) != 1 || len(parameters) > 1 || (len(parameters) == 1 && !strings.EqualFold(parameters["charset"], "utf-8")) {
		h.writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Use application/json with optional charset=utf-8.")

		return
	}

	body := http.MaxBytesReader(w, r.Body, int64(h.limits.MaxRequestBodyBytes))
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			h.writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "The request exceeds the configured byte limit.")
		case r.Context().Err() != nil:
			h.writeOperationError(w, r, r.Context().Err())
		default:
			h.writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body could not be read.")
		}

		return
	}

	compile := r.URL.Path == "/api/v1/compile"
	src, options, err := decodeToolingRequest(data, compile)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "The request must match the documented JSON schema.")

		return
	}

	if len(src.Text) > h.toolingLimits.MaxSourceBytes {
		h.writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "The source exceeds the configured byte limit.")

		return
	}

	var diagnostics []playground.Diagnostic
	response := toolingResponse{Status: "ok"}
	if compile {
		options.MaxBytecodeBytes = h.toolingLimits.MaxBytecodeBytes
		result, err := h.provider.Compile(r.Context(), src, options)
		if err != nil {
			h.writeOperationError(w, r, err)

			return
		}

		diagnostics = result.Diagnostics
		response.Timings = &compileTimings{CompileMs: float64(result.CompileDuration) / float64(time.Millisecond)}
		if len(diagnostics) == 0 {
			response.Artifacts = &result.Artifacts
		}
	} else if r.URL.Path == "/api/v1/format" {
		result, err := h.provider.Format(r.Context(), src, playground.FormatOptions{MaxFormattedSourceBytes: h.toolingLimits.MaxFormattedSourceBytes})
		if err != nil {
			h.writeOperationError(w, r, err)

			return
		}

		diagnostics = result.Diagnostics
		if len(diagnostics) == 0 {
			response.FormattedSource = &result.FormattedSource
		}
	} else {
		result, err := h.provider.Analyze(r.Context(), src)
		if err != nil {
			h.writeOperationError(w, r, err)

			return
		}

		diagnostics = result.Diagnostics
	}

	if err := r.Context().Err(); err != nil {
		h.writeOperationError(w, r, err)

		return
	}

	if len(diagnostics) != 0 {
		response.Status = "invalid"
	}

	response.Diagnostics = []httpDiagnostic{}
	if len(diagnostics) != 0 {
		response.Diagnostics = newSourceIndex(src.Text).diagnostics(diagnostics)
	}

	h.writeJSON(w, r, http.StatusOK, response)
}

func (h *Handler) writeOperationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, playground.ErrInternal):
		// A simultaneous cancellation must not hide a cleanup/internal failure.
	case errors.Is(err, playground.ErrClosed):
		h.writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "The runtime is closing.")

		return
	case errors.Is(err, context.DeadlineExceeded):
		h.writeError(w, r, http.StatusRequestTimeout, "request_timeout", "The request deadline expired.")

		return
	case errors.Is(err, context.Canceled):
		h.writeError(w, r, http.StatusRequestTimeout, "request_canceled", "The request was canceled.")

		return
	case errors.Is(err, playground.ErrResponseTooLarge):
		h.writeError(w, r, http.StatusUnprocessableEntity, "response_too_large", "The generated response exceeds the configured limit.")

		return
	}

	h.logger.Error("Tooling operation failed", "code", "internal_error")
	h.writeError(w, r, http.StatusInternalServerError, "internal_error", "The service could not complete the request.")
}
