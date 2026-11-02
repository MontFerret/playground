package runtimehost

import (
	"bytes"
	"context"
)

type (
	formatTestContext struct {
		context.Context
		onErr func()
	}

	formatFailureWriter struct {
		buffer     bytes.Buffer
		writes     int
		failAt     int
		panicWrite bool
		failure    error
	}
)

func (c formatTestContext) Err() error {
	c.onErr()

	return c.Context.Err()
}

func (w *formatFailureWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		if w.panicWrite {
			panic(w.failure)
		}

		return 0, w.failure
	}

	return w.buffer.Write(p)
}
