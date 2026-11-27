// Package egress enforces the hosted HTML destination policy.
package egress

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// MockeryHost is the sole destination for hosted HTML and browser egress.
const MockeryHost = "mockery.ferretlang.org"

var errDestinationDenied = errors.New("destination denied")

// Handler permits only CONNECT to the public Mockery HTTPS origin.
func Handler() http.Handler {
	return http.HandlerFunc(tunnel)
}

// DialMockery resolves the sole HTTPS destination and dials a validated public
// IPv4 address. The caller is responsible for TLS with the original hostname.
func DialMockery(ctx context.Context, network, address string) (net.Conn, error) {
	if (network != "tcp" && network != "tcp4") || address != MockeryHost+":443" {
		return nil, errDestinationDenied
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", MockeryHost)
	if err != nil {
		return nil, err
	}

	if len(ips) == 0 {
		return nil, errors.New("Mockery has no IPv4 addresses")
	}

	for _, ip := range ips {
		if !publicIPv4(ip.Unmap()) {
			return nil, errDestinationDenied
		}
	}

	// Dial the exact address checked above. A second name lookup here would
	// allow the resolver to rebind an approved destination.
	return (&net.Dialer{}).DialContext(ctx, "tcp4", net.JoinHostPort(ips[0].Unmap().String(), "443"))
}

func tunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect || r.RequestURI != MockeryHost+":443" || r.Host != r.RequestURI {
		http.Error(w, "denied", http.StatusForbidden)

		return
	}

	upstream, err := DialMockery(r.Context(), "tcp4", r.RequestURI)
	if err != nil {
		if errors.Is(err, errDestinationDenied) {
			http.Error(w, "denied", http.StatusForbidden)
		} else {
			http.Error(w, "unavailable", http.StatusBadGateway)
		}

		return
	}
	defer upstream.Close()
	client, buffer, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	deadline := time.Now().Add(2 * time.Minute)
	_ = upstream.SetDeadline(deadline)
	_ = client.SetDeadline(deadline)
	if _, err = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buffer.Flush(); err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, buffer.Reader)
		_ = upstream.Close()
		close(done)
	}()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	<-done
}

var restricted = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("168.63.129.16/32"),
}

func publicIPv4(ip netip.Addr) bool {
	if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, prefix := range restricted {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
