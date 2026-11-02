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

func (h testHost) Analyze(context.Context, playground.Source) (playground.AnalysisResult, error) {
	return playground.AnalysisResult{}, nil
}

func (h testHost) Compile(context.Context, playground.Source, playground.CompileOptions) (playground.CompileResult, error) {
	return playground.CompileResult{}, nil
}

func (h testHost) Format(context.Context, playground.Source, playground.FormatOptions) (playground.FormatResult, error) {
	return playground.FormatResult{}, nil
}
