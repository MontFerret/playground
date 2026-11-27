package runtimehost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/MontFerret/contrib/modules/web/html/drivers"
	ferretjson "github.com/MontFerret/ferret/v2/pkg/encoding/json"
	"github.com/MontFerret/ferret/v2/pkg/engine"
	"github.com/MontFerret/ferret/v2/pkg/runtime"

	"github.com/MontFerret/playground/internal/playground"
)

// Execute compiles once and runs a fresh session on the shared engine. ctx
// owns the entire invocation, including output admission and consumption.
func (h *Host) Execute(ctx context.Context, src playground.Source, options playground.ExecuteOptions) (result playground.ExecuteResult, err error) {
	started := time.Now()
	if ctx == nil {
		return result, playground.ErrInternal
	}

	if options.Optimization == "" {
		options.Optimization = playground.DefaultOptimization
	}

	if !playground.ValidOptimization(options.Optimization) || options.MaxOutputBytes <= 0 {
		return result, playground.ErrInvalidOptions
	}

	if err := h.admit(); err != nil {
		return result, err
	}
	var settleBrowser func()
	defer func() {
		if settleBrowser == nil {
			h.active.Done()

			return
		}

		// On terminal shutdown the host still owns this admission until the
		// canceled browser worker has settled.
		go func() {
			defer h.active.Done()
			settleBrowser()
		}()
	}()

	var cleanupErr error
	closeOwned := func(child io.Closer) {
		if closeErr := child.Close(); closeErr != nil {
			cleanupErr = errors.Join(cleanupErr, closeErr)
		}
	}

	defer func() {
		result.TotalDuration = time.Since(started)
		if err == nil && cleanupErr == nil && len(result.Diagnostics) == 0 {
			err = ctx.Err()
		}

		if ctx.Err() == context.DeadlineExceeded && errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, playground.ErrInternal) && result.Failure == "" {
			result.Failure = playground.ExecutionTimeout
		}

		if cleanupErr != nil {
			// HTTP preserves an earlier program outcome, so record the cleanup
			// failure separately. Native errors may contain caller data.
			slog.Error("Execution resource cleanup failed")
			if err == nil {
				err = playground.ErrInternal
			}

			err = errors.Join(err, cleanupErr)
		}

		if err != nil {
			result.Output = nil
		}
	}()

	if err := ctx.Err(); err != nil {
		return result, err
	}

	params := make(runtime.Params, len(options.Params))
	for name, raw := range options.Params {
		value, err := ferretjson.Default.Decode(ctx, bytes.NewReader(raw))
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, ctxErr
			}

			return result, playground.ErrInvalidParams
		}

		params[name] = value
	}

	plan, compileDuration, err := h.compilePlan(ctx, src, options.Optimization)
	result.CompileDuration = &compileDuration
	if plan != nil {
		defer closeOwned(plan)
	}

	if err != nil {
		result.Diagnostics, err = nativeDiagnostics(err)

		return result, err
	}

	if plan == nil {
		return result, playground.ErrInternal
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}

	executing := time.Now()
	if h.cdpOrigin != "" {
		driver := newBrowserDriver(h.cdpOrigin)
		defer func() {
			ctx, cancel := h.browserCleanupContext()
			defer cancel()

			closeErr := driver.CloseContext(ctx)
			if closeErr != nil {
				cleanupErr = errors.Join(cleanupErr, closeErr, context.Cause(ctx))
				h.failBrowser()

				if ctx.Err() != nil {
					settleBrowser = func() { _ = driver.CloseContext(context.Background()) }
				}
			}
		}()

		memoryDriver := newHostedMemoryDriver()
		defer closeOwned(memoryDriver)

		container := drivers.NewContainer()
		if err := container.Register(memoryDriver); err != nil {
			return result, err
		}

		if err := container.Register(&hostedHTMLDriver{Driver: driver}); err != nil {
			return result, err
		}

		container.SetDefault(memoryDriver.Name())
		ctx = container.WithContext(ctx)
	}

	session, err := plan.NewSession(ctx, engine.WithSessionRuntimeParams(params), engine.WithOutputContentType(ferretjson.ContentType))
	if session != nil {
		defer closeOwned(session)
	}

	// Stop the execution timer after output settlement and before parent cleanup.
	defer func() {
		duration := time.Since(executing)
		result.ExecuteDuration = &duration
	}()

	if err != nil {
		return result, err
	}

	if session == nil {
		return result, playground.ErrInternal
	}

	output, err := session.Run(ctx)
	if err != nil {
		if output != nil {
			defer closeOwned(output)
		}

		return result, err
	}

	if output == nil {
		return result, playground.ErrInternal
	}

	result.Output, result.Failure, err = settleOutput(ctx, output, options.MaxOutputBytes)

	return result, err
}
