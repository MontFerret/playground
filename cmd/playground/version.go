package main

import (
	"runtime/debug"

	"github.com/MontFerret/playground/internal/playground"
)

func serviceIdentity() playground.Service {
	info, _ := debug.ReadBuildInfo()

	return buildIdentity(info)
}

func buildIdentity(info *debug.BuildInfo) playground.Service {
	identity := playground.Service{Name: "ferret-playground"}
	if info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version := info.Main.Version
		identity.Version = &version
	}

	return identity
}
