package runtimehost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/MontFerret/ferret/v2/pkg/module"
	"github.com/MontFerret/ferret/v2/pkg/runtime"
	"github.com/MontFerret/ferret/v2/pkg/source"

	"github.com/MontFerret/playground/internal/playground"
)

func TestFormatPreservationAndIdempotence(t *testing.T) {
	host := toolingTestHost(t, nil)
	options := playground.FormatOptions{MaxFormattedSourceBytes: 1 << 20}
	for _, test := range []struct{ name, input, want string }{
		{"ASCII", "RETURN 1", "return 1"},
		{"BMP", "// é\nRETURN 1", "// é\nreturn 1"},
		{"supplementary", "// 😀\nRETURN 1", "// 😀\nreturn 1"},
		{"literal and comment", `RETURN ["😀", /* keep */ 1]`, "return [\n    \"😀\", /* keep */\n    1\n]"},
		{"CRLF", "// é😀\r\nRETURN \"😀\"", "// é😀\r\nreturn \"😀\""},
		{"multiline", "/* é\n * 😀\n */\nRETURN 1\n// 尾😀\n", "/* é\n * 😀\n */\nreturn 1\n// 尾😀\n"},
		{"unresolved names", `RETURN [MISSING::HOST(@x), missing]`, `return [MISSING::HOST(@x), missing]`},
		{"grouping", `RETURN (1 + 2) * 3`, `return (1 + 2) * 3`},
		{"newline and Unicode literal", `RETURN "hello\n😀"`, `return "hello\n😀"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := host.Format(context.Background(), playground.Source{Name: "../../label.fql", Text: test.input}, options)
			if err != nil || len(result.Diagnostics) != 0 || !utf8.ValidString(result.FormattedSource) || result.FormattedSource != test.want {
				t.Fatalf("format: %+v, %v; want %q", result, err, test.want)
			}

			again, err := host.Format(context.Background(), playground.Source{Text: result.FormattedSource}, options)
			if err != nil || again.FormattedSource != result.FormattedSource {
				t.Fatalf("not idempotent: %+v, %v", again, err)
			}
		})
	}
}

func TestFormatDoesNotCompileOrExecute(t *testing.T) {
	var functions, plans, sessions atomic.Int32
	host := toolingTestHost(t, func(boot module.Bootstrap) error {
		boot.Host().Library().Function().A0().Add("OBSERVE", func(context.Context) (runtime.Value, error) {
			functions.Add(1)

			return runtime.None, nil
		})
		boot.Hooks().Plan().BeforeCompile(func(context.Context) error {
			plans.Add(1)

			return nil
		})
		boot.Hooks().Session().BeforeRun(func(ctx context.Context) (context.Context, error) {
			sessions.Add(1)

			return ctx, nil
		})

		return nil
	})
	result, err := host.Format(context.Background(), playground.Source{Text: "RETURN [OBSERVE(), UNKNOWN(), @value, missing]"}, playground.FormatOptions{MaxFormattedSourceBytes: 1024})
	if err != nil || len(result.Diagnostics) != 0 || result.FormattedSource == "" || functions.Load() != 0 || plans.Load() != 0 || sessions.Load() != 0 {
		t.Fatalf("format invoked native execution or compilation: %+v %v, counts %d/%d/%d", result, err, functions.Load(), plans.Load(), sessions.Load())
	}
}

func TestFormatDiagnostics(t *testing.T) {
	host := toolingTestHost(t, nil)
	for _, input := range []string{"", "RETURN [1,", `RETURN "😀" +`, "// é😀\r\nRETURN \"中\" +", "RETURN [\"é😀\"\r\n"} {
		src := playground.Source{Name: "../../display.fql", Text: input}
		formatted, err := host.Format(context.Background(), src, playground.FormatOptions{MaxFormattedSourceBytes: 1024})
		if err != nil || len(formatted.Diagnostics) == 0 || formatted.FormattedSource != "" {
			t.Fatalf("expected source rejection: %+v %v", formatted, err)
		}

		analysis, err := host.Analyze(context.Background(), src)
		if err != nil || !reflect.DeepEqual(formatted.Diagnostics, analysis.Diagnostics) {
			t.Fatalf("diagnostics differ for %q: %+v %+v %v", input, formatted, analysis, err)
		}

		if input == `RETURN "😀" +` && (formatted.Diagnostics[0].Span.Start != 14 || formatted.Diagnostics[0].Span.End != 15) {
			t.Fatalf("formatter did not publish byte coordinates: %+v", formatted.Diagnostics[0])
		}
	}
}

func TestFormatLimitsAndCancellation(t *testing.T) {
	host := toolingTestHost(t, nil)
	src := playground.Source{Text: "RETURN 1"}
	options := playground.FormatOptions{MaxFormattedSourceBytes: len("return 1")}
	if result, err := host.Format(context.Background(), src, options); err != nil || result.FormattedSource != "return 1" {
		t.Fatalf("exact limit failed: %+v %v", result, err)
	}

	options.MaxFormattedSourceBytes--
	if result, err := host.Format(context.Background(), src, options); !errors.Is(err, playground.ErrResponseTooLarge) || !reflect.DeepEqual(result, playground.FormatResult{}) {
		t.Fatalf("partial output escaped: %+v %v", result, err)
	}

	options.MaxFormattedSourceBytes = 1024
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := host.Format(ctx, src, options); !errors.Is(err, context.Canceled) || result.FormattedSource != "" {
		t.Fatalf("pre-canceled format: %+v %v", result, err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	observed := formatTestContext{Context: ctx, onErr: func() {
		calls++
		// The first check precedes admission, then each writer call checks.
		// Cancel after the first native write has accepted "return".
		if calls == 3 {
			cancel()
		}
	}}
	if result, err := host.Format(observed, src, options); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, playground.FormatResult{}) {
		t.Fatalf("canceled partial output escaped: %+v %v", result, err)
	}

	if _, err := host.Format(nil, src, options); !errors.Is(err, playground.ErrInternal) {
		t.Fatalf("nil context: %v", err)
	}

	if _, err := host.Format(context.Background(), src, playground.FormatOptions{}); !errors.Is(err, playground.ErrInvalidOptions) {
		t.Fatalf("invalid limit: %v", err)
	}
}

func TestFormatWriterBoundsAndFirstFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := formatWriter{ctx: ctx, limit: len("é😀")}
	if n, err := out.Write([]byte("é")); n != len("é") || err != nil {
		t.Fatalf("byte write: %d %v", n, err)
	}

	if n, err := out.WriteString("😀"); n != len("😀") || err != nil {
		t.Fatalf("string write at limit: %d %v", n, err)
	}

	if n, err := out.WriteString("x"); n != 0 || err != playground.ErrResponseTooLarge {
		t.Fatalf("overflow: %d %v", n, err)
	}

	cancel()
	if n, err := out.Write(nil); n != 0 || err != playground.ErrResponseTooLarge || out.buffer.String() != "é😀" {
		t.Fatalf("first error or content changed: %d %v %q", n, err, out.buffer.String())
	}

	out = formatWriter{ctx: ctx, limit: 1024}
	if n, err := out.Write([]byte("private")); n != 0 || err != context.Canceled || out.buffer.Len() != 0 {
		t.Fatalf("canceled write accepted: %d %v", n, err)
	}
}

func TestFormatShutdownWaitsForAdmittedWork(t *testing.T) {
	entered, release, engineClosed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	host := toolingTestHost(t, func(boot module.Bootstrap) error {
		boot.Hooks().Engine().OnClose(func() error {
			close(engineClosed)

			return nil
		})

		return nil
	})
	checks := 0
	ctx := formatTestContext{Context: context.Background(), onErr: func() {
		checks++
		if checks == 2 {
			close(entered)
			<-release
		}
	}}
	finished := make(chan error, 1)
	options := playground.FormatOptions{MaxFormattedSourceBytes: 1024}
	go func() {
		_, err := host.Format(ctx, playground.Source{Text: "RETURN 1"}, options)
		finished <- err
	}()
	<-entered
	closeCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := host.Close(closeCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close wait: %v", err)
	}

	if _, err := host.Format(context.Background(), playground.Source{Text: "RETURN 2"}, options); !errors.Is(err, playground.ErrClosed) {
		t.Fatalf("closing host admitted work: %v", err)
	}

	select {
	case <-engineClosed:
		t.Fatal("engine closed before formatting settled")
	default:
	}

	unblock()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}

	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	<-engineClosed
}

func TestFormatConcurrentIsolation(t *testing.T) {
	host := toolingTestHost(t, nil)
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Go(func() {
			src := playground.Source{Name: fmt.Sprintf("file-%d", i), Text: fmt.Sprintf("// 😀 %d\nRETURN %d", i, i)}
			result, err := host.Format(context.Background(), src, playground.FormatOptions{MaxFormattedSourceBytes: 1024})
			if err != nil || result.FormattedSource != strings.Replace(src.Text, "RETURN", "return", 1) {
				t.Errorf("concurrent output: %+v %v", result, err)
			}

			src.Text = `RETURN "😀" +`
			result, err = host.Format(context.Background(), src, playground.FormatOptions{MaxFormattedSourceBytes: 1024})
			if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Source != src.Name || result.FormattedSource != "" {
				t.Errorf("concurrent diagnostics: %+v %v", result, err)
			}
		})
	}
	wg.Wait()
}

func TestNativeFormatterFailureGate(t *testing.T) {
	host := toolingTestHost(t, nil)
	failure := errors.New("controlled writer error")
	for _, panicWrite := range []bool{false, true} {
		for _, failAt := range []int{1, 2} {
			out := formatFailureWriter{failAt: failAt, panicWrite: panicWrite, failure: failure}
			err := host.formatter.Format(&out, source.NewAnonymous("RETURN 1"))
			if err == nil || (!panicWrite && err != failure) {
				t.Fatalf("writer failure was suppressed or changed: %v", err)
			}

			if failAt == 2 && out.buffer.String() != "return" {
				t.Fatalf("fault injection did not follow partial output: %q", out.buffer.String())
			}

			if diagnostics, convertedErr := nativeDiagnostics(err); !errors.Is(convertedErr, playground.ErrInternal) || len(diagnostics) != 0 {
				t.Fatalf("native failure leaked as source diagnostics: %+v %v", diagnostics, convertedErr)
			}
		}
	}

	var reused bytes.Buffer
	if err := host.formatter.Format(&reused, source.NewAnonymous("RETURN 1")); err != nil || reused.String() != "return 1" {
		t.Fatalf("formatter unusable after failures: %q %v", reused.String(), err)
	}
}

func TestFormatRecoveredPanicIsInternal(t *testing.T) {
	host := toolingTestHost(t, nil)
	calls := 0
	ctx := formatTestContext{Context: context.Background(), onErr: func() {
		calls++
		if calls == 3 {
			// Inject a panic after partial output through the writer's context.
			// Even a cancellation-shaped panic cause is an internal failure.
			panic(context.Canceled)
		}
	}}
	result, err := host.Format(ctx, playground.Source{Text: "RETURN 1"}, playground.FormatOptions{MaxFormattedSourceBytes: 1024})
	if !errors.Is(err, playground.ErrInternal) || !reflect.DeepEqual(result, playground.FormatResult{}) {
		t.Fatalf("internal failure or partial output escaped: %+v %v", result, err)
	}
}
