package runtimehost

import (
	"runtime/debug"
	"testing"
)

func TestDependencyVersion(t *testing.T) {
	for _, test := range []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{name: "missing build info"},
		{name: "unrelated", info: &debug.BuildInfo{Deps: []*debug.Module{{Path: "unrelated", Version: "v9"}}}},
		{name: "pinned", info: &debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/MontFerret/ferret/v2", Version: "v2.0.0-alpha.58"}}}, want: "v2.0.0-alpha.58"},
		{name: "local replacement", info: &debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/MontFerret/ferret/v2", Version: "v2", Replace: &debug.Module{Path: "/private/local"}}}}},
		{name: "versioned replacement", info: &debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/MontFerret/ferret/v2", Version: "v2", Replace: &debug.Module{Path: "fork", Version: "v2.1.0"}}}}, want: "v2.1.0"},
		{name: "development", info: &debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/MontFerret/ferret/v2", Version: "(devel)"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := dependencyVersion(test.info)
			if test.want == "" {
				if got != nil {
					t.Fatalf("unknown version must be null: %q", *got)
				}

				return
			}

			if got == nil || string(*got) != test.want {
				t.Fatalf("version = %v, want %q", got, test.want)
			}
		})
	}
}
