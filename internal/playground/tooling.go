package playground

import (
	"errors"
	"slices"
	"time"

	"github.com/MontFerret/api/source"
)

const (
	OptimizationNone    Optimization       = "none"
	OptimizationBasic   Optimization       = "basic"
	OptimizationFull    Optimization       = "full"
	DefaultOptimization                    = OptimizationFull
	ArtifactBytecode    InspectionArtifact = "bytecode"
	ArtifactParameters  InspectionArtifact = "parameters"
)

var (
	ErrClosed           = errors.New("runtime host is closing")
	ErrResponseTooLarge = errors.New("generated response exceeds limit")
	ErrInvalidOptions   = errors.New("invalid tooling options")
	ErrInternal         = errors.New("internal tooling failure")
)

type (
	// Source is submitted text and a display label, never a filesystem path.
	Source struct {
		Name string
		Text string
	}

	Optimization       string
	InspectionArtifact string

	// CompileOptions separates native settings from HTTP decoding. The bytecode
	// limit is supplied by the operator, never decoded from public request data.
	CompileOptions struct {
		Optimization     Optimization
		Include          []InspectionArtifact
		MaxBytecodeBytes int
	}

	// Diagnostic contains only public source-error information. Span, when
	// present, uses native zero-based UTF-8 byte offsets with an exclusive end.
	Diagnostic struct {
		Phase   string
		Message string
		Hint    string
		Source  string
		Span    *source.Span
	}

	AnalysisResult struct {
		Diagnostics []Diagnostic
	}

	// FormatOptions contains an operator limit, not public formatting settings.
	FormatOptions struct {
		MaxFormattedSourceBytes int
	}

	// FormatResult owns complete formatted text. Failed formatting exposes none
	// of the native formatter's potentially partial output.
	FormatResult struct {
		Diagnostics     []Diagnostic
		FormattedSource string
	}

	// CompileResult owns detached data; no native plan escapes the operation.
	CompileResult struct {
		Diagnostics     []Diagnostic
		Artifacts       Artifacts
		CompileDuration time.Duration
	}

	Artifacts struct {
		Bytecode *TextArtifact `json:"bytecode,omitempty"`
		// A pointer distinguishes requested empty metadata from omission.
		Parameters *[]string `json:"parameters,omitempty"`
	}

	TextArtifact struct {
		Format string `json:"format"`
		Text   string `json:"text"`
	}

	ToolingLimits struct {
		MaxSourceBytes          int `json:"maxSourceBytes"`
		MaxBytecodeBytes        int `json:"maxBytecodeBytes"`
		MaxFormattedSourceBytes int `json:"maxFormattedSourceBytes"`
	}
)

// OptimizationLevels returns a fresh, ordered list of supported settings.
func OptimizationLevels() []Optimization {
	return []Optimization{OptimizationNone, OptimizationBasic, OptimizationFull}
}

// InspectionArtifacts returns a fresh list of implemented artifacts.
func InspectionArtifacts() []InspectionArtifact {
	return []InspectionArtifact{ArtifactBytecode, ArtifactParameters}
}

// ValidOptimization accepts only the advertised native settings.
func ValidOptimization(value Optimization) bool {
	return slices.Contains(OptimizationLevels(), value)
}

// ValidArtifacts rejects unknown and repeated artifact names.
func ValidArtifacts(values []InspectionArtifact) bool {
	for i, value := range values {
		if !slices.Contains(InspectionArtifacts(), value) || slices.Contains(values[:i], value) {
			return false
		}
	}

	return true
}
