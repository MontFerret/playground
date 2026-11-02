//go:build !linux

// Package containerhost owns the Linux container's processes and isolation.
package containerhost

import (
	"context"
	"errors"
)

// Run requires Linux namespaces. Core development uses cmd/playground directly.
func Run(context.Context, string) error {
	return errors.New("linux_container_required")
}
