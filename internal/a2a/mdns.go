package a2a

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	mdnslib "github.com/topcheer/mdns"
	"sync"
)

const (
	mDNSServiceType = "_ggcode._tcp"
	mDNSDomain      = "local."
)

type mdnsService struct {
	mu      sync.Mutex
	server  *mdnslib.Server
	browser *mdnslib.Browser
	info    *InstanceInfo
}

func newMDNSService() *mdnsService {
	return &mdnsService{}
}

func (m *mdnsService) start(info InstanceInfo, interfaces []string) error {
	m.info = &info

	effectiveIfaces := ResolveInterfaces(interfaces)

	advertiseIPs := IPsForInterfaces(effectiveIfaces)
	if len(advertiseIPs) == 0 {
		debug.Log("a2a.mdns", "no IPs from interfaces %v, falling back to all", effectiveIfaces)
		advertiseIPs = allNonLoopbackIPv4s()
	}

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

	name := sanitizeMDNSName(info.DisplayName())

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
		IPs:  advertiseIPs,
	}

	if err := srv.RegisterService(svc); err != nil {
		srv.Close()
		return fmt.Errorf("mDNS register: %w", err)
	}

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

func (m *mdnsService) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.browser != nil {
		m.browser.Stop()
		m.browser = nil
	}
	if m.server != nil {
		m.server.Close()
		m.server = nil
	}
}

func (m *mdnsService) lookup() []InstanceInfo {
	m.mu.Lock()
	browser := m.browser
	m.mu.Unlock()
	if browser == nil {
		return nil
	}

	instances := browser.Instances()
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

	if debug.IsVerbose("a2a.mdns") {
		debug.Log("a2a.mdns", "lookup: browser has %d instances (after self-filter: %d)", len(instances), len(result))
	}
	return result
}

func serviceInfoToInstance(inst *mdnslib.ServiceInstanceInfo) *InstanceInfo {
	if inst == nil || len(inst.IPs) == 0 {
		return nil
	}

	ip := pickBestIP(inst.IPs)

	endpoint := "http://" + net.JoinHostPort(ip, strconv.Itoa(int(inst.Port)))

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

func parseTXTFields(fields []string) map[string]string {
	m := make(map[string]string, len(fields))
	for _, f := range fields {
		if idx := strings.IndexByte(f, '='); idx >= 0 {
			m[f[:idx]] = f[idx+1:]
		}
	}
	return m
}

func sanitizeMDNSName(name string) string {
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "\\", "-")
	if len(name) > 63 {
		var b strings.Builder
		for _, r := range name {
			if b.Len()+utf8.RuneLen(r) > 63 {
				break
			}
			b.WriteRune(r)
		}
		name = b.String()
	}
	return name
}

func pickBestIP(ips []net.IP) string {
	localSubnets := localSubnetList()

	for _, addr := range ips {
		if ip4 := addr.To4(); ip4 != nil {
			for _, subnet := range localSubnets {
				if subnet.Contains(ip4) {
					return ip4.String()
				}
			}
		}
	}

	for _, addr := range ips {
		if ip4 := addr.To4(); ip4 != nil {
			return ip4.String()
		}
	}

	if len(ips) > 0 {
		return ips[0].String()
	}
	return ""
}

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
