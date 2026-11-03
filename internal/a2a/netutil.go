package a2a

import (
	"net"
	"os/exec"
	"runtime"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

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

	out, err = exec.Command("cat", "/proc/net/route").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) >= 8 && fields[1] == "00000000" {
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
		if len(fields) >= 4 && fields[0] == "0.0.0.0" && fields[1] == "0.0.0.0" {
			ifaceIP := fields[3]
			if name := interfaceNameByIP(ifaceIP); name != "" {
				return name
			}
			return ""
		}
	}
	return ""
}

func interfaceNameByIP(ipStr string) string {
	target := net.ParseIP(ipStr)
	if target == nil {
		return ""
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
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
			if ip != nil && ip.Equal(target) {
				return iface.Name
			}
		}
	}
	return ""
}

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

func ResolveInterfaces(interfaces []string) []string {
	if len(interfaces) > 0 {
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

	def := DefaultRouteInterface()
	if def != "" {
		debug.Log("a2a.netutil", "auto-detected default route interface: %s", def)
		return []string{def}
	}
	debug.Log("a2a.netutil", "could not detect default route interface; will advertise all IPs")
	return nil
}

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
