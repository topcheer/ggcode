package a2a

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hashicorp/mdns"
	"github.com/topcheer/ggcode/internal/debug"
)

const (
	mDNSServiceType = "_ggcode._tcp"
	mDNSDomain      = "local."
	mDNSLookupTime  = 3 * time.Second
)

// silentLogger suppresses hashicorp/mdns library output that would
// otherwise corrupt the TUI or daemon follow display.
var silentLogger = log.New(io.Discard, "", 0)

// mdnsService manages mDNS registration (broadcasting self) and
// discovery (finding peers on the LAN).
type mdnsService struct {
	mdnsServer *mdns.Server // hashicorp/mdns server (fallback / macOS)
	avahiCmd   *exec.Cmd    // avahi-publish subprocess (Linux preferred)
	info       *InstanceInfo
}

func newMDNSService() *mdnsService {
	return &mdnsService{}
}

// start broadcasts this instance via mDNS.
// On Linux: prefers avahi-publish (if available) for reliable registration.
// Falls back to hashicorp/mdns on all platforms.
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

	// Try avahi-publish first (most reliable on Linux).
	if runtime.GOOS == "linux" {
		if err := m.startAvahi(name, port, txt); err == nil {
			debug.Log("a2a.mdns", "registered via avahi-publish as %s", name)
			return nil
		}
		// Fall through to hashicorp/mdns
		debug.Log("a2a.mdns", "avahi-publish not available (%v), falling back to hashicorp/mdns", err)
	}

	// Fallback: hashicorp/mdns server.
	return m.startHashicorp(name, port, txt)
}

// startAvahi registers the service using the avahi-publish command-line tool.
// This is more reliable than hashicorp/mdns on Linux where avahi-daemon is common.
func (m *mdnsService) startAvahi(name string, port int, txt []string) error {
	avahiPath, err := exec.LookPath("avahi-publish")
	if err != nil {
		return fmt.Errorf("avahi-publish not found: %w", err)
	}

	args := []string{"-s", name, mDNSServiceType, strconv.Itoa(port)}
	args = append(args, txt...)

	cmd := exec.Command(avahiPath, args...)
	cmd.Stdout = nil // discard — writing to stderr corrupts TUI
	cmd.Stderr = nil
	// Put avahi-publish in its own process group (Unix only).
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("avahi-publish start: %w", err)
	}
	m.avahiCmd = cmd

	// Give avahi-publish a moment to register, and check it didn't die immediately.
	time.Sleep(500 * time.Millisecond)

	// Check if the process is still alive.
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		return fmt.Errorf("avahi-publish died immediately: %w", err)
	}

	debug.Log("a2a.mdns", "registered via avahi-publish as %s port=%d", name, port)

	// Monitor in background — if avahi-publish dies, we just lose registration silently.
	go func() {
		err := cmd.Wait()
		if err != nil {
			debug.Log("a2a.mdns", "avahi-publish exited: %v", err)
		}
	}()

	return nil
}

// startHashicorp registers using the hashicorp/mdns Go library.
// Works well on macOS, less reliable on Linux with multiple interfaces.
func (m *mdnsService) startHashicorp(name string, port int, txt []string) error {
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

	iface := PreferredInterface()
	cfg := &mdns.Config{Zone: service, Logger: silentLogger}
	if iface != nil {
		cfg.Iface = iface
	}
	server, err := mdns.NewServer(cfg)
	if err != nil {
		return fmt.Errorf("mDNS server: %w", err)
	}
	m.mdnsServer = server

	return nil
}

// stop shuts down the mDNS server and/or avahi-publish subprocess.
func (m *mdnsService) stop() {
	if m.avahiCmd != nil && m.avahiCmd.Process != nil {
		// Kill the process group (Unix) or just the process (Windows).
		killProcessGroup(m.avahiCmd.Process.Pid)
		m.avahiCmd = nil
	}
	if m.mdnsServer != nil {
		m.mdnsServer.Shutdown()
		m.mdnsServer = nil
	}
}

// lookup discovers other ggcode instances on the LAN via mDNS.
// Uses system tools (avahi-browse / dns-sd) for reliable discovery,
// falling back to hashicorp/mdns if neither is available.
func (m *mdnsService) lookup() []InstanceInfo {
	// Try system tools first — they work with the system's mDNS daemon
	// and are much more reliable than hashicorp/mdns on Linux.
	if entries := m.lookupSystem(); entries != nil {
		return m.filterSelf(entries)
	}

	// Fallback: hashicorp/mdns Go library (works on macOS, unreliable on Linux).
	return m.filterSelf(m.lookupHashicorp())
}

// filterSelf removes entries matching our own instance ID.
func (m *mdnsService) filterSelf(instances []InstanceInfo) []InstanceInfo {
	selfID := ""
	if m.info != nil {
		selfID = m.info.ID
	}
	var result []InstanceInfo
	for _, inst := range instances {
		if inst.ID != selfID {
			result = append(result, inst)
		}
	}
	return result
}

