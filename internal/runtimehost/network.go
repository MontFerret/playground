package runtimehost

import (
	"context"
	"errors"

	ferrethttp "github.com/MontFerret/ferret/v2/pkg/net/http"
)

type disabledHTTP struct{}

func (disabledHTTP) Do(context.Context, *ferrethttp.Request) (*ferrethttp.Response, error) {
	return nil, errors.New("native HTTP is unavailable")
}
