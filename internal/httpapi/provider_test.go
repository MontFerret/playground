package httpapi

import (
	"context"
	"net/http"

	"github.com/MontFerret/playground/internal/playground"
)

type (
	unreadable   struct{}
	testProvider struct {
		call    func(context.Context) (playground.Runtime, error)
		analyze func(context.Context, playground.Source) (playground.AnalysisResult, error)
		format  func(context.Context, playground.Source, playground.FormatOptions) (playground.FormatResult, error)
		execute func(context.Context, playground.Source, playground.ExecuteOptions) (playground.ExecuteResult, error)
		compile func(context.Context, playground.Source, playground.CompileOptions) (playground.CompileResult, error)
	}

	panickingWriter struct {
		http.ResponseWriter
	}

	cancelingJSON struct {
		cancel context.CancelFunc
	}
)

func (p testProvider) Environment(ctx context.Context) (playground.Runtime, error) {
	return p.call(ctx)
}

func (p testProvider) Analyze(ctx context.Context, src playground.Source) (playground.AnalysisResult, error) {
	return p.analyze(ctx, src)
}

func (p testProvider) Compile(ctx context.Context, src playground.Source, options playground.CompileOptions) (playground.CompileResult, error) {
	return p.compile(ctx, src, options)
}

func (p testProvider) Format(ctx context.Context, src playground.Source, options playground.FormatOptions) (playground.FormatResult, error) {
	return p.format(ctx, src, options)
}

func (w panickingWriter) Write([]byte) (int, error) {
	panic("private writer failure")
}

func (v cancelingJSON) MarshalJSON() ([]byte, error) {
	v.cancel()

	return []byte(`{"status":"ok"}`), nil
}

func (p testProvider) Execute(ctx context.Context, src playground.Source, options playground.ExecuteOptions) (playground.ExecuteResult, error) {
	return p.execute(ctx, src, options)
}

func (unreadable) Read([]byte) (int, error) {
	panic("overloaded request body was read")
}

func (unreadable) Close() error {
	return nil
}
