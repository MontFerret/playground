package runtimehost

import (
	"context"
	"errors"
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
	// budget. Page initialization belongs to the first CDP open in each run.
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
	html := sdk.NewModule("html", registerHTML)
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

func registerHTML(boot module.Bootstrap) error {
	library := boot.Host().Library()
	if err := sdk.RegisterFunctions(library, sdk.Func("DOCUMENT", openDocument)); err != nil {
		return err
	}

	// Register the public subset without installing the upstream session hook,
	// which would replace the request's drivers with bootstrap-owned drivers.
	return sdk.RegisterFunctions(library.Namespace("WEB").Namespace("HTML"),
		sdk.Func("OPEN", lib.Open),
		sdk.Func("PARSE", lib.Parse),
		sdk.Func("HOVER", lib.Hover),
		sdk.Func("SCROLL_BOTTOM", lib.ScrollBottom),
		sdk.Func("SCREENSHOT", lib.Screenshot),
	)
}

func openDocument(ctx context.Context, value runtime.Value) (runtime.Value, error) {
	return lib.Open(ctx, value, runtime.NewString(cdp.DriverName))
}
