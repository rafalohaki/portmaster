package netenv

import (
	"net"
	"time"

	"github.com/safing/portmaster/service-android/go/app_interface"
)

var (
	monitorNetworkChangeOnlineTicker  = time.NewTicker(time.Second)
	monitorNetworkChangeOfflineTicker = time.NewTicker(time.Second)
)

func init() {
	// Network change event is monitored by the android system.
	monitorNetworkChangeOnlineTicker.Stop()
	monitorNetworkChangeOfflineTicker.Stop()
}

func osGetInterfaceAddrs() ([]net.Addr, error) {
	list, err := app_interface.GetNetworkAddresses()
	if err != nil {
		return nil, err
	}

	var netList []net.Addr
	for _, addr := range list {
		ipNetAddr, err := addr.ToIPNet()
		if err == nil {
			netList = append(netList, ipNetAddr)
		}
	}

	return netList, nil
}

func osGetNetworkInterfaces() ([]app_interface.NetworkInterface, error) {
	return app_interface.GetNetworkInterfaces()
}
