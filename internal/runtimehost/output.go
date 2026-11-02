package runtimehost

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/MontFerret/api"
	"github.com/MontFerret/ferret/v2/pkg/diagnostics"
	ferretjson "github.com/MontFerret/ferret/v2/pkg/encoding/json"
	"github.com/MontFerret/ferret/v2/pkg/vm"

	"github.com/MontFerret/playground/internal/playground"
)

// A descriptor alone never reserves a large buffer. Native one-chunk output
// can still copy directly into its final allocation.
const maxOutputReservation = 64 << 10

var (
	errOutputTooLarge = errors.New("encoded output exceeds limit")
	errRuntime        = errors.New("FQL runtime error")
)

// settleOutput takes ownership immediately, including when a canceled context
// prevents Consume from claiming the handle. Close reports cleanup separately
// from the terminal operation, which can also include that same cleanup error.
func settleOutput(ctx context.Context, output api.Output, limit int) (data json.RawMessage, failure playground.ExecutionFailure, err error) {
	defer func() {
		closeErr := output.Close()
		if err == nil && closeErr == nil {
			return
		}

		data = nil
		// Consume can return operation and cleanup errors in one tree. Only
		// operation causes determine the public classification.
		switch outputCause(err, closeErr) {
		case errOutputTooLarge:
			failure = playground.OutputTooLarge
		case errRuntime:
			failure = playground.RuntimeError
		case context.DeadlineExceeded:
			failure = playground.ExecutionTimeout
		case context.Canceled:
			// The caller's transport may already be gone.
		default:
			err = errors.Join(playground.ErrInternal, err)
		}

		if closeErr != nil && !errors.Is(err, closeErr) {
			err = errors.Join(err, closeErr)
		}
	}()

	data, err = consumeJSON(ctx, output, limit)

	return data, failure, err
}

// consumeJSON borrows output. The caller must also close it on preflight
// rejection or canceled admission, which leave the one-shot handle unclaimed.
func consumeJSON(ctx context.Context, output api.Output, limit int) (json.RawMessage, error) {
	metadata := output.Metadata()
	if metadata.LengthKnown && metadata.Length < 0 {
		return nil, playground.ErrInternal
	}

	if metadata.LengthKnown && metadata.Length > int64(limit) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		return nil, errOutputTooLarge
	}

	var data json.RawMessage
	present := false
	err := output.Consume(ctx, func(ctx context.Context, chunk []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		if len(chunk) > limit-len(data) {
			return errOutputTooLarge
		}

		if metadata.LengthKnown && int64(len(data)+len(chunk)) > metadata.Length {
			return playground.ErrInternal
		}

		present = true
		// Grow geometrically within the operator cap, using a bounded
		// reservation when the producer already knows its encoded length.
		if len(chunk) > cap(data)-len(data) {
			capacity := cap(data) + min(cap(data), limit-cap(data))
			if metadata.LengthKnown {
				capacity = min(int(metadata.Length), max(capacity, maxOutputReservation))
			}

			grown := make(json.RawMessage, len(data), max(capacity, len(data)+len(chunk)))
			copy(grown, data)
			data = grown
		}

		data = append(data, chunk...)

		return nil
	})
	if err != nil {
		return nil, err
	}

	if metadata.LengthKnown && metadata.Length != int64(len(data)) {
		return nil, playground.ErrInternal
	}

	if present && (metadata.ContentType != ferretjson.ContentType || !utf8.Valid(data) || !json.Valid(data)) {
		return nil, playground.ErrInternal
	}

	return data, nil
}

// Walk joined outcomes without letting a sibling hook or VM invariant failure
// become a program diagnostic. Ignore the recorded cleanup cause here only.
func outputCause(err, cleanupErr error) error {
	if err == nil || errors.Is(cleanupErr, err) {
		return nil
	}

	switch cause := err.(type) {
	case *vm.RuntimeError:
		if cause.Diagnostic == nil {
			return playground.ErrInternal
		}

		switch cause.Kind {
		case vm.ArityError, vm.InvalidArgument, vm.NullDereferenced, vm.DivideByZero,
			vm.ModuloByZero, vm.AssertionFailed, vm.UnresolvedSymbol, vm.UncaughtError, diagnostics.TypeError:
			if errors.Is(cause, context.DeadlineExceeded) {
				return context.DeadlineExceeded
			}

			if errors.Is(cause, context.Canceled) {
				return context.Canceled
			}

			return errRuntime
		default:
			return playground.ErrInternal
		}
	case interface{ Unwrap() []error }:
		var primary error
		for _, child := range cause.Unwrap() {
			current := outputCause(child, cleanupErr)
			if current == playground.ErrInternal {
				return current
			}

			if primary == nil {
				primary = current
			}
		}

		return primary
	case interface{ Unwrap() error }:
		return outputCause(cause.Unwrap(), cleanupErr)
	}

	switch err {
	case context.DeadlineExceeded, context.Canceled, errOutputTooLarge:
		return err
	default:
		return playground.ErrInternal
	}
}
