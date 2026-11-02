package runtimehost

import (
	"context"
	"strings"
	"testing"

	"github.com/MontFerret/ferret/v2/pkg/source"

	"github.com/MontFerret/playground/internal/playground"
)

func BenchmarkFormat(b *testing.B) {
	for _, workload := range []struct{ name, text string }{
		{"ASCII", "// comment\nFOR x IN 1..100 RETURN {value: x + @offset, label: \"example\"}"},
		{"Unicode", "// é😀\r\nFOR x IN 1..100 RETURN {value: x + @offset, label: \"é😀\"}"},
	} {
		for _, adapter := range []string{"native", "service"} {
			b.Run(workload.name+"/"+adapter, func(b *testing.B) {
				host := toolingTestHost(b, nil)
				for range 32 {
					var out strings.Builder
					if err := host.formatter.Format(&out, source.New("benchmark", workload.text)); err != nil {
						b.Fatal(err)
					}
				}

				b.ReportAllocs()
				for b.Loop() {
					if adapter == "service" {
						if _, err := host.Format(context.Background(), playground.Source{Name: "benchmark", Text: workload.text}, playground.FormatOptions{MaxFormattedSourceBytes: 1 << 20}); err != nil {
							b.Fatal(err)
						}

						continue
					}

					var out strings.Builder
					if err := host.formatter.Format(&out, source.New("benchmark", workload.text)); err != nil {
						b.Fatal(err)
					}
					_ = out.String()
				}
			})
		}
	}
}
