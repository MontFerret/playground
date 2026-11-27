package runtimehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MontFerret/ferret/v2/pkg/module"
	"golang.org/x/net/websocket"

	"github.com/MontFerret/playground/internal/playground"
)

func TestHostedHTMLSurface(t *testing.T) {
	var probes atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		http.NotFound(w, r)
	}))
	defer endpoint.Close()

	host := toolingTestHost(t, func(boot module.Bootstrap) error {
		library := boot.Host().Library()
		before := library.Size()
		if err := registerHTML(boot); err != nil {
			return err
		}

		if library.Size() != before+6 || !library.Function().A1().Has("DOCUMENT") {
			t.Fatal("expected a unary DOCUMENT and five namespaced registrations")
		}

		html := library.Namespace("WEB").Namespace("HTML").Function()
		for _, name := range []string{"OPEN", "PARSE", "HOVER", "SCROLL_BOTTOM", "SCREENSHOT"} {
			if !html.Var().Has(name) {
				t.Errorf("missing variadic HTML registration %s", name)
			}
		}

		return nil
	})
	host.cdpOrigin = endpoint.URL
	host.browserCleanup = time.Second

	for _, query := range []string{
		`DOCUMENT()`, `DOCUMENT(42)`, `DOCUMENT("https://mockery.ferretlang.org", {driver: "cdp"})`,
		`DOCUMENT("http://mockery.ferretlang.org/")`, `DOCUMENT("https://mockery.ferretlang.org:444/")`,
		`DOCUMENT("https://user@mockery.ferretlang.org/")`, `DOCUMENT("https://mockery.ferretlang.org.example/")`,
		`DOCUMENT("about:blank")`, `DOCUMENT("data:text/html,hello")`, `DOCUMENT("file:///etc/hosts")`,
		`DOWNLOAD("https://mockery.ferretlang.org")`, `PARSE("<p>example</p>")`, `CLICK(NONE, "p")`,
		`WEB::HTML::DOWNLOAD("https://mockery.ferretlang.org")`,
		`WEB::HTML::OPEN()`,
		`WEB::HTML::OPEN(42, {driver: "cdp"})`,
		`WEB::HTML::OPEN("https://mockery.ferretlang.org", {driver: "cdp", timeout: 0})`,
		`WEB::HTML::OPEN("https://mockery.ferretlang.org", {driver: "cdp", address: "http://localhost"})`,
		`WEB::HTML::OPEN("https://mockery.ferretlang.org", {driver: "cdp", proxy: "http://localhost"})`,
		`WEB::HTML::OPEN("https://mockery.ferretlang.org", {driver: "third-party"})`,
		`WEB::HTML::OPEN("https://mockery.ferretlang.org", {driver: "cdp", keepCookies: true})`,
		`WEB::HTML::OPEN("https://user@mockery.ferretlang.org/")`,
		`WEB::HTML::OPEN("https://example.test/")`,
		`WEB::HTML::OPEN("file:///etc/hosts", {driver: "cdp"})`,
		`WEB::HTML::PARSE(42)`,
		`WEB::HTML::PARSE("<p>example</p>", {driver: "cdp"})`,
	} {
		result, err := host.Execute(t.Context(), playground.Source{Text: "RETURN " + query}, playground.ExecuteOptions{MaxOutputBytes: 1024})
		if result.Failure != playground.RuntimeError || result.Output != nil {
			t.Fatalf("%s: expected a program failure, got %+v, %v", query, result, err)
		}
	}

	if count := probes.Load(); count != 0 {
		t.Fatalf("rejected inputs contacted CDP %d times", count)
	}

	query := playground.Source{Text: `LET page = web::html::parse(@html)
RETURN (query one "p" in page using css).textContent`}
	result, err := host.Execute(t.Context(), query, playground.ExecuteOptions{
		Params:         map[string]json.RawMessage{"html": json.RawMessage(`"<p>Fixture</p>"`)},
		MaxOutputBytes: 1024,
	})
	if err != nil || result.Failure != "" || string(result.Output) != `"Fixture"` || probes.Load() != 0 {
		t.Fatalf("offline memory parse: got %+v, %v with %d browser probes", result, err, probes.Load())
	}
}

func TestBrowserProbeFailsBeforeHostCreation(t *testing.T) {
	var advertised atomic.Bool
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": "ws://" + r.Host + "/unavailable"})
			return
		}

		advertised.Store(r.URL.Path == "/unavailable")
		http.NotFound(w, r)
	}))
	defer endpoint.Close()

	host, err := NewWithBrowser(t.Context(), endpoint.URL, time.Second)
	if host != nil || err == nil || !advertised.Load() {
		t.Fatalf("unavailable endpoint produced host %v, error %v", host, err)
	}
}

