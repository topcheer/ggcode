package a2a

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	mdnslib "github.com/topcheer/mdns"
)

const (
	mDNSServiceType = "_ggcode._tcp"
	mDNSDomain      = "local."
)

// mdnsService manages mDNS registration (broadcasting self) and
// discovery (finding peers on the LAN) using the topcheer/mdns library.
//
// A persistent Browser is kept running so that the instance list is always
// up-to-date. lookup() reads the browser's current state without any
// network I/O, eliminating timing gaps that caused peer flickering.
type mdnsService struct {
	server  *mdnslib.Server
	browser *mdnslib.Browser
	info    *InstanceInfo
}

func newMDNSService() *mdnsService {
	return &mdnsService{}
}

// start broadcasts this instance via mDNS and begins continuous discovery.
// interfaces controls which NICs are used for advertising; if nil, the
// default-route interface is auto-detected.
func (m *mdnsService) start(info InstanceInfo, interfaces []string) error {
	m.info = &info

	// Resolve effective interfaces (auto-detect default route if not specified).
	effectiveIfaces := ResolveInterfaces(interfaces)

	// Compute the IPs to advertise — only from the selected interfaces.
	advertiseIPs := IPsForInterfaces(effectiveIfaces)
	if len(advertiseIPs) == 0 {
		debug.Log("a2a.mdns", "no IPs from interfaces %v, falling back to all", effectiveIfaces)
		advertiseIPs = allNonLoopbackIPv4s()
	}

	// Extract port from endpoint (may include http:// scheme).
	endpoint := info.Endpoint
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")
	_, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("mDNS: parse endpoint %q: %w", info.Endpoint, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("mDNS: parse port %q: %w", portStr, err)
	}

	// Instance name: workspace basename + port for uniqueness.
	name := sanitizeMDNSName(info.DisplayName())

	// TXT records carry metadata for discovery without needing to connect.
	txt := []string{
		"id=" + info.ID,
		"workspace=" + info.Workspace,
		"status=" + info.Status,
		"pid=" + strconv.Itoa(info.PID),
		"started=" + info.StartedAt,
	}

	srv, err := mdnslib.NewServer(mdnslib.Config{
		Domain:     mDNSDomain,
		Interfaces: effectiveIfaces,
	})
	if err != nil {
		return fmt.Errorf("mDNS server create: %w", err)
	}

	if err := srv.Start(); err != nil {
		srv.Close()
		return fmt.Errorf("mDNS server start: %w", err)
	}

	svc := &mdnslib.ServiceInstance{
		Type: mDNSServiceType,
		Name: name,
		Port: uint16(port),
		Text: txt,
		IPs:  advertiseIPs, // explicitly set which IPs to advertise in A records
	}

	if err := srv.RegisterService(svc); err != nil {
		srv.Close()
		return fmt.Errorf("mDNS register: %w", err)
	}

	// Start persistent browser for continuous discovery.
	browser, err := srv.Browse(mDNSServiceType)
	if err != nil {
		srv.Close()
		return fmt.Errorf("mDNS browse: %w", err)
	}
	events, err := browser.Start()
	if err != nil {
		srv.Close()
		return fmt.Errorf("mDNS browser start: %w", err)
	}

	// Drain events so the browser's internal instances map stays updated.
	// The browser handles add/remove via cache events automatically.
	safego.Go("a2a.mdns.browserDrain", func() {
		for ev := range events {
			if ev.Instance != nil {
				switch ev.Action {
				case mdnslib.EventAdd:
					debug.Log("a2a.mdns", "discovered: %s (%s)", ev.Instance.Name, ev.Instance.Host)
				case mdnslib.EventRemove:
					debug.Log("a2a.mdns", "lost: %s", ev.Instance.Name)
				}
			}
		}
	})

	m.server = srv
	m.browser = browser

	debug.Log("a2a.mdns", "registered as %s port=%d ifaces=%v ips=%v, browser started",
		name, port, effectiveIfaces, advertiseIPs)
	return nil
}

