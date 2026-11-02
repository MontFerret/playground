package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MontFerret/ferret/v2/pkg/module"
	"github.com/MontFerret/ferret/v2/pkg/runtime"

	"github.com/MontFerret/playground/internal/playground"
)

func TestToolingCompilesOnceWithoutExecution(t *testing.T) {
	var invocations, compilations, closed, sessions atomic.Int32
	host := toolingTestHost(t, func(boot module.Bootstrap) error {
		boot.Host().Library().Function().A0().Add("OBSERVE", func(context.Context) (runtime.Value, error) {
			invocations.Add(1)

			return runtime.None, nil
		})
		boot.Hooks().Plan().BeforeCompile(func(context.Context) error {
			compilations.Add(1)

			return nil
		})
		boot.Hooks().Plan().OnClose(func() error {
			closed.Add(1)

			return nil
		})
		boot.Hooks().Session().BeforeRun(func(ctx context.Context) (context.Context, error) {
			sessions.Add(1)

			return ctx, nil
		})

		return nil
	})
	src := playground.Source{Name: "example.fql", Text: "RETURN [OBSERVE(), MISSING_FUNCTION(), @z, @a, @z]"}
	analysis, err := host.Analyze(context.Background(), src)
	if err != nil || len(analysis.Diagnostics) != 0 || closed.Load() != 1 {
		t.Fatalf("analysis: %+v, %v, closed=%d", analysis, err, closed.Load())
	}

	for i, level := range playground.OptimizationLevels() {
		result, err := host.Compile(context.Background(), src, playground.CompileOptions{
			Optimization: level, Include: playground.InspectionArtifacts(), MaxBytecodeBytes: 1 << 20,
		})
		if err != nil || len(result.Diagnostics) != 0 || result.CompileDuration < 0 {
			t.Fatalf("compile %s: %+v, %v", level, result, err)
		}

		if result.Artifacts.Bytecode == nil || result.Artifacts.Bytecode.Format != "text" || !strings.Contains(result.Artifacts.Bytecode.Text, "opt "+string(level)) {
			t.Fatalf("optimization not reflected in native disassembly: %+v", result.Artifacts.Bytecode)
		}

		if result.Artifacts.Parameters == nil || !reflect.DeepEqual(*result.Artifacts.Parameters, []string{"z", "a"}) {
			t.Fatalf("parameter metadata: %+v", result.Artifacts.Parameters)
		}

		if compilations.Load() != int32(i+2) || closed.Load() != int32(i+2) {
			t.Fatalf("compilation/cleanup counts: %d/%d", compilations.Load(), closed.Load())
		}
	}

	if invocations.Load() != 0 || sessions.Load() != 0 {
		t.Fatalf("tooling executed submitted code: functions=%d sessions=%d", invocations.Load(), sessions.Load())
	}
}

func TestToolingDiagnosticsAndArtifactPresence(t *testing.T) {
	host := toolingTestHost(t, nil)
	for _, text := range []string{"", "RETURN [1,", "RETURN missing", "RETURN LENGTH()"} {
		src := playground.Source{Name: "../../label.fql", Text: text}
		analysis, err := host.Analyze(context.Background(), src)
		if err != nil || len(analysis.Diagnostics) == 0 {
			t.Fatalf("expected source diagnostics for %q: %+v %v", text, analysis, err)
		}

		compiled, err := host.Compile(context.Background(), src, playground.CompileOptions{Include: playground.InspectionArtifacts(), MaxBytecodeBytes: 1 << 20})
		if err != nil || !reflect.DeepEqual(analysis.Diagnostics, compiled.Diagnostics) || compiled.Artifacts.Bytecode != nil || compiled.Artifacts.Parameters != nil {
			t.Fatalf("analysis/compile differ: %+v %+v %v", analysis, compiled, err)
		}
	}

	for _, include := range [][]playground.InspectionArtifact{nil, {}, {playground.ArtifactParameters}, {playground.ArtifactBytecode}} {
		result, err := host.Compile(context.Background(), playground.Source{Text: "RETURN 1"}, playground.CompileOptions{Include: include, MaxBytecodeBytes: 1 << 20})
		if err != nil || len(result.Diagnostics) != 0 {
			t.Fatalf("compile: %+v %v", result, err)
		}

		if len(include) == 0 && (result.Artifacts.Bytecode != nil || result.Artifacts.Parameters != nil) {
			t.Fatal("unrequested artifact generated")
		}

		if len(include) == 1 && include[0] == playground.ArtifactParameters && (result.Artifacts.Parameters == nil || *result.Artifacts.Parameters == nil || len(*result.Artifacts.Parameters) != 0 || result.Artifacts.Bytecode != nil) {
			t.Fatal("requested empty parameter metadata not preserved")
		}
	}
}

