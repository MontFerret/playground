package runtimehost

import (
	"runtime/debug"

	"github.com/MontFerret/api"
)

func dependencyVersion(info *debug.BuildInfo) *api.Version {
	if info == nil {
		return nil
	}

	for _, dependency := range info.Deps {
		if dependency.Path != "github.com/MontFerret/ferret/v2" {
			continue
		}

		if dependency.Replace != nil {
			dependency = dependency.Replace
		}

		if dependency.Version == "" || dependency.Version == "(devel)" {
			return nil
		}

		version := api.Version(dependency.Version)

		return &version
	}

	return nil
}
