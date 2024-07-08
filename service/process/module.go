package process

import (
	"errors"
	"os"
	"sync/atomic"

	"github.com/safing/portmaster/service/mgr"
	"github.com/safing/portmaster/service/updates"
)

type ProcessModule struct {
	mgr      *mgr.Manager
	instance instance
}

func (pm *ProcessModule) Manager() *mgr.Manager {
	return pm.mgr
}

func (pm *ProcessModule) Start() error {
	if err := prep(); err != nil {
		return err
	}

	return start()
}

func (pm *ProcessModule) Stop() error {
	return nil
}

var updatesPath string

func prep() error {
	return registerConfiguration()
}

func start() error {
	updatesPath = updates.RootPath()
	if updatesPath != "" {
		updatesPath += string(os.PathSeparator)
	}

	if err := registerAPIEndpoints(); err != nil {
		return err
	}

	return nil
}

var (
	module     *ProcessModule
	shimLoaded atomic.Bool
)

// New returns a new Process module.
func New(instance instance) (*ProcessModule, error) {
	if !shimLoaded.CompareAndSwap(false, true) {
		return nil, errors.New("only one instance allowed")
	}

	if err := prep(); err != nil {
		return nil, err
	}
	m := mgr.New("ProcessModule")
	module = &ProcessModule{
		mgr:      m,
		instance: instance,
	}
	return module, nil
}

type instance interface{}