func TestToolingLimitsOptionsAndCleanupFailures(t *testing.T) {
	var closed atomic.Int32
	host := toolingTestHost(t, func(boot module.Bootstrap) error {
		boot.Hooks().Plan().OnClose(func() error { closed.Add(1); return nil })

		return nil
	})
	src := playground.Source{Text: "RETURN 1"}
	options := playground.CompileOptions{Include: []playground.InspectionArtifact{playground.ArtifactBytecode}, MaxBytecodeBytes: 1 << 20}
	first, err := host.Compile(context.Background(), src, options)
	if err != nil {
		t.Fatal(err)
	}

	options.MaxBytecodeBytes = len(first.Artifacts.Bytecode.Text)
	if _, err := host.Compile(context.Background(), src, options); err != nil {
		t.Fatalf("exact artifact limit rejected: %v", err)
	}

	options.MaxBytecodeBytes--
	result, err := host.Compile(context.Background(), src, options)
	if !errors.Is(err, playground.ErrResponseTooLarge) || !reflect.DeepEqual(result, playground.CompileResult{}) || closed.Load() != 3 {
		t.Fatalf("oversize leaked a plan or partial result: %+v %v closed=%d", result, err, closed.Load())
	}

	for _, options := range []playground.CompileOptions{
		{Optimization: "aggressive"}, {Include: []playground.InspectionArtifact{"ast"}},
		{Include: []playground.InspectionArtifact{"parameters", "parameters"}},
		{Include: []playground.InspectionArtifact{"bytecode"}},
	} {
		if _, err := host.Compile(context.Background(), src, options); !errors.Is(err, playground.ErrInvalidOptions) {
			t.Fatalf("unsupported options accepted: %+v %v", options, err)
		}
	}

	cleanupFailure := errors.New("private cleanup failure")
	failing := toolingTestHost(t, func(boot module.Bootstrap) error {
		boot.Hooks().Plan().OnClose(func() error { return cleanupFailure })

		return nil
	})
	result, err = failing.Compile(context.Background(), src, playground.CompileOptions{})
	if !errors.Is(err, playground.ErrInternal) || !errors.Is(err, cleanupFailure) || !reflect.DeepEqual(result, playground.CompileResult{}) {
		t.Fatalf("cleanup failure became success: %+v %v", result, err)
	}
}

func TestToolingCancellationAndHostClosure(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var engineClosed atomic.Bool
	host := toolingTestHost(t, func(boot module.Bootstrap) error {
		boot.Hooks().Plan().BeforeCompile(func(context.Context) error {
			close(entered)
			<-release

			return nil
		})
		boot.Hooks().Engine().OnClose(func() error { engineClosed.Store(true); return nil })

		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := host.Analyze(ctx, playground.Source{Text: "RETURN 1"})
		done <- err
	}()
	<-entered
	cancel()
	if err := host.Close(ctx); !errors.Is(err, context.Canceled) || engineClosed.Load() {
		t.Fatalf("engine closed before admitted work settled: %v closed=%v", err, engineClosed.Load())
	}

	if _, err := host.Analyze(context.Background(), playground.Source{Text: "RETURN 1"}); !errors.Is(err, playground.ErrClosed) {
		t.Fatalf("closing host admitted work: %v", err)
	}

	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}

	if err := host.Close(context.Background()); err != nil || !engineClosed.Load() {
		t.Fatalf("host did not settle: %v", err)
	}
}

func TestToolingCancellationDuringPlanCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var closed atomic.Int32
	host := toolingTestHost(t, func(boot module.Bootstrap) error {
		boot.Hooks().Plan().OnClose(func() error { closed.Add(1); cancel(); return nil })

		return nil
	})
	result, err := host.Compile(ctx, playground.Source{Text: "RETURN 1"}, playground.CompileOptions{Include: []playground.InspectionArtifact{"parameters"}})
	if !errors.Is(err, context.Canceled) || closed.Load() != 1 || !reflect.DeepEqual(result, playground.CompileResult{}) {
		t.Fatalf("cancellation/cleanup: %+v %v %d", result, err, closed.Load())
	}

	if _, err := host.Analyze(ctx, playground.Source{Text: "RETURN 1"}); !errors.Is(err, context.Canceled) || closed.Load() != 1 {
		t.Fatalf("pre-canceled request acquired a plan: %v", err)
	}
}

func TestConcurrentToolingIsolation(t *testing.T) {
	host := toolingTestHost(t, nil)
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Go(func() {
			level := playground.OptimizationLevels()[i%3]
			name := fmt.Sprintf("source-%d", i)
			parameter := fmt.Sprintf("parameter%d", i)
			result, err := host.Compile(context.Background(), playground.Source{Name: name, Text: "RETURN @" + parameter}, playground.CompileOptions{
				Optimization: level, Include: playground.InspectionArtifacts(), MaxBytecodeBytes: 1 << 20,
			})
			if err != nil || result.Artifacts.Parameters == nil || !reflect.DeepEqual(*result.Artifacts.Parameters, []string{parameter}) || !strings.Contains(result.Artifacts.Bytecode.Text, "opt "+string(level)) {
				t.Errorf("request %d leaked state: %+v %v", i, result, err)
			}

			invalid, err := host.Analyze(context.Background(), playground.Source{Name: name, Text: "RETURN unknown"})
			if err != nil || len(invalid.Diagnostics) != 1 || invalid.Diagnostics[0].Source != name {
				t.Errorf("diagnostic source leaked: %+v %v", invalid, err)
			}
		})
	}
	wg.Wait()
}

func toolingTestHost(t testing.TB, register func(module.Bootstrap) error) *Host {
	t.Helper()
	var registrations []registration
	if register != nil {
		registrations = []registration{{module: testModule{name: "tooling-test", register: register}}}
	}

	host, err := newHost(registrations, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := host.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})

	return host
}

func TestHostWaitsForPlanCleanup(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var engineClosed atomic.Bool
	host := toolingTestHost(t, func(boot module.Bootstrap) error {
		boot.Hooks().Plan().OnClose(func() error {
			close(entered)
			<-release

			return nil
		})
		boot.Hooks().Engine().OnClose(func() error { engineClosed.Store(true); return nil })

		return nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := host.Compile(context.Background(), playground.Source{Text: "RETURN 1"}, playground.CompileOptions{})
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := host.Close(ctx); !errors.Is(err, context.Canceled) || engineClosed.Load() {
		t.Errorf("engine overtook plan cleanup: %v", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if err := host.Close(context.Background()); err != nil || !engineClosed.Load() {
		t.Fatalf("host cleanup incomplete: %v", err)
	}
}
