package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/MontFerret/playground/internal/playground"
)

type (
	executionResponse struct {
		Status      string           `json:"status"`
		Diagnostics []httpDiagnostic `json:"diagnostics"`
		Output      json.RawMessage  `json:"output,omitempty"`
		Timings     executionTimings `json:"timings"`
	}

	executionTimings struct {
		CompileMs *float64 `json:"compileMs,omitempty"`
		ExecuteMs *float64 `json:"executeMs,omitempty"`
		TotalMs   float64  `json:"totalMs"`
	}
)

func (h *Handler) serveExecution(w http.ResponseWriter, r *http.Request, request postRequest) {
	timeout := h.maxExecutionTimeout
	// Compare before multiplying. Even the largest accepted millisecond value
	// can safely select the operator cap without overflowing time.Duration.
	if request.timeoutMs > 0 && request.timeoutMs <= timeout.Milliseconds() {
		timeout = time.Duration(request.timeoutMs) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	result, err := h.provider.Execute(ctx, request.source, playground.ExecuteOptions{
		Optimization: request.optimization, Params: request.params, MaxOutputBytes: h.maxOutputBytes,
	})
	response := executionResponse{Status: "ok", Diagnostics: []httpDiagnostic{}, Timings: newExecutionTimings(result)}
	switch {
	case result.Failure != "":
		diagnostic := httpDiagnostic{Severity: "error", Phase: "execute", Code: string(result.Failure)}
		switch result.Failure {
		case playground.RuntimeError:
			diagnostic.Message = "The program failed during execution."
		case playground.ExecutionTimeout:
			diagnostic.Message = "The execution deadline expired."
		case playground.OutputTooLarge:
			diagnostic.Phase = "output"
			diagnostic.Message = "The encoded result exceeds the configured byte limit."
		default:
			h.writeOperationError(w, r, playground.ErrInternal)

			return
		}

		response.Status = "failed"
		response.Diagnostics = []httpDiagnostic{diagnostic}
	case len(result.Diagnostics) > 0:
		response.Status = "invalid"
		response.Diagnostics = newSourceIndex(request.source.Text).diagnostics(result.Diagnostics)
	case err != nil:
		h.writeOperationError(w, r, err)

		return
	default:
		response.Output = result.Output
	}

	// Once execution has settled, an expired parent deadline is itself a
	// deliverable program outcome. No native work uses a detached context.
	h.writeBufferedJSON(w, r, http.StatusOK, response, response.Status == "ok")
}

func newExecutionTimings(result playground.ExecuteResult) executionTimings {
	timings := executionTimings{TotalMs: float64(result.TotalDuration) / float64(time.Millisecond)}
	if result.CompileDuration != nil {
		ms := float64(*result.CompileDuration) / float64(time.Millisecond)
		timings.CompileMs = &ms
	}

	if result.ExecuteDuration != nil {
		ms := float64(*result.ExecuteDuration) / float64(time.Millisecond)
		timings.ExecuteMs = &ms
	}

	return timings
}
