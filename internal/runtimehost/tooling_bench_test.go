package runtimehost

import (
	"context"
	"testing"

	"github.com/MontFerret/ferret/v2/pkg/asm"
	"github.com/MontFerret/ferret/v2/pkg/bytecode/artifact"
	"github.com/MontFerret/ferret/v2/pkg/engine"
	"github.com/MontFerret/ferret/v2/pkg/source"

	"github.com/MontFerret/playground/internal/playground"
)

// Native and service cases perform the same native operations, including
// source construction and cleanup, so the comparison isolates adapter overhead.
func BenchmarkTooling(b *testing.B) {
	for _, workload := range []struct{ name, text string }{
		{"small", "RETURN @value + 1"},
		{"loop", `FOR x IN 1..100 RETURN {value: x + @offset, label: "example"}`},
	} {
		for _, inspect := range []bool{false, true} {
			mode := "compile"
			if inspect {
				mode = "inspect"
			}

			for _, adapter := range []string{"native", "service"} {
				b.Run(workload.name+"/"+mode+"/"+adapter, func(b *testing.B) {
					host := toolingTestHost(b, nil)
					options := playground.CompileOptions{Optimization: playground.DefaultOptimization, MaxBytecodeBytes: 1 << 20}
					if inspect {
						options.Include = playground.InspectionArtifacts()
					}

					// ANTLR initializes shared prediction state lazily. Warm it before
					// either timed variant so startup does not bias the native case.
					for range 32 {
						if err := benchmarkNativeTooling(host, workload.text, inspect); err != nil {
							b.Fatal(err)
						}
					}

					b.ReportAllocs()
					for b.Loop() {
						if adapter == "service" {
							if _, err := host.Compile(context.Background(), playground.Source{Name: "benchmark", Text: workload.text}, options); err != nil {
								b.Fatal(err)
							}

							continue
						}

						if err := benchmarkNativeTooling(host, workload.text, inspect); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func benchmarkNativeTooling(host *Host, text string, inspect bool) (resultErr error) {
	plan, err := host.engine.Compile(context.Background(), source.New("benchmark", text), engine.WithPlanOptimizationLevel(engine.OptimizationFull))
	if err != nil {
		return err
	}
	defer func() {
		if err := plan.Close(); err != nil {
			resultErr = err
		}
	}()

	if inspect {
		_ = plan.Params()
		data, err := plan.Marshal()
		if err != nil {
			return err
		}

		program, err := artifact.Unmarshal(data)
		if err != nil {
			return err
		}

		if _, err := asm.Disassemble(program); err != nil {
			return err
		}
	}

	return nil
}
