// Package containerhost owns the Linux container's processes and isolation.
package containerhost

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MontFerret/playground/internal/config"
)

// Run dispatches private container process roles. No role is request-selectable.
func Run(ctx context.Context, mode string) error {
	switch mode {
	case "", "probe":
		return runContainer(ctx, mode == "probe")
	case "namespace":
		return runNamespace(ctx)
	case "proxy":
		return runProxy(ctx)
	case "proxy-relay":
		return runRelay(ctx, "unix", stateDir+"/egress.sock")
	case "cdp-relay":
		return runRelay(ctx, "tcp4", "127.0.0.1:9223")
	case "control-relay":
		return runRelay(ctx, "unix", stateDir+"/cdp.sock")
	case "network-check":
		return networkCheck(ctx)
	case "check":
		return requestNetworkCheck(ctx)
	case "health":
		return checkAPI(ctx)
	default:
		return errors.New("invalid_container_role")
	}
}

const (
	stateDir     = "/tmp/playground"
	browserUID   = 10001
	helperUID    = 10002
	bootstrapUID = 65530
	apiUID       = 65532
)

func runContainer(ctx context.Context, probe bool) (result error) {
	if os.Geteuid() != 0 {
		return errors.New("bootstrap_identity_required")
	}

	env := containerEnvironment()
	cfg, err := config.Load(func(name string) (string, bool) {
		for _, entry := range env {
			if value, ok := strings.CutPrefix(entry, name+"="); ok {
				return value, true
			}
		}

		return "", false
	})
	if err != nil || cfg.ShutdownTimeout > 4*time.Second || cfg.CleanupTimeout > 3*time.Second {
		return errors.New("invalid_container_configuration")
	}

	// Nested user namespaces disable setgroups. Clear inherited groups before
	// spawning workers so their identity changes cannot retain a root group.
	if err := syscall.Setgroups(nil); err != nil {
		return errors.New("bootstrap_groups_failed")
	}

	if err := os.RemoveAll(stateDir); err != nil {
		return errors.New("state_initialization_failed")
	}

	if err := os.Mkdir(stateDir, 0755); err != nil {
		return errors.New("state_initialization_failed")
	}

	var workers []*process
	var api *process
	defer func() {
		started := time.Now()
		slog.Info("Container cleanup started")

		// A fresh absolute budget covers partial initialization and cancellation.
		cleanup, cancel := context.WithTimeout(context.Background(), 8500*time.Millisecond)
		defer cancel()
		if api != nil {
			drain, stop := context.WithTimeout(cleanup, 7*time.Second)
			api.stop(drain)
			stop()

			select {
			case <-api.done:
				if api.err != nil && result == nil {
					result = errors.New("api_shutdown_failed")
				}
			default:
				if result == nil {
					result = errors.New("api_shutdown_timeout")
				}
			}
		}

		var wg sync.WaitGroup
		for _, worker := range workers {
			wg.Go(func() { worker.stop(cleanup) })
		}
		wg.Wait()

		// The profile lives on ephemeral container storage. Do not recursively
		// walk an arbitrarily large browser cache on the shutdown critical path.
		for _, name := range []string{"egress.sock", "cdp.sock", "check.sock", "processes"} {
			_ = os.Remove(filepath.Join(stateDir, name))
		}

		slog.Info("Container cleanup finished", "elapsedMs", time.Since(started).Milliseconds())
	}()

	if err := os.Chown(stateDir, bootstrapUID, apiUID); err != nil {
		return errors.New("state_identity_failed")
	}

	proxy, err := unixListener(stateDir+"/egress.sock", helperUID, apiUID, 0660)
	if err != nil {
		return errors.New("proxy_listener_failed")
	}
	defer proxy.Close()

	worker, err := launch("proxy", apiUID, apiUID, []*os.File{proxy}, env, false)
	if err != nil {
		return errors.New("proxy_start_failed")
	}
	workers = append(workers, worker)
	_ = proxy.Close()

	control, err := unixListener(stateDir+"/cdp.sock", helperUID, apiUID, 0660)
	if err != nil {
		return errors.New("control_listener_failed")
	}
	defer control.Close()

	checks, err := unixListener(stateDir+"/check.sock", 0, 0, 0600)
	if err != nil {
		return errors.New("check_listener_failed")
	}
	defer checks.Close()

	ready, notify, err := os.Pipe()
	if err != nil {
		return errors.New("readiness_pipe_failed")
	}
	defer ready.Close()
	defer notify.Close()

	worker, err = launch("namespace", 0, 0, []*os.File{control, notify, checks}, env, true)
	if err != nil {
		return errors.New("namespace_start_failed")
	}
	workers = append(workers, worker)
	_ = control.Close()
	_ = notify.Close()
	_ = checks.Close()
	_ = ready.SetReadDeadline(time.Now().Add(40 * time.Second))
	stopRead := context.AfterFunc(ctx, func() { _ = ready.Close() })
	var reply [1]byte
	_, err = ready.Read(reply[:])
	stopRead()
	if err != nil || reply[0] != 1 {
		return errors.New("browser_isolation_unavailable")
	}

	if probe {
		return nil
	}

	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9222})
	if err != nil {
		return errors.New("control_relay_listener_failed")
	}
	file, err := listener.File()
	_ = listener.Close()
	if err != nil {
		return errors.New("control_relay_listener_failed")
	}
	defer file.Close()

	worker, err = launch("control-relay", apiUID, apiUID, []*os.File{file}, env, false)
	if err != nil {
		return errors.New("control_relay_start_failed")
	}
	workers = append(workers, worker)
	_ = file.Close()

	cmd := exec.Command("/usr/bin/playground")
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: identity(apiUID, apiUID)}
	api, err = startProcess(cmd)
	if err != nil {
		return errors.New("api_start_failed")
	}

	exited := make(chan struct{}, len(workers)+1)
	for _, child := range append(workers, api) {
		go func() { <-child.done; exited <- struct{}{} }()
	}

	select {
	case <-ctx.Done():
		return nil
	case <-exited:
		return errors.New("essential_process_exited")
	}
}

