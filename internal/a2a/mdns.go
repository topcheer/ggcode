package a2a

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/mdns"
)

const (
	mDNSServiceType = "_ggcode._tcp"
	mDNSDomain      = "local."
	mDNSLookupTime  = 3 * time.Second
)

// mdnsService manages mDNS registration (broadcasting self) and
// discovery (finding peers on the LAN).
type mdnsService struct {
	server *mdns.Server
	info   *InstanceInfo
}

func newMDNSService() *mdnsService {
	return &mdnsService{}
}

// start broadcasts this instance via mDNS.
func (m *mdnsService) start(info InstanceInfo) error {
	m.info = &info

	// Extract port from endpoint.
	_, portStr, err := net.SplitHostPort(info.Endpoint)
	if err != nil {
		return fmt.Errorf("mDNS: parse endpoint %q: %w", info.Endpoint, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("mDNS: parse port %q: %w", portStr, err)
	}

	// Instance name: workspace basename + port for uniqueness.
	name := info.DisplayName()
	name = sanitizeMDNSName(name)

	// TXT records carry metadata for discovery without needing to connect.
	txt := []string{
		"id=" + info.ID,
		"workspace=" + info.Workspace,
		"status=" + info.Status,
		"pid=" + strconv.Itoa(info.PID),
		"started=" + info.StartedAt,
	}

	// Create the mDNS service definition.
	service, err := mdns.NewMDNSService(
		name,            // instance
		mDNSServiceType, // service type
		mDNSDomain,      // domain
		"",              // hostName (auto-detected)
		port,            // port
		nil,             // IPs (auto-detected)
		txt,             // TXT records
	)
	if err != nil {
		return fmt.Errorf("mDNS service: %w", err)
	}

	// Create and start the mDNS server.
	server, err := mdns.NewServer(&mdns.Config{Zone: service})
	if err != nil {
		return fmt.Errorf("mDNS server: %w", err)
	}
	m.server = server

	return nil
}

// stop shuts down the mDNS server.
func (m *mdnsService) stop() {
	if m.server != nil {
		m.server.Shutdown()
		m.server = nil
	}
}

// lookup discovers other ggcode instances on the LAN via mDNS.
// Returns instances found in the lookup window (typically 3 seconds).
func (m *mdnsService) lookup() []InstanceInfo {
	entriesCh := make(chan *mdns.ServiceEntry, 16)

	// Lookup is blocking — run with a timeout via goroutine + timer.
	done := make(chan struct{})
	go func() {
		if err := mdns.Lookup(mDNSServiceType, entriesCh); err != nil {
			log.Printf("mDNS lookup error: %v", err)
		}
		close(done)
	}()

	// Wait up to mDNSLookupTime for entries.
	timer := time.NewTimer(mDNSLookupTime)
	defer timer.Stop()

	var entries []*mdns.ServiceEntry
collect:
	for {
		select {
		case entry, ok := <-entriesCh:
			if !ok {
				break collect
			}
			entries = append(entries, entry)
		case <-timer.C:
			break collect
		case <-done:
			break collect
		}
	}

	selfID := ""
	if m.info != nil {
		selfID = m.info.ID
	}

	var instances []InstanceInfo
	for _, entry := range entries {
		inst := entryToInstance(entry)
		if inst == nil {
			continue
		}
		// Exclude self.
		if inst.ID == selfID {
			continue
		}
		instances = append(instances, *inst)
	}
	return instances
}

// entryToInstance converts an mDNS ServiceEntry to InstanceInfo.
func entryToInstance(entry *mdns.ServiceEntry) *InstanceInfo {
	if entry == nil {
		return nil
	}

	// Prefer IPv4.
	ip := ""
	if entry.AddrV4 != nil && len(entry.AddrV4) > 0 {
		ip = entry.AddrV4.String()
	} else if entry.Addr != nil {
		ip = entry.Addr.String()
	}
	if ip == "" {
		return nil
	}

	endpoint := fmt.Sprintf("%s:%d", ip, entry.Port)

	// Parse TXT records into fields.
	txt := parseTXTFields(entry.InfoFields)

	id := txt["id"]
	if id == "" {
		// Fallback: construct from name + port.
		id = fmt.Sprintf("mdns-%s-%d", entry.Name, entry.Port)
	}

	workspace := txt["workspace"]
	if workspace == "" {
		workspace = filepath.FromSlash(entry.Name)
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
	// Replace characters that are problematic in DNS names.
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")
	// Remove any path separators.
	name = strings.ReplaceAll(name, string(os.PathSeparator), "-")
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}
