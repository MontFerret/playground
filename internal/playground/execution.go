package playground

import (
	"encoding/json"
	"errors"
	"time"
)

var ErrInvalidParams = errors.New("invalid JSON bind parameters")

const (
	RuntimeError     ExecutionFailure = "runtime_error"
	ExecutionTimeout ExecutionFailure = "execution_timeout"
	OutputTooLarge   ExecutionFailure = "output_too_large"
)

type (
	ExecutionFailure string

	// ExecuteOptions binds request-local JSON values. MaxOutputBytes is supplied by the host's caller.
	ExecuteOptions struct {
		Optimization   Optimization
		Params         map[string]json.RawMessage
		MaxOutputBytes int
	}

	// ExecuteResult carries the primary program outcome even when cleanup also
	// returns an error. Only successful execution can carry detached JSON.
	ExecuteResult struct {
		Diagnostics     []Diagnostic
		Failure         ExecutionFailure
		Output          json.RawMessage
		CompileDuration *time.Duration
		ExecuteDuration *time.Duration
		TotalDuration   time.Duration
	}

	ExecutionLimits struct {
		MaxConcurrentExecutions int     `json:"maxConcurrentExecutions"`
		MaxTimeoutMs            float64 `json:"maxTimeoutMs"`
		MaxOutputBytes          int     `json:"maxOutputBytes"`
	}
)
