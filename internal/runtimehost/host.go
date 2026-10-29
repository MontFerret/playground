// Package runtimehost owns native runtime construction and its resources.
package runtimehost

import (
	"context"
	"errors"
	"runtime/debug"
	"sync"

	"github.com/MontFerret/ferret/v2"
	"github.com/MontFerret/ferret/v2/pkg/module"
	"github.com/MontFerret/ferret/v2/pkg/stdlib"

	"github.com/MontFerret/playground/internal/playground"
)

type (
	// Host owns its engine. Close it after all users of the host have settled.
	Host struct {
		engine      *ferret.Engine
		description playground.Runtime
		closeOnce   sync.Once
		closeDone   chan struct{}
		closeErr    error
		admission   sync.Mutex
		closing     bool
		active      sync.WaitGroup
	}

	registration struct {
		module  module.Module
		version *string
	}
)

// New initializes the safe standard library without optional modules. Native
// construction owns rollback; no browser, query, or external request is started.
func New() (*Host, error) {
	info, _ := debug.ReadBuildInfo()

	return newHost(nil, info)
}

// Environment returns a detached description, also available after closure.
// It does no runtime work and honors cancellation before copying the snapshot.
func (h *Host) Environment(ctx context.Context) (playground.Runtime, error) {
	if ctx == nil {
		return playground.Runtime{}, errors.New("environment: context is required")
	}

	if err := ctx.Err(); err != nil {
		return playground.Runtime{}, err
	}

	description := h.description
	if description.Version != nil {
		version := *description.Version
		description.Version = &version
	}

	description.Modules = make([]playground.Module, len(h.description.Modules))
	for i, entry := range h.description.Modules {
		description.Modules[i] = entry
		if entry.Version != nil {
			version := *entry.Version
			description.Modules[i].Version = &version
		}
	}

	return description, nil
}

// Close starts native cleanup once and bounds this caller's wait. A timeout
// does not interrupt native Close, which has no context parameter. Later callers
// can wait again and observe its retained cleanup result.
func (h *Host) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("close host: context is required")
	}

	h.closeOnce.Do(func() {
		h.admission.Lock()
		h.closing = true
		h.admission.Unlock()

		go func() {
			h.active.Wait()
			h.closeErr = h.engine.Close()
			close(h.closeDone)
		}()
	})

	// Prefer the retained result when cleanup has already completed.
	select {
	case <-h.closeDone:
		return h.closeErr
	default:
	}

	select {
	case <-h.closeDone:
		return h.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newHost(registrations []registration, info *debug.BuildInfo) (*Host, error) {
	modules := make([]module.Module, len(registrations))
	metadata := make([]playground.Module, len(registrations))
	for i, entry := range registrations {
		modules[i] = entry.module
		metadata[i] = playground.Module{Name: entry.module.Name()}
		if entry.version != nil {
			version := *entry.version
			metadata[i].Version = &version
		}
	}

	// The profile label is owned beside the option that establishes it. Module
	// descriptions are produced from the same entries passed to construction.
	engine, err := ferret.New(ferret.WithStdlib(stdlib.Safe()), ferret.WithModules(modules...))
	if err != nil {
		return nil, err
	}

	return &Host{
		engine: engine,
		description: playground.Runtime{
			Name: "ferret", Version: dependencyVersion(info),
			StandardLibrary: "safe", Modules: metadata,
		},
		closeDone: make(chan struct{}),
	}, nil
}
