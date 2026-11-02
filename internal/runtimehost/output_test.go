package runtimehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MontFerret/api"
	"github.com/MontFerret/ferret/v2/pkg/diagnostics"
	"github.com/MontFerret/ferret/v2/pkg/vm"

	"github.com/MontFerret/playground/internal/config"
	"github.com/MontFerret/playground/internal/httpapi"
	"github.com/MontFerret/playground/internal/playground"
)

func TestBoundedOutput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		length  int64 // -1 is unknown
		chunks  []string
		want    string
		wantErr error
	}{
		{"known exact", 6, []string{`"😀"`}, `"😀"`, nil},
		{"unknown exact split UTF-8", -1, []string{"\"\xf0", "", "\x9f\x98", "\x80\""}, `"😀"`, nil},
		{"known oversize", 7, nil, "", errOutputTooLarge},
		{"unknown oversize", -1, []string{"123", "456", "7"}, "", errOutputTooLarge},
		{"unknown null", -1, []string{"nu", "", "ll"}, "null", nil},
		{"absent", 0, nil, "", nil},
		{"unknown absent", -1, nil, "", nil},
		{"present empty", 0, []string{""}, "", playground.ErrInternal},
		{"invalid JSON", -1, []string{"12 34"}, "", playground.ErrInternal},
		{"invalid UTF-8", -1, []string{"\"\xff\""}, "", playground.ErrInternal},
		{"exceeds declared length", 1, []string{"1", "2"}, "", playground.ErrInternal},
		{"wrong length", 2, []string{"1"}, "", playground.ErrInternal},
		{"absent wrong length", 1, nil, "", playground.ErrInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			output := &fixtureOutput{meta: api.Metadata{ContentType: "application/json", Length: tc.length, LengthKnown: tc.length >= 0}}
			output.produce = func(ctx context.Context, consume api.Consumer) error {
				called = true
				for _, part := range tc.chunks {
					scratch := []byte(part)
					err := consume(ctx, scratch)
					clear(scratch)
					if err != nil {
						if output.closed {
							t.Error("output closed inside its callback")
						}

						return err
					}
				}

				return nil
			}
			data, _, err := settleOutput(t.Context(), output, 6)
			if !errors.Is(err, tc.wantErr) || string(data) != tc.want || (tc.want == "" && data != nil) || !output.closed || called != (tc.length <= 6) {
				t.Fatalf("data=%q error=%v called=%v closed=%v", data, err, called, output.closed)
			}
		})
	}
}

func TestOutputCanceledBeforeConsumption(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out := &fixtureOutput{produce: func(ctx context.Context, _ api.Consumer) error { return ctx.Err() }}
	data, _, err := settleOutput(ctx, out, 8)
	if !errors.Is(err, context.Canceled) || data != nil || !out.closed {
		t.Fatalf("canceled admission: data=%q closed=%v error=%v", data, out.closed, err)
	}
}

func TestHTTPWaitsForTerminalOutput(t *testing.T) {
	fqlErr := &vm.RuntimeError{Diagnostic: &diagnostics.Diagnostic{Kind: vm.UncaughtError, Message: "private terminal error"}}
	for _, tc := range []struct {
		name    string
		err     error
		payload string
		status  int
		code    string
	}{
		{"terminal runtime error", fqlErr, "null", 200, "runtime_error"},
		{"terminal service error", errors.New("private terminal error"), "null", 500, "internal_error"},
		{"invalid successful encoding", nil, "{", 500, "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delivered, finish := make(chan struct{}), make(chan struct{})
			out := &fixtureOutput{meta: api.Metadata{ContentType: "application/json"}, produce: func(ctx context.Context, consume api.Consumer) error {
				if err := consume(ctx, []byte(tc.payload)); err != nil {
					return err
				}

				close(delivered)
				<-finish

				return tc.err
			}}
			cfg, _ := config.Load(func(string) (string, bool) { return "", false })
			handler := httpapi.New(outputProvider{output: out}, playground.Service{}, cfg, nil)
			request := httptest.NewRequest("POST", "/api/v1/execute", strings.NewReader(`{"source":{"text":"RETURN 1"}}`))
			request.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			w := observedResponse{ResponseWriter: rec, committed: make(chan struct{})}
			done := make(chan struct{})
			go func() { defer close(done); handler.ServeHTTP(w, request) }()
			select {
			case <-delivered:
			case <-done:
				t.Fatalf("response completed before the callback: %s", rec.Body)
			}
			select {
			case <-w.committed:
				t.Error("HTTP committed while completion was still pending")
			default:
			}

			close(finish)
			<-done
			var fields map[string]json.RawMessage
			if json.Unmarshal(rec.Body.Bytes(), &fields) != nil || rec.Code != tc.status || fields["output"] != nil || !strings.Contains(rec.Body.String(), tc.code) || strings.Contains(rec.Body.String(), "private") || !out.closed {
				t.Fatalf("terminal outcome: %d %s closed=%v", rec.Code, rec.Body, out.closed)
			}
		})
	}
}

func BenchmarkFragmentedOutput(b *testing.B) {
	payload := bytes.Repeat([]byte{'a'}, 64<<10)
	payload[0], payload[len(payload)-1] = '"', '"'
	for _, known := range []bool{false, true} {
		name := "unknown"
		if known {
			name = "known"
		}

		b.Run(name, func(b *testing.B) {
			producer := &fixtureOutput{meta: api.Metadata{ContentType: "application/json", LengthKnown: known, Length: int64(len(payload))}, produce: func(ctx context.Context, consume api.Consumer) error {
				for start := 0; start < len(payload); start += 256 {
					if err := consume(ctx, payload[start:start+256]); err != nil {
						return err
					}
				}

				return nil
			}}
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			for b.Loop() {
				if _, err := consumeJSON(b.Context(), producer, len(payload)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestTerminalFailureClassification(t *testing.T) {
	runtimeErr := &vm.RuntimeError{Diagnostic: &diagnostics.Diagnostic{Kind: vm.UncaughtError}}
	panicErr := &vm.RuntimeError{Diagnostic: &diagnostics.Diagnostic{Kind: diagnostics.UnexpectedError, Cause: context.DeadlineExceeded}}
	hookErr := errors.New("hook failure")
	for _, tc := range []struct {
		name              string
		terminal, cleanup error
		failure           playground.ExecutionFailure
		internal          bool
	}{
		{"runtime and cleanup", errors.Join(runtimeErr, context.DeadlineExceeded), context.DeadlineExceeded, playground.RuntimeError, false},
		{"cleanup only", context.DeadlineExceeded, context.DeadlineExceeded, "", true},
		{"wrapped cleanup only", errors.Join(runtimeErr), runtimeErr, "", true},
		{"panic matching timeout", panicErr, nil, "", true},
		{"runtime and hook", errors.Join(runtimeErr, hookErr), nil, "", true},
		{"consumer overflow and cleanup", errors.Join(errOutputTooLarge, hookErr), hookErr, playground.OutputTooLarge, false},
		{"timeout", context.DeadlineExceeded, nil, playground.ExecutionTimeout, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &fixtureOutput{produce: func(context.Context, api.Consumer) error { return tc.terminal }, closeErr: tc.cleanup}
			data, failure, err := settleOutput(t.Context(), out, 6)
			if !out.closed || data != nil || failure != tc.failure || errors.Is(err, playground.ErrInternal) != tc.internal || !errors.Is(err, tc.terminal) || (tc.cleanup != nil && !errors.Is(err, tc.cleanup)) {
				t.Fatalf("classification: %s %s %v", data, failure, err)
			}
		})
	}
}
