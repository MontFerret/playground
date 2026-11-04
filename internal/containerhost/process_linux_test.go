package containerhost

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestControlDescriptorsAreCloseOnExec(t *testing.T) {
	if os.Getenv("PLAYGROUND_DESCRIPTOR_HELPER") == "1" {
		protectControlDescriptors()
		err := syscall.Exec("/bin/sh", []string{"sh", "-c", "test ! -e /proc/self/fd/3 && test ! -e /proc/self/fd/4 && test ! -e /proc/self/fd/5"}, os.Environ())
		t.Fatal(err)
	}

	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	file, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cmd := exec.Command(path, "-test.run=^TestControlDescriptorsAreCloseOnExec$")
	cmd.Env = append(os.Environ(), "PLAYGROUND_DESCRIPTOR_HELPER=1")
	cmd.ExtraFiles = []*os.File{file, file, file}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("control descriptors survived exec: %v, %s", err, output)
	}
}

func TestStopKillsAndReapsProcessGroup(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "trap '' TERM; echo ready; while :; do sleep 1; done")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	child, err := startProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		child.stop(cleanup)
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("child readiness: %q, %v", line, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	child.stop(ctx)
	select {
	case <-child.done:
	default:
		t.Fatal("child was not reaped")
	}

	if child.err == nil {
		t.Fatal("forced termination lost its wait result")
	}

	// A repeated close must use the retained result, not signal a reused PID.
	child.stop(context.Background())
}

func TestContainerEnvironmentOwnsControlAndFiltersCredentials(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "private-test-value")
	t.Setenv("PLAYGROUND_CDP_ORIGIN", "http://untrusted.invalid")
	t.Setenv("PLAYGROUND_ALLOWED_ORIGINS", "https://ferretlang.org")
	env := strings.Join(containerEnvironment(), "\n")
	if strings.Contains(env, "private-test-value") || strings.Contains(env, "untrusted.invalid") {
		t.Fatal("container inherited a credential or external control origin")
	}

	if !strings.Contains(env, "PLAYGROUND_CDP_ORIGIN=http://127.0.0.1:9222") ||
		!strings.Contains(env, "PLAYGROUND_ALLOWED_ORIGINS=https://ferretlang.org") {
		t.Fatal("container lost its enforced control endpoint or operator CORS setting")
	}
}

func TestAwaitBrowserRequiresReadiness(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply []byte
		code  string
	}{
		{"ready", []byte{1}, ""},
		{"invalid", []byte{0}, "browser_isolation_unavailable"},
		{"closed", nil, "browser_isolation_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ready, notify, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer ready.Close()
			defer notify.Close()
			if _, err := notify.Write(tc.reply); err != nil {
				t.Fatal(err)
			}
			_ = notify.Close()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			pending := &process{done: make(chan struct{})}
			err = awaitBrowser(ctx, ready, pending, pending)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != tc.code {
				t.Fatalf("readiness error: %v", err)
			}

			if _, err := ready.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatal("readiness reader remained open")
			}
		})
	}
}

func TestAwaitBrowserCancellationClosesReader(t *testing.T) {
	ready, notify, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer ready.Close()
	defer notify.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pending := &process{done: make(chan struct{})}
	if err := awaitBrowser(ctx, ready, pending, pending); !errors.Is(err, context.Canceled) {
		t.Fatalf("startup cancellation: %v", err)
	}

	if _, err := ready.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("canceled startup left its reader open")
	}
}

func TestAwaitBrowserDetectsEssentialExit(t *testing.T) {
	for _, role := range []string{"proxy", "namespace"} {
		t.Run(role, func(t *testing.T) {
			child, err := startProcess(exec.Command("/bin/sh", "-c", "exit 23"))
			if err != nil {
				t.Fatal(err)
			}
			<-child.done
			ready, notify, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer ready.Close()
			defer notify.Close()
			pending := &process{done: make(chan struct{})}
			proxy, namespace := child, pending
			if role == "namespace" {
				proxy, namespace = pending, child
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := awaitBrowser(ctx, ready, proxy, namespace); err == nil || err.Error() != role+"_process_exited" {
				t.Fatalf("startup child exit: %v", err)
			}
		})
	}
}

func TestProcessFailureKeepsOnlyFixedCodeAndNumericStatus(t *testing.T) {
	child, err := startProcess(exec.Command("/bin/sh", "-c", "exit 23", "private-child-value"))
	if err != nil {
		t.Fatal(err)
	}
	<-child.done
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	if err := child.failure("proxy_process_exited"); err.Error() != "proxy_process_exited" {
		t.Fatal("child error lost its fixed code")
	}

	if !strings.Contains(output.String(), `"exitStatus":23`) || strings.Contains(output.String(), "private-child-value") {
		t.Fatal("child failure lost its status or leaked command data")
	}
}

func TestStopProcessesRollsBackPartialStartup(t *testing.T) {
	child, err := startProcess(exec.Command("/bin/sh", "-c", "exec sleep 30"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		child.stop(ctx)
	})
	cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := stopProcesses(cleanup, nil, child, nil); err != nil {
		t.Fatal(err)
	}

	select {
	case <-child.done:
	default:
		t.Fatal("rollback returned before reaping its child")
	}
}
