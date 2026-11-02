package runtimehost

import (
	"context"
	"strings"

	"github.com/MontFerret/playground/internal/playground"
)

// formatWriter retains at most limit bytes and latches the first failure.
// The native formatter may keep traversing after a writer error; subsequent
// writes must neither accept output nor replace the original failure.
type formatWriter struct {
	ctx    context.Context
	buffer strings.Builder
	limit  int
	err    error
}

func (w *formatWriter) Write(p []byte) (int, error) {
	if err := w.check(len(p)); err != nil {
		return 0, err
	}

	return w.buffer.Write(p)
}

func (w *formatWriter) WriteString(s string) (int, error) {
	if err := w.check(len(s)); err != nil {
		return 0, err
	}

	return w.buffer.WriteString(s)
}

func (w *formatWriter) check(size int) error {
	if w.err != nil {
		return w.err
	}

	if err := w.ctx.Err(); err != nil {
		w.err = err
	} else if size > w.limit-w.buffer.Len() {
		w.err = playground.ErrResponseTooLarge
	}

	return w.err
}
