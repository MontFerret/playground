package containerhost

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"syscall"
	"time"
)

// Reserve part of every stop deadline for SIGKILL and the sole Wait owner.
const reapTimeout = 250 * time.Millisecond

type process struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// startProcess owns the command and publishes its single retained wait result.
func startProcess(cmd *exec.Cmd) (*process, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	p := &process{cmd: cmd, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()

	return p, nil
}

// failure is called only after done closes. Even a successful essential-child
// exit is unexpected; retain only numeric status and the caller's fixed code.
func (p *process) failure(code string) error {
	signal := 0
	if status, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		signal = int(status.Signal())
	}
	slog.Error("Container child exited", "stage", code, "exitStatus", p.cmd.ProcessState.ExitCode(), "signal", signal)

	return errors.New(code)
}

func (p *process) stop(ctx context.Context) {
	select {
	case <-p.done:
		return
	default:
	}

	// Never spend the full deadline waiting for SIGTERM: force termination
	// early enough for Wait to reap the child before its owner can disappear.
	grace := ctx
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		grace, cancel = context.WithDeadline(ctx, deadline.Add(-reapTimeout))
		defer cancel()
	}

	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
		return
	case <-grace.Done():
	}

	select {
	case <-p.done:
		return
	default:
	}

	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	select {
	case <-p.done:
	case <-ctx.Done():
	}
}
