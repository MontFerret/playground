package runtimehost

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/MontFerret/ferret/v2/pkg/asm"
	"github.com/MontFerret/ferret/v2/pkg/bytecode/artifact"
	"github.com/MontFerret/ferret/v2/pkg/engine"
	"github.com/MontFerret/ferret/v2/pkg/source"

	"github.com/MontFerret/playground/internal/playground"
)

// Analyze performs native compilation checks without binding parameters or
// host functions. The temporary plan is closed before returning diagnostics.
func (h *Host) Analyze(ctx context.Context, src playground.Source) (playground.AnalysisResult, error) {
	result, err := h.compile(ctx, src, playground.CompileOptions{Optimization: playground.DefaultOptimization})
	if err != nil {
		return playground.AnalysisResult{}, err
	}

	return playground.AnalysisResult{Diagnostics: result.Diagnostics}, nil
}

// Compile returns detached inspection data from one native compilation. It
// never creates a session or executes the program. Bytecode limits apply after
// upstream disassembly has materialized its string, not to its allocations.
func (h *Host) Compile(ctx context.Context, src playground.Source, options playground.CompileOptions) (playground.CompileResult, error) {
	return h.compile(ctx, src, options)
}

func (h *Host) compile(ctx context.Context, src playground.Source, options playground.CompileOptions) (result playground.CompileResult, resultErr error) {
	if ctx == nil {
		return result, playground.ErrInternal
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}

	if options.Optimization == "" {
		options.Optimization = playground.DefaultOptimization
	}

	if !playground.ValidOptimization(options.Optimization) || !playground.ValidArtifacts(options.Include) {
		return result, playground.ErrInvalidOptions
	}

	wantBytecode := slices.Contains(options.Include, playground.ArtifactBytecode)
	if wantBytecode && options.MaxBytecodeBytes <= 0 {
		return result, playground.ErrInvalidOptions
	}

	if err := h.admit(); err != nil {
		return result, err
	}
	defer h.active.Done()

	var level engine.OptimizationLevel
	switch options.Optimization {
	case playground.OptimizationNone:
		level = engine.OptimizationNone
	case playground.OptimizationBasic:
		level = engine.OptimizationBasic
	case playground.OptimizationFull:
		level = engine.OptimizationFull
	}

	nativeSource := source.New(src.Name, src.Text)
	started := time.Now()
	plan, err := h.engine.Compile(ctx, nativeSource, engine.WithPlanOptimizationLevel(level))
	result.CompileDuration = time.Since(started)
	// Register cleanup even for a future native implementation that returns a
	// non-nil plan alongside an error. Cleanup failures override source outcomes.
	defer func() {
		if plan != nil {
			if closeErr := plan.Close(); closeErr != nil {
				resultErr = errors.Join(resultErr, playground.ErrInternal, closeErr)
			}
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			resultErr = errors.Join(resultErr, ctxErr)
		}

		if resultErr != nil {
			result = playground.CompileResult{}
		}
	}()

	if err != nil {
		result.Diagnostics, resultErr = nativeDiagnostics(err)

		return result, resultErr
	}

	if plan == nil {
		return result, playground.ErrInternal
	}

	result.Diagnostics = []playground.Diagnostic{}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	if slices.Contains(options.Include, playground.ArtifactParameters) {
		parameters := plan.Params()
		if parameters == nil {
			parameters = []string{}
		}

		result.Artifacts.Parameters = &parameters
	}

	if wantBytecode {
		// Plan exposes no program accessor. Use its supported artifact codec;
		// never inspect private fields or compile again for disassembly.
		data, err := plan.Marshal()
		if err != nil {
			return result, errors.Join(playground.ErrInternal, err)
		}

		if err := ctx.Err(); err != nil {
			return result, err
		}

		program, err := artifact.Unmarshal(data)
		if err != nil {
			return result, errors.Join(playground.ErrInternal, err)
		}

		if err := ctx.Err(); err != nil {
			return result, err
		}

		text, err := asm.Disassemble(program)
		if err != nil {
			return result, errors.Join(playground.ErrInternal, err)
		}

		if len(text) > options.MaxBytecodeBytes {
			return result, playground.ErrResponseTooLarge
		}

		result.Artifacts.Bytecode = &playground.TextArtifact{Format: "text", Text: text}
	}

	return result, nil
}

// Admission and closing share a lock so Wait can never race a new Add after
// closing begins. Active work includes artifact generation and plan cleanup.
func (h *Host) admit() error {
	h.admission.Lock()
	defer h.admission.Unlock()

	if h.closing {
		return playground.ErrClosed
	}

	h.active.Add(1)

	return nil
}