// lookupSystem tries to discover instances using system mDNS tools.
// Returns nil if no system tool is available (caller should fall back).
func (m *mdnsService) lookupSystem() []InstanceInfo {
	// Try avahi-browse (Linux).
	if path, err := exec.LookPath("avahi-browse"); err == nil {
		return m.lookupAvahi(path)
	}
	// Try dns-sd (macOS).
	if path, err := exec.LookPath("dns-sd"); err == nil {
		return m.lookupDNSSD(path)
	}
	return nil
}

// lookupAvahi uses avahi-browse -rpt to discover services.
func (m *mdnsService) lookupAvahi(avahiBrowsePath string) []InstanceInfo {
	ctx, cancel := context.WithTimeout(context.Background(), mDNSLookupTime)
	defer cancel()

	cmd := exec.CommandContext(ctx, avahiBrowsePath, "-rpt", "_ggcode._tcp")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	// avahi-browse -rpt output format (tab-separated):
	// =;eth0;IPv4;order-service;_ggcode._tcp;local;beedeb.local;192.168.31.3;33129;["id=xxx" "workspace=/tmp/test"]
	var instances []InstanceInfo
	for _, line := range strings.Split(string(output), "\n") {
		inst := parseAvahiLine(line)
		if inst != nil {
			instances = append(instances, *inst)
		}
	}
	return instances
}

// parseAvahiLine parses a single avahi-browse -rpt output line.
func parseAvahiLine(line string) *InstanceInfo {
	fields := strings.Split(line, "\t")
	// =;iface;proto;name;type;domain;host;ip;port;txt
	if len(fields) < 10 || !strings.HasPrefix(fields[0], "=") {
		return nil
	}

	ip := fields[7]
	portStr := fields[8]
	port, err := strconv.Atoi(portStr)
	if err != nil || port == 0 {
		return nil
	}

	// Parse TXT field: ["key1=val1" "key2=val2"]
	txtRaw := fields[9]
	txtRaw = strings.TrimPrefix(txtRaw, "[")
	txtRaw = strings.TrimSuffix(txtRaw, "]")
	txtParts := strings.Split(txtRaw, `" "`)
	txt := make(map[string]string, len(txtParts))
	for _, p := range txtParts {
		p = strings.Trim(p, `"`)
		if idx := strings.IndexByte(p, '='); idx >= 0 {
			txt[p[:idx]] = p[idx+1:]
		}
	}

	endpoint := fmt.Sprintf("%s:%d", ip, port)
	id := txt["id"]
	if id == "" {
		id = fmt.Sprintf("avahi-%s-%d", fields[3], port)
	}

	pid := 0
	if p := txt["pid"]; p != "" {
		pid, _ = strconv.Atoi(p)
	}

	return &InstanceInfo{
		ID:           id,
		PID:          pid,
		Workspace:    txt["workspace"],
		StartedAt:    txt["started"],
		Endpoint:     endpoint,
		AgentCardURL: endpoint + "/.well-known/agent.json",
		Status:       txt["status"],
	}
}

// lookupDNSSD uses macOS dns-sd to discover services.
func (m *mdnsService) lookupDNSSD(dnsSDPath string) []InstanceInfo {
	ctx, cancel := context.WithTimeout(context.Background(), mDNSLookupTime)
	defer cancel()

	cmd := exec.CommandContext(ctx, dnsSDPath, "-B", "_ggcode._tcp", "local.")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	// dns-sd -B output:
	// Timestamp     A/R    Flags  if Domain               Service Type         Instance Name
	// 22:15:56.937  Add        2  23 local.               _ggcode._tcp.        order-service
	var instances []InstanceInfo
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		// Look for "Add" lines
		if len(fields) < 9 || fields[1] != "Add" {
			continue
		}
		name := fields[len(fields)-1]
		// We only get the name from -B; need -L to resolve.
		// For now, use a simplified instance with just the name.
		instances = append(instances, InstanceInfo{
			ID:        fmt.Sprintf("dns-sd-%s", name),
			Workspace: name,
		})
	}
	return instances
}

// entryToInstance converts an mDNS ServiceEntry to InstanceInfo.
// lookupHashicorp uses the hashicorp/mdns Go library for discovery.
// Less reliable than system tools on Linux, but works on macOS.
func (m *mdnsService) lookupHashicorp() []InstanceInfo {
	entriesCh := make(chan *mdns.ServiceEntry, 16)

	done := make(chan struct{})
	go func() {
		params := &mdns.QueryParam{
			Service:             mDNSServiceType,
			Domain:              mDNSDomain,
			Timeout:             time.Duration(mDNSLookupTime),
			Entries:             entriesCh,
			WantUnicastResponse: false,
			Logger:              silentLogger,
		}
		iface := PreferredInterface()
		if iface != nil {
			params.Interface = iface
		}
		if err := mdns.Query(params); err != nil {
			debug.Log("a2a.mdns", "mDNS lookup error: %v", err)
		}
		close(entriesCh)
		close(done)
	}()

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

	var instances []InstanceInfo
	for _, entry := range entries {
		inst := entryToInstance(entry)
		if inst != nil {
			instances = append(instances, *inst)
		}
	}
	return instances
}

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
