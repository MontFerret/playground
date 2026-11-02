package main

import (
	"runtime/debug"

	"github.com/MontFerret/playground/internal/playground"
)

// releaseVersion is set by GoReleaser for the service binary.
var releaseVersion string

func serviceIdentity() playground.Service {
	info, _ := debug.ReadBuildInfo()

	identity := buildIdentity(info)
	if releaseVersion != "" {
		identity.Version = &releaseVersion
	}

	return identity
}

func buildIdentity(info *debug.BuildInfo) playground.Service {
	identity := playground.Service{Name: "ferret-playground"}
	if info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version := info.Main.Version
		identity.Version = &version
	}

	return identity
}
