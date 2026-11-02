package containerhost

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func networkCheck(ctx context.Context) error {
	if os.Geteuid() != browserUID {
		return errors.New("network_probe_identity_failed")
	}

	dialer := &net.Dialer{Timeout: time.Second}
	for _, address := range []string{"127.0.0.1:9223", "127.0.0.1:8080", "169.254.169.254:80", "1.1.1.1:443", "[::1]:9223"} {
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			_ = conn.Close()
			return errors.New("direct_browser_connection_available")
		}
	}

	conn, err := dialer.DialContext(ctx, "udp", "169.254.169.254:53")
	if err == nil {
		_ = conn.Close()
		return errors.New("direct_browser_udp_available")
	}

	for _, path := range []string{stateDir + "/egress.sock", stateDir + "/cdp.sock", stateDir + "/check.sock"} {
		conn, err := dialer.DialContext(ctx, "unix", path)
		if err == nil {
			_ = conn.Close()
			return errors.New("browser_control_socket_available")
		}
	}

	// A positive control proves the browser can use the policy-enforcing proxy.
	conn, err = dialer.DialContext(ctx, "tcp4", "127.0.0.1:3128")
	if err != nil {
		return errors.New("browser_proxy_unavailable")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, err = fmt.Fprint(conn, "CONNECT forbidden.test:443 HTTP/1.1\r\nHost: forbidden.test:443\r\n\r\n")
	if err != nil {
		return errors.New("browser_proxy_unavailable")
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return errors.New("browser_proxy_unavailable")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		return errors.New("browser_proxy_policy_failed")
	}

	return nil
}