// stop shuts down the mDNS browser and server (sends goodbye records).
func (m *mdnsService) stop() {
	if m.browser != nil {
		m.browser.Stop()
		m.browser = nil
	}
	if m.server != nil {
		m.server.Close()
		m.server = nil
	}
}

// lookup returns all discovered instances (excluding self) by reading the
// persistent browser's current state. No network I/O — instant and reliable.
func (m *mdnsService) lookup() []InstanceInfo {
	if m.browser == nil {
		return nil
	}

	instances := m.browser.Instances()
	var result []InstanceInfo
	selfID := ""
	if m.info != nil {
		selfID = m.info.ID
	}

	for _, inst := range instances {
		info := serviceInfoToInstance(inst)
		if info == nil || info.ID == selfID {
			continue
		}
		result = append(result, *info)
	}

	debug.Log("a2a.mdns", "lookup: browser has %d instances (after self-filter: %d)", len(instances), len(result))
	return result
}

// serviceInfoToInstance converts a ServiceInstanceInfo to InstanceInfo.
func serviceInfoToInstance(inst *mdnslib.ServiceInstanceInfo) *InstanceInfo {
	if inst == nil || len(inst.IPs) == 0 {
		return nil
	}

	// Prefer an IPv4 address that's reachable on a local subnet.
	// This avoids picking a Docker/VPN IP that the receiver can't reach.
	ip := pickBestIP(inst.IPs)

	endpoint := fmt.Sprintf("http://%s:%d", ip, inst.Port)

	// Parse TXT records into a map.
	txt := parseTXTFields(inst.Text)

	id := txt["id"]
	if id == "" {
		id = fmt.Sprintf("mdns-%s-%d", inst.Name, inst.Port)
	}

	workspace := txt["workspace"]
	if workspace == "" {
		workspace = inst.Name
	}

	pid := 0
	if p := txt["pid"]; p != "" {
		pid, _ = strconv.Atoi(p)
	}

	return &InstanceInfo{
		ID:           id,
		PID:          pid,
		Workspace:    workspace,
		StartedAt:    txt["started"],
		Endpoint:     endpoint,
		AgentCardURL: endpoint + "/.well-known/agent.json",
		Status:       txt["status"],
	}
}

// parseTXTFields converts TXT record string slice to a map.
func parseTXTFields(fields []string) map[string]string {
	m := make(map[string]string, len(fields))
	for _, f := range fields {
		if idx := strings.IndexByte(f, '='); idx >= 0 {
			m[f[:idx]] = f[idx+1:]
		}
	}
	return m
}

// sanitizeMDNSName makes a string safe for use as an mDNS service instance name.
func sanitizeMDNSName(name string) string {
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, string(os.PathSeparator), "-")
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// pickBestIP selects the best IP address from a list of discovered IPs.
// Preference order:
//  1. IPv4 addresses on a local subnet (reachable without routing)
//  2. Any IPv4 address
//  3. First address (IPv6 fallback)
func pickBestIP(ips []net.IP) string {
	// Collect local subnets once.
	localSubnets := localSubnetList()

	// Pass 1: IPv4 on a local subnet.
	for _, addr := range ips {
		if ip4 := addr.To4(); ip4 != nil {
			for _, subnet := range localSubnets {
				if subnet.Contains(ip4) {
					return ip4.String()
				}
			}
		}
	}

	// Pass 2: any IPv4.
	for _, addr := range ips {
		if ip4 := addr.To4(); ip4 != nil {
			return ip4.String()
		}
	}

	// Pass 3: fallback.
	if len(ips) > 0 {
		return ips[0].String()
	}
	return ""
}

// localSubnetList returns all local network subnets (IPv4 only) for
// use in pickBestIP preference matching.
func localSubnetList() []*net.IPNet {
	var subnets []*net.IPNet
	ifaces, err := net.Interfaces()
	if err != nil {
		return subnets
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				if ipNet.IP.To4() != nil {
					subnets = append(subnets, ipNet)
				}
			}
		}
	}
	return subnets
}