func TestDriverCleanupAfterHandledDocumentFailure(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		var disposals atomic.Int32
		// Fail page initialization after the native driver acquires its context.
		// FQL handles the open error, so only Driver.Close can reject its output.
		peer := websocket.Server{Handler: func(conn *websocket.Conn) {
			for {
				var call struct {
					ID     int
					Method string
				}
				if err := websocket.JSON.Receive(conn, &call); err != nil {
					return
				}

				reply := map[string]any{"id": call.ID}
				switch call.Method {
				case "Fetch.enable", "Fetch.disable":
					reply["result"] = struct{}{}
				case "Target.createBrowserContext":
					reply["result"] = map[string]string{"browserContextId": "owned"}
				case "Target.disposeBrowserContext":
					disposals.Add(1)
					if !cleanupFails {
						reply["result"] = struct{}{}
					}
				case "Target.getBrowserContexts":
					reply["result"] = map[string][]string{"browserContextIds": {"owned"}}
				}

				if reply["result"] == nil {
					reply["error"] = map[string]any{"code": -32000, "message": "fixture failure"}
				}

				if err := websocket.JSON.Send(conn, reply); err != nil {
					return
				}
			}
		}}
		mux := http.NewServeMux()
		mux.Handle("/browser", peer)
		mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": "ws://" + r.Host + "/browser"})
		})
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)
		host := toolingTestHost(t, registerHTML)
		host.cdpOrigin = server.URL
		host.browserCleanup = time.Second
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		result, err := host.Execute(ctx, playground.Source{Text: `LET page = DOCUMENT("https://mockery.ferretlang.org") ON ERROR RETURN NONE RETURN 7`}, playground.ExecuteOptions{MaxOutputBytes: 1024})
		cancel()
		if disposals.Load() == 0 || result.Failure != "" {
			t.Fatalf("expected handled open failure and context cleanup: %+v, %v", result, err)
		}

		if cleanupFails {
			if !errors.Is(err, playground.ErrInternal) || result.Output != nil {
				t.Fatalf("driver cleanup failure delivered output: %+v, %v", result, err)
			}

			select {
			case <-host.BrowserFailed():
			default:
				t.Fatal("unconfirmed disposal did not request shutdown")
			}

			_, err := host.Execute(ctx, playground.Source{Text: "RETURN 1"}, playground.ExecuteOptions{MaxOutputBytes: 1024})
			if !errors.Is(err, playground.ErrClosed) {
				t.Fatalf("terminal browser failure did not close admission: %v", err)
			}
		} else if err != nil || string(result.Output) != "7" {
			t.Fatalf("successful rollback discarded the handled result: %+v, %v", result, err)
		}
	}
}

func TestDriverCleanupSharesDeadlineAcrossPages(t *testing.T) {
	var next atomic.Int32
	var stalledAttempts atomic.Int32
	responsive := make(chan struct{}, 1)
	ownerClosed := make(chan struct{}, 1)
	peer := websocket.Server{Handler: func(conn *websocket.Conn) {
		var owned string
		defer func() {
			if owned == "context-1" {
				ownerClosed <- struct{}{}
			}
		}()
		for {
			var call struct {
				ID     int
				Method string
				Params struct {
					BrowserContextID string `json:"browserContextId"`
					DisposeOnDetach  bool   `json:"disposeOnDetach"`
				}
			}
			if err := websocket.JSON.Receive(conn, &call); err != nil {
				return
			}

			reply := map[string]any{"id": call.ID}
			switch call.Method {
			case "Fetch.enable":
				reply["result"] = struct{}{}
			case "Target.createBrowserContext":
				if !call.Params.DisposeOnDetach {
					t.Error("driver omitted disposal on owner disconnect")
				}

				owned = fmt.Sprintf("context-%d", next.Add(1))
				reply["result"] = map[string]string{"browserContextId": owned}
			case "Target.disposeBrowserContext":
				if call.Params.BrowserContextID == "context-1" {
					stalledAttempts.Add(1)

					continue
				}

				responsive <- struct{}{}
				reply["result"] = struct{}{}
			default:
				reply["error"] = map[string]any{"code": -32000, "message": "fixture target creation failed"}
			}

			if err := websocket.JSON.Send(conn, reply); err != nil {
				return
			}
		}
	}}
	mux := http.NewServeMux()
	mux.Handle("/browser", peer)
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": "ws://" + r.Host + "/browser"})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	host := toolingTestHost(t, registerHTML)
	host.cdpOrigin = server.URL
	host.browserCleanup = 400 * time.Millisecond

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	source := playground.Source{Text: `
		LET first = DOCUMENT("https://mockery.ferretlang.org/first") ON ERROR RETURN NONE
		LET second = DOCUMENT("https://mockery.ferretlang.org/second") ON ERROR RETURN NONE
		RETURN 7`}
	started := time.Now()
	result, err := host.Execute(ctx, source, playground.ExecuteOptions{MaxOutputBytes: 1024})
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("execution exceeded shared cleanup budget: %s", elapsed)
	}

	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, playground.ErrInternal) || result.Output != nil {
		t.Fatalf("stalled disposal did not fail closed: %+v, %v", result, err)
	}

	if next.Load() != 2 || stalledAttempts.Load() != 2 {
		t.Fatalf("expected both contexts and one fallback, got %d creations and %d attempts", next.Load(), stalledAttempts.Load())
	}

	select {
	case <-responsive:
	default:
		t.Fatal("stalled page prevented disposal of the responsive page")
	}

	select {
	case <-ownerClosed:
	case <-ctx.Done():
		t.Fatal("stalled context owner did not disconnect")
	}
}

func TestHostShutdownCapsAlreadyRunningBrowserCleanup(t *testing.T) {
	host := toolingTestHost(t, registerHTML)
	host.browserCleanup = time.Hour
	if err := host.admit(); err != nil {
		t.Fatal(err)
	}

	browser, stopBrowser := host.browserCleanupContext()
	defer stopBrowser()
	ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- host.Close(ctx) }()

	select {
	case <-browser.Done():
	case <-ctx.Done():
		host.active.Done()
		t.Fatal("host did not shorten the running browser cleanup")
	}

	select {
	case <-host.closeDone:
		host.active.Done()
		t.Fatal("engine closed while browser admission was still active")
	default:
	}

	host.active.Done()
	if err := <-done; err != nil {
		t.Fatalf("shutdown did not leave time to close the engine: %v", err)
	}
}
