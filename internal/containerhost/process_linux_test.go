package containerhost

import (
	"bufio"
	"context"
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

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	child.stop(ctx)
	select {
	case <-child.done:
	case <-time.After(2 * time.Second):
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
