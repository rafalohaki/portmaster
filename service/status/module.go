package status

import (
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/safing/portmaster/base/utils/debug"
	"github.com/safing/portmaster/service/mgr"
	"github.com/safing/portmaster/service/netenv"
)

type Status struct {
	mgr      *mgr.Manager
	instance instance
}

func (s *Status) Manager() *mgr.Manager {
	return s.mgr
}

func (s *Status) Start() error {
	if err := setupRuntimeProvider(); err != nil {
		return err
	}

	s.instance.NetEnv().EventOnlineStatusChange.AddCallback("update online status in system status",
		func(_ *mgr.WorkerCtx, _ netenv.OnlineStatus) (bool, error) {
			pushSystemStatus()
			return false, nil
		},
	)

	return nil
}

func (s *Status) Stop() error {
	return nil
}

// AddToDebugInfo adds the system status to the given debug.Info.
func AddToDebugInfo(di *debug.Info) {
	di.AddSection(
		fmt.Sprintf("Status: %s", netenv.GetOnlineStatus()),
		debug.UseCodeSection|debug.AddContentLineBreaks,
		fmt.Sprintf("OnlineStatus:          %s", netenv.GetOnlineStatus()),
		"CaptivePortal:         "+netenv.GetCaptivePortal().URL,
	)
}

var (
	module     *Status
	shimLoaded atomic.Bool
)

func New(instance instance) (*Status, error) {
	if !shimLoaded.CompareAndSwap(false, true) {
		return nil, errors.New("only one instance allowed")
	}
	m := mgr.New("Status")
	module = &Status{
		mgr:      m,
		instance: instance,
	}

	return module, nil
}

type instance interface {
	NetEnv() *netenv.NetEnv
}
