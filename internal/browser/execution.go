package browser

import (
	"context"
	"errors"
	"net/url"
	"sync"

	"github.com/MontFerret/contrib/modules/web/html/drivers"
	htmlcdp "github.com/MontFerret/contrib/modules/web/html/drivers/cdp"
	"github.com/MontFerret/contrib/modules/web/html/drivers/cdp/session"
	"github.com/mafredri/cdp"
	"github.com/mafredri/cdp/protocol/page"
	"github.com/mafredri/cdp/protocol/target"
	"github.com/mafredri/cdp/rpcc"
)

type execution struct {
	browser     *Browser
	ctx         context.Context
	mu          sync.Mutex
	owner       *rpcc.Conn
	created     *target.CreateBrowserContextReply
	pages       []*htmlcdp.HTMLPage
	connections []*rpcc.Conn
	closed      bool
}

func (e *execution) Name() string { return "cdp" }

func (e *execution) Parse(context.Context, drivers.ParseParams) (drivers.HTMLPage, error) {
	return nil, errors.New("HTML parsing is unavailable")
}

func (e *execution) Open(ctx context.Context, p drivers.Params) (drivers.HTMLPage, error) {
	u, err := url.Parse(p.URL)
	if err != nil || u.Scheme != "https" || u.Host != "mockery.ferretlang.org" && u.Host != "mockery.ferretlang.org:443" || u.User != nil ||
		p.KeepCookies || p.Cookies != nil || p.Headers != nil || p.InitScript != nil || p.Ignore != nil || p.Viewport != nil || p.UserAgent != "" || p.Charset != "" {
		return nil, errors.New("document access is unavailable")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("browser execution is closed")
	}

	if e.owner == nil {
		conn, err := rpcc.DialContext(ctx, e.browser.endpoint, rpcc.WithWriteBufferSize(htmlcdp.DefaultBufferSize), rpcc.WithCompression())
		if err != nil {
			return nil, err
		}
		reply, err := cdp.NewClient(conn).Target.CreateBrowserContext(ctx, target.NewCreateBrowserContextArgs().SetDisposeOnDetach(true))
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		e.owner, e.created = conn, reply
	}

	// Each upstream page manager owns its connection. The run has a separate
	// owner connection so closing one page cannot dispose its siblings.
	conn, err := rpcc.DialContext(ctx, e.browser.endpoint, rpcc.WithWriteBufferSize(htmlcdp.DefaultBufferSize), rpcc.WithCompression())
	if err != nil {
		return nil, err
	}
	client := cdp.NewClient(conn)
	t, err := client.Target.CreateTarget(ctx, target.NewCreateTargetArgs("about:blank").SetBrowserContextID(e.created.BrowserContextID))
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	manager, err := session.New(ctx, conn, client, t.TargetID)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	// Upstream page cleanup uses background CDP calls. Disconnect them at the
	// run deadline even if cleanup is already in progress.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	p.Viewport = &drivers.Viewport{Width: 1600, Height: 900}
	loaded, err := e.loadPage(ctx, manager, p)
	if err != nil {
		stop()
		_ = conn.Close()
		_ = manager.Close()
		return nil, err
	}
	stop()
	context.AfterFunc(e.ctx, func() { _ = conn.Close() })
	e.connections = append(e.connections, conn)
	e.pages = append(e.pages, loaded)

	return loaded, nil
}

// Close disposes all targets, including popups, before releasing admission.
func (e *execution) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.owner == nil {
		e.closed = true
		return nil
	}
	e.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), e.browser.cleanup)
	defer cancel()
	for _, conn := range e.connections {
		context.AfterFunc(ctx, func() { _ = conn.Close() })
	}
	var err error
	for _, p := range e.pages {
		err = errors.Join(err, p.Close())
	}
	err = errors.Join(err, cdp.NewClient(e.owner).Target.DisposeBrowserContext(ctx, target.NewDisposeBrowserContextArgs(e.created.BrowserContextID)))
	// disposeOnDetach also covers an expired cleanup deadline.
	return errors.Join(err, e.owner.Close())
}

// loadPage subscribes before navigation. The pinned HTML module subscribes
// after Page.navigate, which can miss a fast fixture's navigation event.
// Hand the completed document to its supported existing-page loader.
func (e *execution) loadPage(ctx context.Context, manager *session.Manager, params drivers.Params) (*htmlcdp.HTMLPage, error) {
	client := manager.Root().CDP

	if err := client.Page.Enable(ctx); err != nil {
		return nil, err
	}
	if err := client.Page.SetLifecycleEventsEnabled(ctx, page.NewSetLifecycleEventsEnabledArgs(true)); err != nil {
		return nil, err
	}
	events, err := client.Page.LifecycleEvent(ctx)
	if err != nil {
		return nil, err
	}
	defer events.Close()
	navigation, err := client.Page.Navigate(ctx, page.NewNavigateArgs(params.URL))
	if err != nil {
		return nil, err
	}
	if navigation.ErrorText != nil {
		return nil, errors.New("document navigation failed")
	}
	for {
		event, err := events.Recv()
		if err != nil {
			return nil, err
		}
		if event.Name == "load" && event.FrameID == navigation.FrameID && navigation.LoaderID != nil && event.LoaderID == *navigation.LoaderID {
			break
		}
	}
	params.URL = htmlcdp.BlankPageURL

	return htmlcdp.LoadHTMLPage(ctx, manager, params)
}
