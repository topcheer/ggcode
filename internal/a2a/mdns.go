package a2a

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	mdnslib "github.com/topcheer/mdns"
)

const (
	mDNSServiceType = "_ggcode._tcp"
	mDNSDomain      = "local."
	mDNSLookupTime  = 3 * time.Second
)

// mdnsService manages mDNS registration (broadcasting self) and
// discovery (finding peers on the LAN) using the topcheer/mdns library.
type mdnsService struct {
	server *mdnslib.Server
	info   *InstanceInfo
}

func newMDNSService() *mdnsService {
	return &mdnsService{}
}

// start broadcasts this instance via mDNS using the pure-Go topcheer/mdns library.
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

	m.server = srv
	debug.Log("a2a.mdns", "registered as %s port=%d", name, port)
	return nil
}

// stop shuts down the mDNS server (sends goodbye records).
func (m *mdnsService) stop() {
	if m.server != nil {
		m.server.Close()
		m.server = nil
	}
}

// lookup discovers other ggcode instances on the LAN via mDNS.
// Creates a temporary browser, waits for responses, then returns results.
func (m *mdnsService) lookup() []InstanceInfo {
	if m.server == nil {
		return nil
	}

	browser, err := m.server.Browse(mDNSServiceType)
	if err != nil {
		debug.Log("a2a.mdns", "browse error: %v", err)
		return nil
	}

	events, err := browser.Start()
	if err != nil {
		debug.Log("a2a.mdns", "browser start error: %v", err)
		return nil
	}

	// Wait for responses to arrive.
	timer := time.NewTimer(mDNSLookupTime)
	defer timer.Stop()

	// Drain events to keep the browser processing.
	go func() {
		for range events {
		}
	}()

	<-timer.C
	browser.Stop()

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

	debug.Log("a2a.mdns", "lookup found %d instances (after self-filter: %d)", len(instances), len(result))
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
