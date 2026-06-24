package a2a

import (
	"net"
	"os/exec"
	"runtime"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// DefaultRouteInterface returns the name of the network interface used for
// the default IPv4 route (i.e. the primary NIC). This is used to pick which
// interface to advertise via mDNS when no explicit interfaces are configured.
func DefaultRouteInterface() string {
	switch runtime.GOOS {
	case "darwin":
		return defaultRouteInterfaceDarwin()
	case "linux":
		return defaultRouteInterfaceLinux()
	case "windows":
		return defaultRouteInterfaceWindows()
	default:
		return ""
	}
}

func defaultRouteInterfaceDarwin() string {
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		debug.Log("a2a.netutil", "darwin route get failed: %v", err)
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "interface:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
		}
	}
	return ""
}

func defaultRouteInterfaceLinux() string {
	// Try `ip route` first (modern systems).
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "dev" && i+1 < len(fields) {
					return fields[i+1]
				}
			}
		}
	}

	// Fallback: /proc/net/route (busybox/embedded).
	out, err = exec.Command("cat", "/proc/net/route").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n")[1:] { // skip header
			fields := strings.Fields(line)
			if len(fields) >= 8 && fields[1] == "00000000" {
				// Interface name is the last field.
				return fields[len(fields)-1]
			}
		}
	}
	return ""
}

func defaultRouteInterfaceWindows() string {
	out, err := exec.Command("netstat", "-rn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == "0.0.0.0" {
			// The interface column is typically the last meaningful field.
			return fields[len(fields)-1]
		}
	}
	return ""
}

// InterfaceIPv4s returns non-loopback IPv4 addresses for the named interface.
// Returns nil if the interface doesn't exist or has no IPv4 addresses.
func InterfaceIPv4s(ifaceName string) []net.IP {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		debug.Log("a2a.netutil", "interface %s not found: %v", ifaceName, err)
		return nil
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsLoopback() {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil {
			ips = append(ips, ip4)
		}
	}
	return ips
}

// ResolveInterfaces returns the effective interface list for mDNS advertising.
// If interfaces is non-empty, it is returned as-is (after validation).
// If empty, the default-route interface is auto-detected.
// Returns at least one interface name, or empty slice if detection fails.
func ResolveInterfaces(interfaces []string) []string {
	if len(interfaces) > 0 {
		// Validate that each named interface exists.
		var valid []string
		for _, name := range interfaces {
			if _, err := net.InterfaceByName(name); err != nil {
				debug.Log("a2a.netutil", "skipping invalid interface %q: %v", name, err)
				continue
			}
			valid = append(valid, name)
		}
		if len(valid) > 0 {
			return valid
		}
	}

	// Auto-detect default route interface.
	def := DefaultRouteInterface()
	if def != "" {
		debug.Log("a2a.netutil", "auto-detected default route interface: %s", def)
		return []string{def}
	}
	debug.Log("a2a.netutil", "could not detect default route interface; will advertise all IPs")
	return nil
}

// IPsForInterfaces collects all non-loopback IPv4 addresses from the given
// interface names. If interfaces is nil/empty, returns all non-loopback IPv4s
// (same behavior as the mdns library's localIPs()).
func IPsForInterfaces(interfaces []string) []net.IP {
	if len(interfaces) == 0 {
		return allNonLoopbackIPv4s()
	}
	var ips []net.IP
	for _, name := range interfaces {
		ips = append(ips, InterfaceIPv4s(name)...)
	}
	return ips
}

// allNonLoopbackIPv4s returns all non-loopback IPv4 addresses on all active
// interfaces. This is the fallback when no interfaces are specified.
func allNonLoopbackIPv4s() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				ips = append(ips, ip4)
			}
		}
	}
	return ips
}
