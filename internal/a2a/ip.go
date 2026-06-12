package a2a

import (
	"net"
)

var localInterfaceIPs = func() []net.IP {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			switch v := addr.(type) {
			case *net.IPNet:
				if v.IP != nil {
					ips = append(ips, v.IP)
				}
			case *net.IPAddr:
				if v.IP != nil {
					ips = append(ips, v.IP)
				}
			}
		}
	}
	return ips
}

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

// PreferredInterface returns the net.Interface that carries the default-route IP.
// Returns nil if not found (caller should fall back to default behavior).
func PreferredInterface() *net.Interface {
	ip := net.ParseIP(PreferredIP())
	if ip == nil || ip.IsLoopback() {
		return nil
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ifaceIP net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ifaceIP = v.IP
			case *net.IPAddr:
				ifaceIP = v.IP
			}
			if ifaceIP != nil && ifaceIP.Equal(ip) {
				ifaceCopy := iface
				return &ifaceCopy
			}
		}
	}
	return nil
}

func isLocalRequestHost(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, localIP := range localInterfaceIPs() {
		if localIP != nil && localIP.Equal(ip) {
			return true
		}
	}
	return false
}
