package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MontFerret/ferret/v2/pkg/diagnostics"
	parserdiagnostics "github.com/MontFerret/ferret/v2/pkg/parser/diagnostics"
	"github.com/MontFerret/ferret/v2/pkg/source"

	"github.com/MontFerret/playground/internal/playground"
)

func TestStructuredDiagnosticConversion(t *testing.T) {
	src := source.New("label.fql", "RETURN bad")
	first := &diagnostics.Diagnostic{Kind: parserdiagnostics.SyntaxError, Source: src, Message: "first", Hint: "hint", Spans: []diagnostics.ErrorSpan{
		{Span: source.Span{Start: 0, End: 1}}, {Main: true, Span: source.Span{Start: 7, End: 10}},
	}}
	second := &diagnostics.Diagnostic{Kind: parserdiagnostics.NameError, Source: src, Message: "second"}
	values, err := nativeDiagnostics(fmt.Errorf("wrapper: %w", errors.Join(first, second)))
	if err != nil || len(values) != 2 || values[0].Message != "first" || values[0].Hint != "hint" || values[0].Phase != "parse" || values[0].Span.Start != 7 || values[1].Phase != "compile" || values[1].Span != nil {
		t.Fatalf("structured information lost: %+v %v", values, err)
	}

	for _, failure := range []error{
		errors.New("private text"), diagnostics.NewUnexpectedError(src, "private stack"),
		&diagnostics.Diagnostic{Kind: "UnknownKind", Message: "private"},
	} {
		values, err := nativeDiagnostics(errors.Join(first, failure, context.Canceled))
		if !errors.Is(err, playground.ErrInternal) || values != nil {
			t.Fatalf("mixed error became source error: %+v %v", values, err)
		}
	}

	values, err = nativeDiagnostics(errors.Join(first, context.DeadlineExceeded))
	if !errors.Is(err, context.DeadlineExceeded) || values != nil {
		t.Fatalf("deadline became invalid program: %+v %v", values, err)
	}
}
