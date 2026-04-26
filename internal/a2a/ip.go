package a2a

import (
	"net"
)

// PreferredIP returns the preferred outbound IP address on the default route.
// This is the IP that other machines on the same LAN can reach.
// Falls back to 127.0.0.1 if no suitable address is found.
func PreferredIP() string {
	// Dial a non-routable address — doesn't actually send traffic,
	// just lets the OS pick the default-route source IP.
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()

	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return "127.0.0.1"
	}

	// Skip loopback — means no real network interface available.
	if addr.IP.IsLoopback() {
		return "127.0.0.1"
	}

	return addr.IP.String()
}
