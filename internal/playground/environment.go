// Package playground defines transport-independent service models.
package playground

import "github.com/MontFerret/api"

type (
	// Environment describes implemented service behavior, not engine possibilities.
	Environment struct {
		APIVersion   string       `json:"apiVersion"`
		Service      Service      `json:"service"`
		Runtime      Runtime      `json:"runtime"`
		Capabilities Capabilities `json:"capabilities"`
		Limits       Limits       `json:"limits"`
	}

	// Service identifies this binary separately from its embedded runtime.
	Service struct {
		Name    string  `json:"name"`
		Version *string `json:"version"`
	}

	// Runtime describes the native host that was successfully initialized.
	Runtime struct {
		Name            string       `json:"name"`
		Version         *api.Version `json:"version"`
		StandardLibrary string       `json:"standardLibrary"`
		Modules         []Module     `json:"modules"`
	}

	// Module metadata accompanies the registration owned by runtime composition.
	Module struct {
		Name    string  `json:"name"`
		Version *string `json:"version"`
	}

	// Capabilities lists only currently usable operations and their options.
	Capabilities struct {
		Environment         bool                 `json:"environment"`
		Analyze             bool                 `json:"analyze"`
		Format              bool                 `json:"format"`
		Compile             bool                 `json:"compile"`
		Execute             bool                 `json:"execute"`
		Debug               bool                 `json:"debug"`
		FunctionCatalog     bool                 `json:"functionCatalog"`
		OptimizationLevels  []Optimization       `json:"optimizationLevels"`
		DefaultOptimization Optimization         `json:"defaultOptimization"`
		InspectionArtifacts []InspectionArtifact `json:"inspectionArtifacts"`
	}

	// Limits contains only settings enforced by enabled service behavior.
	Limits struct {
		HTTP    HTTPLimits    `json:"http"`
		Tooling ToolingLimits `json:"tooling"`
	}

	// HTTPLimits reports net/http settings. Durations use fractional milliseconds
	// so valid sub-millisecond operator settings are not rounded down to zero.
	HTTPLimits struct {
		MaxHeaderBytes      int     `json:"maxHeaderBytes"`
		MaxRequestBodyBytes int     `json:"maxRequestBodyBytes"`
		MaxResponseBytes    int     `json:"maxResponseBytes"`
		ReadHeaderTimeoutMs float64 `json:"readHeaderTimeoutMs"`
		ReadTimeoutMs       float64 `json:"readTimeoutMs"`
		WriteTimeoutMs      float64 `json:"writeTimeoutMs"`
		IdleTimeoutMs       float64 `json:"idleTimeoutMs"`
	}
)