func launch(role string, uid, gid uint32, files []*os.File, env []string, namespace bool) (*process, error) {
	path, err := os.Executable()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(path, "container", role)
	cmd.Env = env
	cmd.ExtraFiles = files
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: identity(uid, gid)}
	if namespace {
		cmd.SysProcAttr.Cloneflags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET
		cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: bootstrapUID, Size: 1}, {ContainerID: browserUID, HostID: browserUID, Size: 2}, {ContainerID: apiUID, HostID: apiUID, Size: 1}}
		cmd.SysProcAttr.GidMappings = cmd.SysProcAttr.UidMappings
		cmd.SysProcAttr.GidMappingsEnableSetgroups = false
	}

	return startProcess(cmd)
}

func identity(uid, gid uint32) *syscall.Credential {
	return &syscall.Credential{Uid: uid, Gid: gid, NoSetGroups: true}
}

func unixListener(path string, uid, gid int, mode os.FileMode) (*os.File, error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()

	if err := os.Chmod(path, mode); err != nil {
		return nil, err
	}

	if err := os.Chown(path, uid, gid); err != nil {
		return nil, err
	}

	return listener.File()
}

func containerEnvironment() []string {
	env := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/tmp"}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PLAYGROUND_") && !strings.HasPrefix(entry, "PLAYGROUND_CDP_ORIGIN=") || strings.HasPrefix(entry, "PORT=") {
			env = append(env, entry)
		}
	}

	for name, value := range map[string]string{"PLAYGROUND_CDP_ORIGIN": "http://127.0.0.1:9222", "PLAYGROUND_SHUTDOWN_TIMEOUT": "4s", "PLAYGROUND_CLEANUP_TIMEOUT": "3s"} {
		if _, ok := os.LookupEnv(name); !ok || name == "PLAYGROUND_CDP_ORIGIN" {
			env = append(env, name+"="+value)
		}
	}

	if _, listen := os.LookupEnv("PLAYGROUND_LISTEN_ADDR"); !listen {
		if _, port := os.LookupEnv("PORT"); !port {
			env = append(env, "PLAYGROUND_LISTEN_ADDR=0.0.0.0:8080")
		}
	}

	return env
}

func processIDs(children map[string]*process) error {
	var text strings.Builder
	for name, child := range children {
		text.WriteString(name + " " + strconv.Itoa(child.cmd.Process.Pid) + "\n")
	}

	return os.WriteFile(filepath.Join(stateDir, "processes"), []byte(text.String()), 0600)
}
