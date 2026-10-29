package httpapi

import (
	"context"
	"net/http"

	"github.com/MontFerret/playground/internal/playground"
)

type (
	testProvider struct {
		call func(context.Context) (playground.Runtime, error)
	}

	panickingWriter struct {
		http.ResponseWriter
	}
)

func (p testProvider) Environment(ctx context.Context) (playground.Runtime, error) {
	return p.call(ctx)
}

func (w panickingWriter) Write([]byte) (int, error) {
	panic("private writer failure")
}
