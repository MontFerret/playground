package egress

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/netip"
	"os"
	"slices"
	"testing"
)

func TestDialMockeryRejectsOtherDestinations(t *testing.T) {
	for _, tc := range []struct{ network, address string }{
		{"tcp", "mockery.ferretlang.org:80"},
		{"tcp", "127.0.0.1:443"},
		{"tcp", "sub.mockery.ferretlang.org:443"},
		{"udp", MockeryHost + ":443"},
		{"tcp6", MockeryHost + ":443"},
	} {
		conn, err := DialMockery(t.Context(), tc.network, tc.address)
		if conn != nil || !errors.Is(err, errDestinationDenied) {
			t.Errorf("%s %s: got connection %v and error %v", tc.network, tc.address, conn != nil, err)
		}
	}
}

func TestChromiumManagedOriginsMatchProxy(t *testing.T) {
	data, err := os.ReadFile("../../deploy/browser/policy.json")
	if err != nil {
		t.Fatal(err)
	}

	var policy struct {
		URLBlocklist []string
		URLAllowlist []string
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal(err)
	}

	slices.Sort(policy.URLAllowlist)
	if !slices.Equal(policy.URLBlocklist, []string{"*"}) ||
		!slices.Equal(policy.URLAllowlist, []string{"about:blank", "https://." + MockeryHost + ":443"}) {
		t.Fatalf("unexpected managed URL policy: %+v", policy)
	}
}

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
	for _, target := range []string{"mockery.ferretlang.org:80", "127.0.0.1:9223", "sub.mockery.ferretlang.org:443", "169.254.169.254:443"} {
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
