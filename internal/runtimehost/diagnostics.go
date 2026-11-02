package runtimehost

import (
	"context"
	"errors"

	"github.com/MontFerret/ferret/v2/pkg/diagnostics"
	parserdiagnostics "github.com/MontFerret/ferret/v2/pkg/parser/diagnostics"

	"github.com/MontFerret/playground/internal/playground"
)

// Walk the structured error tree in upstream order. A mixed source/internal
// failure must not become an ordinary invalid-program outcome. Diagnostic
// causes and internal stack-bearing messages never cross this boundary.
func nativeDiagnostics(err error) ([]playground.Diagnostic, error) {
	var result []playground.Diagnostic
	var failures []error
	var visit func(error)
	visit = func(current error) {
		if current == nil {
			return
		}

		if diagnostic, ok := current.(*diagnostics.Diagnostic); ok {
			phase := "compile"
			switch diagnostic.Kind {
			case parserdiagnostics.SyntaxError:
				phase = "parse"
			case parserdiagnostics.NameError, parserdiagnostics.SemanticError, diagnostics.TypeError, diagnostics.Unsupported:
			default:
				failures = append(failures, playground.ErrInternal)

				return
			}

			converted := playground.Diagnostic{
				Phase: phase, Message: diagnostic.Message, Hint: diagnostic.Hint, Source: diagnostic.Source.Name(),
			}
			for i, span := range diagnostic.Spans {
				if i == 0 || span.Main {
					value := span.Span
					converted.Span = &value
				}

				if span.Main {
					break
				}
			}

			result = append(result, converted)

			return
		}

		switch wrapped := current.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range wrapped.Unwrap() {
				visit(child)
			}
		case interface{ Unwrap() error }:
			visit(wrapped.Unwrap())
		default:
			if errors.Is(current, context.Canceled) || errors.Is(current, context.DeadlineExceeded) {
				failures = append(failures, current)
			} else {
				failures = append(failures, playground.ErrInternal)
			}
		}
	}
	visit(err)

	if len(failures) != 0 {
		return nil, errors.Join(failures...)
	}

	if len(result) == 0 {
		return nil, playground.ErrInternal
	}

	return result, nil
}
