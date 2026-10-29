package runtimehost

import "github.com/MontFerret/ferret/v2/pkg/module"

type testModule struct {
	name     string
	register func(module.Bootstrap) error
}

func (m testModule) Name() string {
	return m.name
}

func (m testModule) Register(boot module.Bootstrap) error {
	return m.register(boot)
}
