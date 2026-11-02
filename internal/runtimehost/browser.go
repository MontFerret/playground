package runtimehost

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/MontFerret/playground/internal/browser"
)

// NewWithBrowser composes the restricted HTML module using the private CDP service.
func NewWithBrowser(ctx context.Context, origin string, cleanup time.Duration) (*Host, error) {
	b, err := browser.New(ctx, origin, cleanup)
	if err != nil {
		return nil, err
	}

	info, _ := debug.ReadBuildInfo()
	version := moduleVersion(info, "github.com/MontFerret/contrib/modules/web/html")
	h, err := newHost([]registration{{module: b.Module(), version: version}}, info)
	if err != nil {
		return nil, err
	}
	h.browser = b

	return h, nil
}
