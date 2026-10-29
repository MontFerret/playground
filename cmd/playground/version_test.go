package main

import (
	"runtime/debug"
	"testing"
)

func TestServiceBuildVersion(t *testing.T) {
	for _, version := range []string{"", "(devel)", "v1.2.3", "v0.0.0-20261029153822-82aed91b9140+dirty"} {
		identity := buildIdentity(&debug.BuildInfo{Main: debug.Module{Version: version}})
		if identity.Name != "ferret-playground" {
			t.Fatal("incorrect service identity")
		}

		if version == "" || version == "(devel)" {
			if identity.Version != nil {
				t.Fatalf("unavailable version must be null: %v", identity.Version)
			}

			continue
		}

		if identity.Version == nil || *identity.Version != version {
			t.Fatalf("available build version was changed: %v", identity.Version)
		}
	}

	if identity := buildIdentity(nil); identity.Version != nil {
		t.Fatal("missing build metadata must produce null")
	}
}
