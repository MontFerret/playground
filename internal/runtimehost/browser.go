package runtimehost

import (
	"context"
	"errors"
	"net/url"
	"runtime/debug"
	"time"

	"github.com/MontFerret/contrib/modules/web/html/drivers/cdp"
	"github.com/MontFerret/contrib/modules/web/html/lib"
	"github.com/MontFerret/ferret/v2/pkg/module"
	"github.com/MontFerret/ferret/v2/pkg/runtime"
	"github.com/MontFerret/ferret/v2/pkg/sdk"
	"github.com/mafredri/cdp/devtool"
	"github.com/mafredri/cdp/rpcc"

	"github.com/MontFerret/playground/internal/egress"
)

// NewWithBrowser verifies the private browser before advertising the HTML module.
func NewWithBrowser(ctx context.Context, origin string, cleanup time.Duration) (*Host, error) {
	if ctx == nil || origin == "" || cleanup <= 0 {
		return nil, errors.New("browser host requires a probe context, endpoint and cleanup timeout")
	}

	// Verify the advertised WebSocket through the relay within the startup
	// budget. Page initialization belongs to the first DOCUMENT in each run.
	chrome, err := devtool.New(origin).Version(ctx)
	if err != nil {
		return nil, err
	}

	conn, err := rpcc.DialContext(ctx, chrome.WebSocketDebuggerURL)
	if err != nil {
		return nil, err
	}

	if err := errors.Join(conn.Close(), ctx.Err()); err != nil {
		return nil, err
	}

	info, _ := debug.ReadBuildInfo()
	version := moduleVersion(info, "github.com/MontFerret/contrib/modules/web/html")
	html := sdk.NewModule("html", registerDocument)
	h, err := newHost([]registration{{module: html, version: version}}, info)
	if err != nil {
		return nil, err
	}

	h.cdpOrigin = origin
	h.browserCleanup = cleanup

	return h, nil
}

func newBrowserDriver(origin string) *cdp.Driver {
	return cdp.New(cdp.WithAddress(origin), cdp.WithAllowedOrigins("https://"+egress.MockeryHost), cdp.WithManagedPageCleanup())
}

func registerDocument(boot module.Bootstrap) error {
	return sdk.RegisterFunctions(boot.Host().Library(), sdk.Func("DOCUMENT", openDocument))
}

func openDocument(ctx context.Context, value runtime.Value) (runtime.Value, error) {
	raw, err := runtime.CastString(value)
	if err != nil {
		return runtime.None, err
	}

	// Fetch interception covers HTTP(S). Check the submitted URL before Chrome
	// can interpret a local scheme or consume embedded credentials.
	u, err := url.Parse(raw.String())
	if err != nil || u.Scheme != "https" || u.User != nil ||
		(u.Host != egress.MockeryHost && u.Host != egress.MockeryHost+":443") {
		return runtime.None, errors.New("document access is unavailable")
	}

	return lib.Open(ctx, raw)
}
