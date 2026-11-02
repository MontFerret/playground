// Package browser exposes the hosted subset of the HTML module.
package browser

import (
	"context"
	"errors"
	"net"
	"net/url"
	"time"

	"github.com/MontFerret/contrib/modules/web/html/drivers"
	"github.com/MontFerret/contrib/modules/web/html/lib"
	"github.com/MontFerret/ferret/v2/pkg/module"
	"github.com/MontFerret/ferret/v2/pkg/sdk"
	"github.com/mafredri/cdp/devtool"
	"github.com/mafredri/cdp/rpcc"
)

// Browser borrows a private CDP service for the lifetime of the host.
type Browser struct {
	endpoint string
	cleanup  time.Duration
}

// New resolves the private browser before discovery can advertise its module.
func New(ctx context.Context, origin string, cleanup time.Duration) (*Browser, error) {
	version, err := devtool.New(origin).Version(ctx)
	if err != nil {
		return nil, errors.New("browser unavailable")
	}

	endpoint, err := url.Parse(version.WebSocketDebuggerURL)
	if err != nil || endpoint.Scheme != "ws" || endpoint.Host == "" {
		return nil, errors.New("invalid browser endpoint")
	}

	// Chrome reports its loopback endpoint. The configured private relay is
	// the reachable authority from the API container.
	control, err := url.Parse(origin)
	if err != nil {
		return nil, errors.New("invalid browser origin")
	}

	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", control.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("browser relay unavailable")
	}

	port := control.Port()
	if port == "" {
		port = "80"
	}

	endpoint.Host = net.JoinHostPort(addresses[0].Unmap().String(), port)
	conn, err := rpcc.DialContext(ctx, endpoint.String())
	if err != nil {
		return nil, errors.New("browser control unavailable")
	}
	_ = conn.Close()

	return &Browser{endpoint: endpoint.String(), cleanup: cleanup}, nil
}

// Module registers only the upstream DOCUMENT function; the driver container is run-local.
func (b *Browser) Module() module.Module {
	return sdk.NewModule("html", func(boot module.Bootstrap) error {
		return sdk.RegisterFunctions(boot.Host().Library(), sdk.Func("DOCUMENT", lib.Open))
	})
}

// Execution supplies a new driver to this run. CDP resources are acquired on first open.
func (b *Browser) Execution(ctx context.Context) (context.Context, *execution) {
	run := &execution{browser: b, ctx: ctx}
	container := drivers.NewContainer()
	// Registering the single, known driver in an empty container cannot collide.
	_ = container.Register(run)
	container.SetDefault(run.Name())
	return container.WithContext(ctx), run
}
