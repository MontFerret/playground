package runtimehost

import (
	"context"
	"time"
)

// BrowserFailed signals that unresolved browser cleanup stopped host admission.
func (h *Host) BrowserFailed() <-chan struct{} {
	return h.browserFailed
}

func (h *Host) failBrowser() {
	h.admission.Lock()
	defer h.admission.Unlock()

	h.closing = true
	h.browserFailureOnce.Do(func() { close(h.browserFailed) })
}

// Called under admission when host cleanup starts. Reserve half of the
// remaining budget for admitted work to settle and the engine to close.
func (h *Host) limitBrowserShutdown(ctx context.Context) func() {
	deadline, ok := ctx.Deadline()
	if ok {
		now := time.Now()
		h.browserDeadline = now.Add(deadline.Sub(now) / 2)
	}

	browserCtx := ctx
	cancel := func() {}
	if ok {
		browserCtx, cancel = context.WithDeadline(ctx, h.browserDeadline)
	}

	stop := context.AfterFunc(browserCtx, func() {
		h.cancelBrowserShutdown(context.Cause(browserCtx))
	})

	return func() {
		stop()
		cancel()
	}
}

func (h *Host) browserCleanupContext() (context.Context, context.CancelFunc) {
	h.admission.Lock()
	cutoff := h.browserDeadline
	h.admission.Unlock()

	deadline := time.Now().Add(h.browserCleanup)
	if !cutoff.IsZero() && cutoff.Before(deadline) {
		deadline = cutoff
	}

	budget, stopBudget := context.WithDeadline(context.Background(), deadline)
	ctx, cancel := context.WithCancelCause(budget)
	stopShutdown := context.AfterFunc(h.browserShutdown, func() {
		cancel(context.Cause(h.browserShutdown))
	})

	if cause := context.Cause(h.browserShutdown); cause != nil {
		cancel(cause)
	}

	return ctx, func() {
		stopShutdown()
		stopBudget()
		cancel(context.Canceled)
	}
}
