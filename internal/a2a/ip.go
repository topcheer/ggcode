package a2a

import (
	"net"

	"github.com/topcheer/ggcode/internal/debug"
)

// PreferredIP returns the preferred outbound IP address on the default route.
// This is the IP that other machines on the same LAN can reach.
// Falls back to 127.0.0.1 if no suitable address is found.
func PreferredIP() string {
	// Method 1: Dial a non-routable address — doesn't actually send traffic,
	// just lets the OS pick the default-route source IP.
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err == nil {
		defer conn.Close()
		addr, ok := conn.LocalAddr().(*net.UDPAddr)
		if ok && addr.IP != nil && !addr.IP.IsLoopback() {
			return addr.IP.String()
		}
	}

	debug.Log("a2a.ip", "UDP dial method failed (%v), falling back to interface scan", err)

	// Method 2: Scan interfaces for the first non-loopback IPv4 on an
	// interface that matches the default route. This handles cases where
	// the UDP dial fails (e.g., Windows firewall, VPN, no connectivity).
	if defIface := DefaultRouteInterface(); defIface != "" {
		if ips := InterfaceIPv4s(defIface); len(ips) > 0 {
			debug.Log("a2a.ip", "using IP from default route interface %s: %s", defIface, ips[0])
			return ips[0].String()
		}
	}

	// Method 3: Last resort — first non-loopback IPv4 on any interface.
	for _, ip := range allNonLoopbackIPv4s() {
		debug.Log("a2a.ip", "using fallback non-loopback IP: %s", ip)
		return ip.String()
	}

	return "127.0.0.1"
}
