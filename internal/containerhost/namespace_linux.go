package containerhost

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/MontFerret/playground/internal/browsercheck"
	"golang.org/x/sys/unix"
)

func runNamespace(ctx context.Context) (result error) {
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

	env := os.Environ() // Already filtered and validated by the outer supervisor.
	var proxy, cdp, browser, probe *process
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := stopProcesses(cleanup, probe, browser, cdp, proxy); err != nil && result == nil {
			result = err
		}
	}()

	file, err := tcpListener(3128)
	if err != nil {
		return errors.New("namespace_proxy_listener_failed")
	}
	defer file.Close()

	proxy, err = launch("proxy-relay", helperUID, apiUID, []*os.File{file}, env, false)
	if err != nil {
		return errors.New("namespace_proxy_start_failed")
	}
	_ = file.Close()

	control := os.NewFile(3, "control")
	defer control.Close()
	cdp, err = launch("cdp-relay", helperUID, apiUID, []*os.File{control}, env, false)
	if err != nil {
		return errors.New("namespace_control_start_failed")
	}
	_ = control.Close()

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
	browser, err = startProcess(cmd)
	if err != nil {
		return errors.New("chromium_start_failed")
	}

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
		case <-browser.done:
			return browser.failure("chromium_exited")
		case <-proxy.done:
			return proxy.failure("proxy_relay_exited")
		case <-cdp.done:
			return cdp.failure("cdp_relay_exited")
		case <-time.After(100 * time.Millisecond):
		}
	}

	probe, err = launch("network-check", browserUID, browserUID, nil, env, false)
	if err != nil {
		return errors.New("network_probe_start_failed")
	}
	select {
	case <-probe.done:
		if probe.err != nil {
			return errors.New("network_isolation_unavailable")
		}
	case <-startup.Done():
		return errors.New("network_probe_timeout")
	case <-browser.done:
		return browser.failure("chromium_exited")
	case <-proxy.done:
		return proxy.failure("proxy_relay_exited")
	case <-cdp.done:
		return cdp.failure("cdp_relay_exited")
	}
	probe = nil // The successful one-shot probe has already been reaped.
	slog.Info("Container isolation verified", "stage", "browser_connections")

	ready := os.NewFile(4, "readiness")
	_, err = ready.Write([]byte{1})
	_ = ready.Close()
	if err != nil {
		return errors.New("readiness_notification_failed")
	}

	select {
	case <-ctx.Done():
		return nil
	case <-browser.done:
		return browser.failure("chromium_exited")
	case <-proxy.done:
		return proxy.failure("proxy_relay_exited")
	case <-cdp.done:
		return cdp.failure("cdp_relay_exited")
	}
}

func protectControlDescriptors() {
	for _, fd := range []int{3, 4, 5} {
		unix.CloseOnExec(fd)
	}
}
