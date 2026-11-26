package containerhost

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
)

const layoutFixture = `[
  {"ifindex":1,"ifname":"lo","link_type":"loopback","flags":["LOOPBACK"],"addr_info":[]},
  {"ifindex":2,"ifname":"tunl0","link_type":"ipip","flags":["NOARP"],"operstate":"DOWN","link":null,"addr_info":[],
   "linkinfo":{"info_kind":"ipip","info_data":{"local":"any","remote":"any"}}},
  {"ifindex":3,"ifname":"ip6tnl0","link_type":"tunnel6","flags":["NOARP"],"operstate":"DOWN","addr_info":[],
   "linkinfo":{"info_kind":"ip6tnl","info_data":{"local":"any","remote":"any"}}}
]`

func TestIsolatedLinks(t *testing.T) {
	interfaces := []net.Interface{{Index: 1, Name: "lo", Flags: net.FlagLoopback}, {Index: 2, Name: "tunl0"}, {Index: 3, Name: "ip6tnl0"}}
	for _, tc := range []struct {
		name, before, after string
	}{
		{"unknown kind", `"info_kind":"ipip"`, `"info_kind":"dummy"`},
		{"wrong hardware type", `"link_type":"tunnel6"`, `"link_type":"ether"`},
		{"up", `["NOARP"]`, `["UP","NOARP"]`},
		{"carrier", `["NOARP"]`, `["LOWER_UP"]`},
		{"address", `"link":null,"addr_info":[]`, `"addr_info":[{"family":"inet6"}]`},
		{"missing addresses", `"link":null,"addr_info":[]`, `"link":null`},
		{"attached device", `"link":null`, `"link_index":4`},
		{"peer", `"link":null`, `"link_netnsid":0`},
		{"master", `"link":null`, `"master":"br0"`},
		{"bound endpoint", `"remote":"any"`, `"remote":"192.0.2.1"`},
		{"tunnel underlay", `"local":"any"`, `"local":"any","link":"eth0"`},
		{"incomplete flags", `"flags":["NOARP"]`, `"flags":null`},
		{"ambiguous state", `"operstate":"DOWN"`, `"operstate":"UNKNOWN"`},
		{"duplicate record", `"ifindex":3`, `"ifindex":2`},
		{"loopback impersonation", `"link_type":"loopback"`, `"link_type":"ether"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var links []namespaceLink
			if err := json.Unmarshal([]byte(strings.Replace(layoutFixture, tc.before, tc.after, 1)), &links); err != nil {
				t.Fatal(err)
			}

			if isolatedLinks(interfaces, links) {
				t.Fatal("accepted invalid network layout")
			}
		})
	}

	var links []namespaceLink
	if err := json.Unmarshal([]byte(layoutFixture), &links); err != nil {
		t.Fatal(err)
	}

	if !isolatedLinks(interfaces, links) || !isolatedLinks(interfaces[:1], links[:1]) {
		t.Fatal("rejected an isolated layout")
	}

	if isolatedLinks(interfaces, links[:2]) || isolatedLinks(interfaces[1:], links[1:]) {
		t.Fatal("accepted incomplete inventory or missing loopback")
	}

	interfaces[2].Flags = net.FlagUp
	if isolatedLinks(interfaces, links) {
		t.Fatal("independent interface enumeration saw an active tunnel")
	}
}

func TestIsolatedRoutes(t *testing.T) {
	for _, tc := range []struct {
		route string
		ok    bool
	}{
		{`{"type":"local","dst":"127.0.0.1","dev":"lo"}`, true},
		{`{"type":"unreachable","dst":"::/0","dev":"lo"}`, true},
		{`{"type":"prohibit","dst":"default"}`, true},
		{`{"type":"local","dst":"192.0.2.1","table":250,"dev":"tunl0"}`, false},
		{`{"type":"unreachable","dst":"default","dev":"ip6tnl0"}`, false},
		{`{"dst":"default","dev":"lo"}`, false},
		{`{"dst":"::/0","dev":"lo"}`, false},
		{`{"dst":"192.0.2.1","dev":"lo","nhid":7}`, false},
		{`{"dst":"192.0.2.1","dev":"lo","nexthops":[{"dev":"eth0"}]}`, false},
		{`{"dst":"192.0.2.1","dev":"lo","encap":"ip"}`, false},
		{`{"dst":"192.0.2.1","gateway":"192.0.2.2","dev":"lo"}`, false},
		{`{"dst":"192.0.2.1","via":{"family":"inet6"},"dev":"lo"}`, false},
		{`{"dst":"192.0.2.1"}`, false}, {`{"dev":"lo"}`, false},
	} {
		var route namespaceRoute
		if err := json.Unmarshal([]byte(tc.route), &route); err != nil {
			t.Fatal(err)
		}

		if isolatedRoute(route) != tc.ok {
			t.Errorf("unexpected route admission for %s", tc.route)
		}
	}
}

func TestBrowserNetworkPrivileges(t *testing.T) {
	const status = "Uid:\t10001 10001 10001 10001\nCapPrm:\t0000000000000000\nCapEff:\t0000000000000000\n"
	if !restrictedBrowserStatus(status) {
		t.Fatal("rejected the unprivileged browser identity")
	}

	for _, failure := range []string{
		strings.Replace(status, "10001", "0", 1),
		strings.Replace(status, "CapPrm:\t0", "CapPrm:\t1", 1),
		strings.Replace(status, "CapEff:\t0000000000000000", "CapEff:\t0000000000001000", 1),
		strings.Replace(status, "CapEff:", "missing:", 1),
		strings.Replace(status, "CapPrm:\t0", "CapPrm:\t?", 1),
	} {
		if restrictedBrowserStatus(failure) {
			t.Fatal("accepted privileged or incomplete Chromium status")
		}
	}
}
