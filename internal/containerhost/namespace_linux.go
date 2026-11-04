package containerhost

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MontFerret/playground/internal/browsercheck"
	"golang.org/x/sys/unix"
)

func runNamespace(ctx context.Context) error {
	// These descriptors were explicitly inherited from the supervisor. Only
	// ExtraFiles may pass them onward; Chromium must not inherit control FDs.
	protectControlDescriptors()

	if err := isolateNetwork(); err != nil {
		return err
	}
	slog.Info("Container isolation verified", "stage", "network_policy")

	if err := os.Mkdir(stateDir+"/profile", 0700); err != nil {
		return errors.New("profile_initialization_failed")
	}

	if err := os.Chown(stateDir+"/profile", browserUID, browserUID); err != nil {
		return errors.New("profile_identity_failed")
	}

	children := make(map[string]*process)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var wg sync.WaitGroup
		for _, child := range children {
			wg.Go(func() { child.stop(cleanup) })
		}
		wg.Wait()
	}()

	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3128})
	if err != nil {
		return errors.New("namespace_proxy_listener_failed")
	}
	file, err := listener.File()
	_ = listener.Close()
	if err != nil {
		return errors.New("namespace_proxy_listener_failed")
	}
	defer file.Close()

	child, err := launch("proxy-relay", helperUID, apiUID, []*os.File{file}, containerEnvironment(), false)
	if err != nil {
		return errors.New("namespace_proxy_start_failed")
	}
	children["proxy-relay"] = child
	_ = file.Close()

	control := os.NewFile(3, "control")
	child, err = launch("cdp-relay", helperUID, apiUID, []*os.File{control}, containerEnvironment(), false)
	_ = control.Close()
	if err != nil {
		return errors.New("namespace_control_start_failed")
	}
	children["cdp-relay"] = child

	args := []string{"--headless=new", "--user-data-dir=" + stateDir + "/profile", "--no-first-run",
		"--disable-dev-shm-usage",
		"--remote-debugging-port=9223", "--remote-debugging-address=127.0.0.1",
		"--proxy-server=http://127.0.0.1:3128", "--proxy-bypass-list=<-loopback>",
		"--disable-quic", "--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
		"--disable-extensions", "--disable-background-networking", "--disable-component-update",
		"--disable-background-timer-throttling", "--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding", "--disable-gpu", "--disable-breakpad", "--no-default-browser-check"}
	// The integration fixture supplies a public-key pin, never a production bypass.
	if pin := os.Getenv("PLAYGROUND_FIXTURE_SPKI"); pin != "" {
		if len(pin) != 44 || strings.ContainsAny(pin, " \t\r\n,") {
			return errors.New("invalid_fixture_pin")
		}
		args = append(args, "--ignore-certificate-errors-spki-list="+pin, "--disable-popup-blocking")
	}
	args = append(args, "about:blank")
	cmd := exec.Command("chrome", args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + stateDir + "/profile"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: identity(browserUID, browserUID)}
	child, err = startProcess(cmd)
	if err != nil {
		return errors.New("chromium_start_failed")
	}
	children["browser"] = child

	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		check, stop := context.WithTimeout(startup, 2*time.Second)
		healthy := browsercheck.Healthy(check)
		stop()
		if healthy {
			slog.Info("Container isolation verified", "stage", "renderer_sandbox")

			break
		}

		select {
		case <-startup.Done():
			return errors.New("renderer_sandbox_unavailable")
		case <-children["browser"].done:
			return errors.New("chromium_exited")
		case <-time.After(100 * time.Millisecond):
		}
	}

	check, err := launch("network-check", browserUID, browserUID, nil, containerEnvironment(), false)
	if err != nil {
		return errors.New("network_probe_start_failed")
	}
	children["network-check"] = check
	select {
	case <-check.done:
		if check.err != nil {
			return errors.New("network_isolation_unavailable")
		}
	case <-startup.Done():
		return errors.New("network_probe_timeout")
	}
	delete(children, "network-check")
	slog.Info("Container isolation verified", "stage", "browser_connections")

	ready := os.NewFile(4, "readiness")
	_, err = ready.Write([]byte{1})
	_ = ready.Close()
	if err != nil {
		return errors.New("readiness_notification_failed")
	}

	exited := make(chan struct{}, len(children))
	for _, child := range children {
		go func() { <-child.done; exited <- struct{}{} }()
	}

	select {
	case <-ctx.Done():
		return nil
	case <-exited:
		return errors.New("browser_process_exited")
	}
}

func protectControlDescriptors() {
	for _, fd := range []int{3, 4, 5} {
		unix.CloseOnExec(fd)
	}
}
