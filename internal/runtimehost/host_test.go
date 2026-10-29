package runtimehost

import (
	"context"
	"errors"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MontFerret/api"
	"github.com/MontFerret/ferret/v2/pkg/module"
)

func TestInitializedEnvironment(t *testing.T) {
	var registered atomic.Bool
	version := "module-1"
	info := &debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/MontFerret/ferret/v2", Version: "v2.0.0-alpha.58"}}}
	host, err := newHost([]registration{{module: testModule{name: "observer", register: func(boot module.Bootstrap) error {
		functions, err := boot.Host().Library().Build()
		if err != nil {
			return err
		}

		if functions.Size() == 0 || functions.Has("IO::FS::READ") || functions.Has("IO::NET::HTTP::GET") {
			return errors.New("safe library selection was not installed")
		}

		registered.Store(true)

		return nil
	}}, version: &version}}, info)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := host.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	version = "mutated input"
	description, err := host.Environment(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if !registered.Load() || description.Name != "ferret" || description.StandardLibrary != "safe" || description.Version == nil || *description.Version != api.Version("v2.0.0-alpha.58") || len(description.Modules) != 1 || description.Modules[0].Name != "observer" || *description.Modules[0].Version != "module-1" {
		t.Fatalf("incorrect initialized metadata: %+v", description)
	}

	*description.Version = "corrupt"
	description.Modules[0].Name = "corrupt"
	*description.Modules[0].Version = "corrupt"
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			copy, err := host.Environment(context.Background())
			if err != nil || *copy.Version != "v2.0.0-alpha.58" || copy.Modules[0].Name != "observer" || *copy.Modules[0].Version != "module-1" {
				t.Errorf("snapshot changed: %+v, %v", copy, err)
			}

			*copy.Version = "caller owned"
			*copy.Modules[0].Version = "caller owned"
		})
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := host.Environment(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not preserved: %v", err)
	}

	if _, err := host.Environment(nil); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestDefaultHostHasNoOptionalModules(t *testing.T) {
	host, err := New()
	if err != nil {
		t.Fatal(err)
	}

	defer host.Close(context.Background())
	description, err := host.Environment(context.Background())
	if err != nil || description.Modules == nil || len(description.Modules) != 0 {
		t.Fatalf("default modules: %+v, %v", description.Modules, err)
	}
}

func TestConstructionRollback(t *testing.T) {
	for _, phase := range []string{"register", "init"} {
		t.Run(phase, func(t *testing.T) {
			failure, cleanupFailure := errors.New("initialization failure"), errors.New("cleanup failure")
			var closed atomic.Int32
			first := testModule{name: "owned", register: func(boot module.Bootstrap) error {
				boot.Hooks().Engine().OnClose(func() error {
					closed.Add(1)

					return cleanupFailure
				})

				return nil
			}}
			second := testModule{name: "failure", register: func(boot module.Bootstrap) error {
				if phase == "register" {
					return failure
				}

				boot.Hooks().Engine().OnInit(func() error { return failure })

				return nil
			}}
			host, err := newHost([]registration{{module: first}, {module: second}}, nil)
			if host != nil || !errors.Is(err, failure) || !errors.Is(err, cleanupFailure) || closed.Load() != 1 {
				t.Fatalf("rollback lost ownership or error: host=%v err=%v closed=%d", host, err, closed.Load())
			}
		})
	}
}

func TestBoundedCleanupRetainsResult(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var closed atomic.Int32
	failure := errors.New("close failure")
	host, err := newHost([]registration{{module: testModule{name: "blocking", register: func(boot module.Bootstrap) error {
		boot.Hooks().Engine().OnClose(func() error {
			closed.Add(1)
			close(started)
			<-release

			return failure
		})

		return nil
	}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := host.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup was not bounded: %v", err)
	}

	<-started
	close(release)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := host.Close(context.Background()); !errors.Is(err, failure) {
				t.Errorf("cleanup result lost: %v", err)
			}
		})
	}
	wg.Wait()
	if closed.Load() != 1 {
		t.Fatalf("cleanup ran %d times", closed.Load())
	}

	if err := host.Close(nil); err == nil {
		t.Fatal("nil close context accepted")
	}
}
