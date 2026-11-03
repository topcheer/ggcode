package a2a

import (
	"net"

	"github.com/topcheer/ggcode/internal/debug"
)

func PreferredIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err == nil {
		defer conn.Close()
		addr, ok := conn.LocalAddr().(*net.UDPAddr)
		if ok && addr.IP != nil && !addr.IP.IsLoopback() {
			return addr.IP.String()
		}
	}

	debug.Log("a2a.ip", "UDP dial method failed (%v), falling back to interface scan", err)

	if defIface := DefaultRouteInterface(); defIface != "" {
		if ips := InterfaceIPv4s(defIface); len(ips) > 0 {
			debug.Log("a2a.ip", "using IP from default route interface %s: %s", defIface, ips[0])
			return ips[0].String()
		}
	}

	for _, ip := range allNonLoopbackIPv4s() {
		debug.Log("a2a.ip", "using fallback non-loopback IP: %s", ip)
		return ip.String()
	}

	return "127.0.0.1"
}
