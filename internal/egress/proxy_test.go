package egress

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestProxyRejectsNonPublicAddresses(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.2.3.4", "169.254.169.254", "100.100.100.200", "168.63.129.16", "192.0.0.170", "192.168.1.12", "224.0.0.1", "::1", "fd00::2", "64:ff9b::a00:1", "255.255.255.255"} {
		if publicIPv4(netip.MustParseAddr(ip)) {
			t.Errorf("accepted %s", ip)
		}
	}
	if !publicIPv4(netip.MustParseAddr("185.199.108.153")) {
		t.Fatal("denied public address")
	}
}
func TestProxyRejectsOtherAuthoritiesAndMethods(t *testing.T) {
	for _, target := range []string{"mockery.ferretlang.org:80", "127.0.0.1:9222", "sub.mockery.ferretlang.org:443", "169.254.169.254:443"} {
		r := httptest.NewRequest("CONNECT", target, nil)
		w := httptest.NewRecorder()
		tunnel(w, r)
		if w.Code != 403 {
			t.Errorf("accepted %s", target)
		}
	}
	w := httptest.NewRecorder()
	tunnel(w, httptest.NewRequest("GET", "https://mockery.ferretlang.org", nil))
	if w.Code != 403 {
		t.Fatal("forwarded plain HTTP")
	}
}
