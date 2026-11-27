package runtimehost

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/MontFerret/contrib/modules/web/html/drivers"
	"github.com/MontFerret/contrib/modules/web/html/drivers/cdp"
	"github.com/MontFerret/contrib/modules/web/html/drivers/memory"

	"github.com/MontFerret/playground/internal/egress"
)

type hostedHTMLDriver struct {
	drivers.Driver
	transport *http.Transport
}

func newHostedMemoryDriver() *hostedHTMLDriver {
	transport := &http.Transport{DialContext: egress.DialMockery, TLSHandshakeTimeout: 5 * time.Second}

	return &hostedHTMLDriver{
		// One HTTP attempt lets the execution handle retries explicitly and
		// returns destination denials without the client's exponential backoff.
		Driver: memory.New(
			memory.WithAllowedOrigins("https://"+egress.MockeryHost),
			memory.WithCustomTransport(transport),
			memory.WithMaxRetries(1),
		),
		transport: transport,
	}
}

func (d *hostedHTMLDriver) Open(ctx context.Context, params drivers.Params) (drivers.HTMLPage, error) {
	// Check the initial URL before the driver can consume credentials or
	// interpret a non-HTTP scheme outside its request interception.
	u, err := url.Parse(params.URL)
	if err != nil || u.Scheme != "https" || u.User != nil ||
		(u.Host != egress.MockeryHost && u.Host != egress.MockeryHost+":443") {
		return nil, errors.New("document access is unavailable")
	}

	if d.Name() == cdp.DriverName && params.KeepCookies {
		return nil, errors.New("shared browser cookies are unavailable")
	}

	return d.Driver.Open(ctx, params)
}

func (d *hostedHTMLDriver) Parse(ctx context.Context, params drivers.ParseParams) (drivers.HTMLPage, error) {
	if d.Name() == cdp.DriverName {
		// Native CDP Parse creates a target in the shared default context.
		return nil, errors.New("parsing HTML in the browser is unavailable")
	}

	return d.Driver.Parse(ctx, params)
}

func (d *hostedHTMLDriver) Close() error {
	if d.transport != nil {
		defer d.transport.CloseIdleConnections()
	}

	return d.Driver.Close()
}
