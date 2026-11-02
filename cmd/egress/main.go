// Command egress is the packaged browser's single-destination CONNECT proxy.
package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"time"
)

const demoHost = "mockery.ferretlang.org"

func main() {
	server := http.Server{Addr: ":3128", Handler: http.HandlerFunc(tunnel), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	if server.ListenAndServe() != nil {
		os.Exit(1)
	}
}

func tunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect || r.RequestURI != demoHost+":443" || r.Host != r.RequestURI {
		http.Error(w, "denied", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", demoHost)
	if err != nil || len(ips) == 0 {
		http.Error(w, "unavailable", 502)
		return
	}
	for _, ip := range ips {
		if !publicIPv4(ip.Unmap()) {
			http.Error(w, "denied", 403)
			return
		}
	}
	// Dial the exact address checked above. A second name lookup here would
	// allow the resolver to rebind an approved destination.
	upstream, err := (&net.Dialer{}).DialContext(ctx, "tcp4", net.JoinHostPort(ips[0].Unmap().String(), "443"))
	if err != nil {
		http.Error(w, "unavailable", 502)
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
