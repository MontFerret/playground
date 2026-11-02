package runtimehost

import (
	"reflect"
	"testing"

	"github.com/MontFerret/ferret/v2/pkg/compiler"
	"github.com/MontFerret/ferret/v2/pkg/source"

	"github.com/MontFerret/playground/internal/playground"
)

// An idempotent rewrite can still change a literal's value. Compare compiled
// constants on both sides of the rewrite, then format the result a second time.
func TestFormatPreservesLiteralValues(t *testing.T) {
	host := toolingTestHost(t, nil)
	c, err := compiler.New()
	if err != nil {
		t.Fatal(err)
	}

	options := playground.FormatOptions{MaxFormattedSourceBytes: 1024}
	for _, input := range []string{`RETURN "a\b"`, `RETURN 'a"b'`, `RETURN "a\\b"`, `RETURN "hello\n\"😀\""`} {
		t.Run(input, func(t *testing.T) {
			src := playground.Source{Name: "literal.fql", Text: input}
			before, err := c.Compile(t.Context(), source.New(src.Name, src.Text))
			if err != nil {
				t.Fatal(err)
			}

			first, err := host.Format(t.Context(), src, options)
			if err != nil || len(first.Diagnostics) != 0 {
				t.Fatalf("format input: %+v, %v", first, err)
			}

			src.Text = first.FormattedSource
			after, err := c.Compile(t.Context(), source.New(src.Name, src.Text))
			if err != nil {
				t.Fatal(err)
			}

			if len(before.Constants) != 1 || !reflect.DeepEqual(before.Constants, after.Constants) {
				t.Fatalf("compiled literal changed: %q -> %q", before.Constants, after.Constants)
			}

			second, err := host.Format(t.Context(), src, options)
			if err != nil || len(second.Diagnostics) != 0 || second.FormattedSource != first.FormattedSource {
				t.Fatalf("second format: %+v, %v; want %q", second, err, first.FormattedSource)
			}
		})
	}
}
