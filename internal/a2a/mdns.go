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
func (m *mdnsService) start(info InstanceInfo) error {
	m.info = &info

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
		Domain: mDNSDomain,
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

	debug.Log("a2a.mdns", "registered as %s port=%d, browser started", name, port)
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

	// Prefer IPv4.
	ip := ""
	for _, addr := range inst.IPs {
		if addr.To4() != nil {
			ip = addr.String()
			break
		}
	}
	if ip == "" {
		ip = inst.IPs[0].String()
	}

	endpoint := fmt.Sprintf("%s:%d", ip, inst.Port)

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
