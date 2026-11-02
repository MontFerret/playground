package browser

import (
	"context"
	"github.com/MontFerret/contrib/modules/web/html/drivers"
	"testing"
)

func TestRejectDocumentBeforeCDP(t *testing.T) {
	for _, p := range []drivers.Params{
		{URL: "file:///etc/shadow"}, {URL: "http://relay:9222/json"}, {URL: "https://mockery.ferretlang.org.evil.test/"},
		{URL: "https://user@mockery.ferretlang.org"}, {URL: "https://mockery.ferretlang.org:9222"},
		{URL: "https://mockery.ferretlang.org", KeepCookies: true},
		{URL: "https://mockery.ferretlang.org", InitScript: &drivers.InitScript{}},
		{URL: "https://mockery.ferretlang.org", Headers: drivers.NewHTTPHeaders()},
	} {
		var run execution
		if _, err := run.Open(context.Background(), p); err == nil {
			t.Fatal("unexpectedly accepted document options")
		}
		if err := run.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
