package runtimehost

import (
	"context"
	"net/http"

	"github.com/MontFerret/api"

	"github.com/MontFerret/playground/internal/playground"
)

type (
	fixtureOutput struct {
		meta     api.Metadata
		produce  func(context.Context, api.Consumer) error
		closeErr error
		closed   bool
	}

	// Only replace the output producer. Use the production consumer and router.
	outputProvider struct {
		*Host
		output api.Output
	}

	observedResponse struct {
		http.ResponseWriter
		committed chan struct{}
	}
)

func (o *fixtureOutput) Metadata() api.Metadata {
	return o.meta
}

func (o *fixtureOutput) Consume(ctx context.Context, consume api.Consumer) error {
	return o.produce(ctx, consume)
}

func (o *fixtureOutput) Collect(context.Context) (*api.Content, error) {
	panic("unbounded collection")
}

func (o *fixtureOutput) Close() error {
	o.closed = true

	return o.closeErr
}

func (p outputProvider) Execute(ctx context.Context, _ playground.Source, opts playground.ExecuteOptions) (playground.ExecuteResult, error) {
	data, failure, err := settleOutput(ctx, p.output, opts.MaxOutputBytes)

	return playground.ExecuteResult{Output: data, Failure: failure}, err
}

func (w observedResponse) WriteHeader(code int) {
	close(w.committed)
	w.ResponseWriter.WriteHeader(code)
}

func (w observedResponse) Write(data []byte) (int, error) {
	select {
	case <-w.committed:
	default:
		w.WriteHeader(200)
	}

	return w.ResponseWriter.Write(data)
}
