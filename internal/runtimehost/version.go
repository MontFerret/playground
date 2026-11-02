package runtimehost

import (
	"runtime/debug"

	"github.com/MontFerret/api"
)

func dependencyVersion(info *debug.BuildInfo) *api.Version {
	v := moduleVersion(info, "github.com/MontFerret/ferret/v2")

	return (*api.Version)(v)
}

func moduleVersion(info *debug.BuildInfo, path string) *string {
	if info == nil {
		return nil
	}

	for _, dependency := range info.Deps {
		if dependency.Path != path {
			continue
		}

		if dependency.Replace != nil {
			dependency = dependency.Replace
		}

		if dependency.Version == "" || dependency.Version == "(devel)" {
			return nil
		}

		version := dependency.Version

		return &version
	}

	return nil
}
