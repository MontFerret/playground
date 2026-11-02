package runtimehost

import (
	"context"
	"errors"

	"github.com/MontFerret/ferret/v2/pkg/source"

	"github.com/MontFerret/playground/internal/playground"
)

// Format uses native defaults without compiling or executing the source. The
// writer bounds retained output and observes cancellation between writes; native
// parsing and internal formatting work cannot be forcibly interrupted.
func (h *Host) Format(ctx context.Context, src playground.Source, options playground.FormatOptions) (result playground.FormatResult, resultErr error) {
	if ctx == nil {
		return result, playground.ErrInternal
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}

	if options.MaxFormattedSourceBytes <= 0 {
		return result, playground.ErrInvalidOptions
	}

	if err := h.admit(); err != nil {
		return result, err
	}
	defer h.active.Done()
	defer func() {
		if err := ctx.Err(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}

		if resultErr != nil {
			result = playground.FormatResult{}
		}
	}()

	out := formatWriter{ctx: ctx, limit: options.MaxFormattedSourceBytes}
	err := h.formatter.Format(&out, source.New(src.Name, src.Text))
	if err != nil {
		// Only the exact error returned by our writer is a known boundary
		// failure. An internal diagnostic wrapping such a cause stays internal.
		if out.err != nil && err == out.err {
			return result, err
		}

		result.Diagnostics, resultErr = nativeDiagnostics(err)

		return result, resultErr
	}

	result.Diagnostics = []playground.Diagnostic{}
	result.FormattedSource = out.buffer.String()

	return result, nil
}
