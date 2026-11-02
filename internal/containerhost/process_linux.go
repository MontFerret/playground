package containerhost

import (
	"context"
	"os/exec"
	"syscall"
)

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

func (p *process) stop(ctx context.Context) {
	select {
	case <-p.done:
		return
	default:
	}

	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
		return
	case <-ctx.Done():
	}

	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)

	// The caller's deadline also bounds waiting for a killed process.
	select {
	case <-p.done:
	case <-ctx.Done():
	}
}
