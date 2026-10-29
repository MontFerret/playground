package main

import (
	"context"

	"github.com/MontFerret/playground/internal/playground"
)

type testHost struct {
	close func(context.Context) error
}

func (h testHost) Environment(context.Context) (playground.Runtime, error) {
	return playground.Runtime{Name: "ferret", StandardLibrary: "safe", Modules: []playground.Module{}}, nil
}

func (h testHost) Close(ctx context.Context) error {
	return h.close(ctx)
}
