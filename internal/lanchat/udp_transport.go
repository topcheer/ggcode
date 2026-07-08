package lanchat

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/net/ipv4"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// udpHandler is implemented by the Hub to process incoming UDP messages.
type udpHandler interface {
	handleUDPEnvelope(env udpEnvelope, remoteAddr net.Addr)
}

// maxFragmentEntries caps the fragment reassembly map to prevent unbounded growth.
const maxFragmentEntries = 256

// UDPTransport listens for UDP datagrams (both unicast and multicast) and
// dispatches them to the Hub. It also provides methods for sending UDP
// messages with automatic compression and fragmentation.
type UDPTransport struct {
	conn       *net.UDPConn // unicast listener
	mcastConn  *net.UDPConn // multicast listener (may be nil)
	mcastSend  *net.UDPConn // dedicated multicast send socket (avoids RX interference)
	mcastAddr  *net.UDPAddr // multicast group address
	hub        udpHandler   // Hub
	nodeID     string       // our node ID (for filtering multicast DMs)
	apiKey     string       // community key for auth
	stopCh     chan struct{}
	wg         sync.WaitGroup
	listenPort int // port for self-filtering (ReadFromUDP remote port match)

	// Fragment reassembly
	fragMu    sync.Mutex
	fragments map[string]*fragmentAssembly // keyed by FragmentID

	// ACK registry: maps message ID → channel, signalled when ACK arrives
	ackMu sync.Mutex
	acks  map[string]chan struct{}
}

// fragmentAssembly collects fragments until all are received.
type fragmentAssembly struct {
	id       string
	total    int
	received map[int][]byte // seq → data
	deadline time.Time
	envType  string
	fromNode string
}

// NewUDPTransport creates a UDP listener bound to the given port.
// If multicastAddr is non-empty, it also joins the multicast group.
func NewUDPTransport(port int, mcastAddr string, hub udpHandler, nodeID, apiKey string) (*UDPTransport, error) {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: port}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("udp listen on port %d: %w", port, err)
	}
	conn.SetReadBuffer(256 * 1024)

	t := &UDPTransport{
		conn:      conn,
		hub:       hub,
		nodeID:    nodeID,
		apiKey:    apiKey,
		stopCh:    make(chan struct{}),
		fragments: make(map[string]*fragmentAssembly),
		acks:      make(map[string]chan struct{}),
	}

	// Resolve listen port for self-filtering
	if laddr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		t.listenPort = laddr.Port
	}

	// Join multicast group
	if mcastAddr != "" {
		ma, err := net.ResolveUDPAddr("udp4", mcastAddr)
		if err == nil {
			mcastListen := &net.UDPAddr{IP: net.IPv4zero, Port: ma.Port}
			pc, err := net.ListenPacket("udp4", mcastListen.String())
			if err == nil {
				pconn := ipv4.NewPacketConn(pc)
				// Join the multicast group on all non-loopback interfaces
				ifaces, _ := net.Interfaces()
				for _, iface := range ifaces {
					if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
						continue
					}
					_ = pconn.JoinGroup(&iface, ma)
				}
				t.mcastConn = pc.(*net.UDPConn)

				// Dedicated send socket bound to 0.0.0.0:0 for multicast writes.
				// Writing on the receive socket can interfere with the read loop
				// and cause source-address ambiguity on some platforms.
				sendConn, err := net.DialUDP("udp4", nil, ma)
				if err == nil {
					t.mcastSend = sendConn
				}

				debug.Log("lanchat-udp", "multicast listener joined %s on port %d", mcastAddr, ma.Port)
			}
		}
	}

	return t, nil
}

// Start begins the read loops for unicast and multicast UDP.
func (t *UDPTransport) Start() {
	t.wg.Add(1)
	safego.Go("lanchat.udp.unicast", func() {
		defer t.wg.Done()
		t.readLoop(t.conn, "unicast")
	})

	if t.mcastConn != nil {
		t.wg.Add(1)
		safego.Go("lanchat.udp.multicast", func() {
			defer t.wg.Done()
			t.readLoop(t.mcastConn, "multicast")
		})
	}

	// Fragment cleanup goroutine
	t.wg.Add(1)
	safego.Go("lanchat.udp.fragclean", func() {
		defer t.wg.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-t.stopCh:
				return
			case <-ticker.C:
				t.cleanupExpiredFragments()
			}
		}
	})
}

// Stop shuts down the UDP transport.
func (t *UDPTransport) Stop() {
	close(t.stopCh)
	if t.conn != nil {
		t.conn.Close()
	}
	if t.mcastConn != nil {
		t.mcastConn.Close()
	}
	if t.mcastSend != nil {
		t.mcastSend.Close()
	}
	t.wg.Wait()
}

func (t *UDPTransport) readLoop(conn *net.UDPConn, label string) {
	buf := make([]byte, 64*1024) // 64KB read buffer
	for {
		select {
		case <-t.stopCh:
			return
		default:
		}
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-t.stopCh:
				return
			default:
				debug.Log("lanchat-udp", "%s read error: %v", label, err)
				continue
			}
		}

		// Self-filter: skip our own multicast packets by source port
		if label == "multicast" && t.listenPort > 0 && remoteAddr.Port == t.listenPort {
			continue
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		t.processDatagram(data, remoteAddr, label)
	}
}

func (t *UDPTransport) processDatagram(data []byte, remoteAddr *net.UDPAddr, source string) {
	// Decompress at the datagram level (handles single-datagram compressed messages)
	data = decompressIfGzipped(data)

	var env udpEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		debug.Log("lanchat-udp", "%s: parse error from %s: %v", source, remoteAddr, err)
		return
	}

	// Auth check
	if env.APIKey != t.apiKey {
		debug.Log("lanchat-udp", "%s: auth failed from %s", source, remoteAddr)
		return
	}

	// Handle ACK: signal the waiting sender and skip hub processing
	if env.Type == "ack" {
		t.signalACK(env.ACKID)
		return
	}

	// Handle fragment
	if env.IsFragment {
		t.handleFragment(env, remoteAddr, source)
		return
	}

	// Normal message — dispatch to hub
	t.hub.handleUDPEnvelope(env, remoteAddr)

	// Send ACK for unicast (reliable delivery)
	if source == "unicast" {
		ackID := env.FragmentID
		if ackID == "" {
			ackID = fmt.Sprintf("udp-%d", time.Now().UnixNano())
		}
		ack := udpEnvelope{
			Type:     "ack",
			APIKey:   t.apiKey,
			FromNode: t.nodeID,
			ACKID:    ackID,
		}
		ackData, _ := json.Marshal(ack)
		_, _ = t.conn.WriteToUDP(ackData, remoteAddr)
	}
}

// SendUnicast sends an envelope via UDP unicast with compression,
// fragmentation, and ACK-based retry.
func (t *UDPTransport) SendUnicast(ctx context.Context, addr *net.UDPAddr, env udpEnvelope) error {
	// Set message ID for ACK tracking
	if env.FragmentID == "" {
		env.FragmentID = fmt.Sprintf("udp-%d", time.Now().UnixNano())
	}

	// Marshal the envelope to bytes (this is the canonical payload)
	payloadBytes, err := json.Marshal(env)
	if err != nil {
		return err
	}

	// Compress the payload (compression is preserved through the fragment path)
	compressed := compressIfNeeded(payloadBytes)

	// Split into fragments if needed. Fragments carry raw compressed bytes
	// as base64 chunks, preserving the compression end-to-end.
	fragments := splitFragments(compressed, env.FragmentID, env.APIKey, env.FromNode, env.Type)

	for _, frag := range fragments {
		fragData, _ := json.Marshal(frag)
		// Send with retry + ACK wait (per-fragment ACK for reliability)
		for attempt := 0; attempt <= udpMaxRetries; attempt++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if _, err := t.conn.WriteToUDP(fragData, addr); err != nil {
				debug.Log("lanchat-udp", "unicast send error attempt %d: %v", attempt+1, err)
				continue
			}
			// Wait for ACK for each fragment
			ackID := frag.FragmentID
			if frag.IsFragment {
				ackID = fmt.Sprintf("%s-%d", frag.FragmentID, frag.FragmentSeq)
			}
			if t.waitForACK(ackID, udpACKTimeout) {
				break // ACK received, move to next fragment
			}
			debug.Log("lanchat-udp", "unicast ACK timeout attempt %d for %s", attempt+1, ackID)
		}
	}

	return nil
}

// SendMulticast sends an envelope via UDP multicast (no ACK).
func (t *UDPTransport) SendMulticast(env udpEnvelope) error {
	if t.mcastSend == nil && t.mcastConn == nil {
		return fmt.Errorf("multicast not available")
	}

	if env.FragmentID == "" {
		env.FragmentID = fmt.Sprintf("udp-mcast-%d", time.Now().UnixNano())
	}

	payloadBytes, err := json.Marshal(env)
	if err != nil {
		return err
	}

	compressed := compressIfNeeded(payloadBytes)
	fragments := splitFragments(compressed, env.FragmentID, env.APIKey, env.FromNode, env.Type)

	for _, frag := range fragments {
		fragData, _ := json.Marshal(frag)
		if t.mcastSend != nil {
			_, _ = t.mcastSend.Write(fragData) // dedicated send socket
		} else {
			_, _ = t.mcastConn.WriteToUDP(fragData, t.mcastAddr) // fallback
		}
	}
	return nil
}

// compressIfNeeded returns gzip-compressed data only if it's actually smaller.
// Otherwise returns the original data unchanged.
func compressIfNeeded(data []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		return data
	}
	gz.Close()
	compressed := buf.Bytes()
	if len(compressed) < len(data) {
		return compressed
	}
	return data
}

// splitFragments divides raw payload bytes into fragment envelopes.
// The payload bytes (which may be gzip-compressed) are base64-encoded and
// split into chunks. Each fragment carries its chunk as a JSON string in Payload.
// The receiver reassembles the base64 string, decodes it, and decompresses if needed.
func splitFragments(data []byte, fragID, apiKey, fromNode, msgType string) []udpEnvelope {
	// If data fits in one datagram, send a single non-fragment envelope
	// containing the raw (possibly compressed) bytes as a base64 string.
	// The receiver decompresses after base64 decode.
	payloadStr := base64.StdEncoding.EncodeToString(data)
	if len(payloadStr) <= udpMaxPayload-512 {
		// Single datagram — wrap the original data as a non-fragment
		return []udpEnvelope{{
			Type:       msgType,
			APIKey:     apiKey,
			FromNode:   fromNode,
			Payload:    json.RawMessage(`"` + payloadStr + `"`),
			FragmentID: fragID,
		}}
	}

	// Need to fragment: split base64 string into chunks
	chunkSize := udpMaxPayload - 512 // leave room for envelope overhead
	totalFrags := (len(payloadStr) + chunkSize - 1) / chunkSize
	fragments := make([]udpEnvelope, 0, totalFrags)
	for i := 0; i < totalFrags; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(payloadStr) {
			end = len(payloadStr)
		}
		frag := udpEnvelope{
			Type:          msgType,
			APIKey:        apiKey,
			FromNode:      fromNode,
			Payload:       json.RawMessage(`"` + payloadStr[start:end] + `"`),
			FragmentID:    fragID,
			FragmentTotal: totalFrags,
			FragmentSeq:   i,
			IsFragment:    true,
		}
		fragments = append(fragments, frag)
	}
	return fragments
}

// decompressIfGzipped checks for gzip magic bytes and decompresses.
func decompressIfGzipped(data []byte) []byte {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data // not gzip
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return data
	}
	defer gz.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(gz); err != nil {
		return data
	}
	return buf.Bytes()
}

// registerACK creates a channel for tracking an ACK for the given message ID.
// Returns the channel. Caller should call unregisterACK when done.
func (t *UDPTransport) registerACK(msgID string) chan struct{} {
	ch := make(chan struct{}, 1)
	t.ackMu.Lock()
	t.acks[msgID] = ch
	t.ackMu.Unlock()
	return ch
}

// signalACK signals the waiting sender that an ACK was received.
func (t *UDPTransport) signalACK(msgID string) {
	t.ackMu.Lock()
	ch, ok := t.acks[msgID]
	if ok {
		delete(t.acks, msgID)
	}
	t.ackMu.Unlock()
	if ok {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// unregisterACK removes a pending ACK registration (cleanup on timeout).
func (t *UDPTransport) unregisterACK(msgID string) {
	t.ackMu.Lock()
	delete(t.acks, msgID)
	t.ackMu.Unlock()
}

// waitForACK blocks until an ACK for the given message ID is received or
// the timeout expires. Uses a channel-based registry for deterministic delivery.
func (t *UDPTransport) waitForACK(msgID string, timeout time.Duration) bool {
	ch := t.registerACK(msgID)
	defer t.unregisterACK(msgID)

	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}

// handleFragment collects fragments and reassembles when complete.
func (t *UDPTransport) handleFragment(env udpEnvelope, remoteAddr *net.UDPAddr, source string) {
	t.fragMu.Lock()
	defer t.fragMu.Unlock()

	// Cap fragment entries to prevent unbounded growth
	if len(t.fragments) >= maxFragmentEntries {
		// Evict the oldest by deadline
		var oldestID string
		var oldestDeadline time.Time
		for id, a := range t.fragments {
			if oldestID == "" || a.deadline.Before(oldestDeadline) {
				oldestID = id
				oldestDeadline = a.deadline
			}
		}
		if oldestID != "" {
			delete(t.fragments, oldestID)
		}
	}

	assembly, exists := t.fragments[env.FragmentID]
	if !exists {
		assembly = &fragmentAssembly{
			id:       env.FragmentID,
			total:    env.FragmentTotal,
			received: make(map[int][]byte),
			deadline: time.Now().Add(udpFragmentTimeout),
			envType:  env.Type,
			fromNode: env.FromNode,
		}
		t.fragments[env.FragmentID] = assembly
	}

	// Store the fragment payload (base64 encoded chunk)
	var chunkStr string
	if err := json.Unmarshal(env.Payload, &chunkStr); err != nil {
		debug.Log("lanchat-udp", "fragment payload parse error: %v", err)
		return
	}
	assembly.received[env.FragmentSeq] = []byte(chunkStr)

	// Send per-fragment ACK for unicast
	if source == "unicast" {
		ackID := fmt.Sprintf("%s-%d", env.FragmentID, env.FragmentSeq)
		ack := udpEnvelope{
			Type:     "ack",
			APIKey:   t.apiKey,
			FromNode: t.nodeID,
			ACKID:    ackID,
		}
		ackData, _ := json.Marshal(ack)
		// Use goroutine to avoid holding fragMu during network write
		go func(data []byte, addr *net.UDPAddr) {
			_, _ = t.conn.WriteToUDP(data, addr)
		}(ackData, remoteAddr)
	}

	// Check if we have all fragments
	if len(assembly.received) < assembly.total {
		return
	}

	// Reassemble: concatenate fragments in order
	var fullStr string
	for i := 0; i < assembly.total; i++ {
		chunk, ok := assembly.received[i]
		if !ok {
			debug.Log("lanchat-udp", "fragment missing seq=%d for %s", i, assembly.id)
			return
		}
		fullStr += string(chunk)
	}

	// Base64 decode the reassembled data
	fullData, err := base64.StdEncoding.DecodeString(fullStr)
	if err != nil {
		debug.Log("lanchat-udp", "fragment base64 decode error: %v", err)
		delete(t.fragments, env.FragmentID)
		return
	}

	// Decompress if needed (preserved through fragment path)
	fullData = decompressIfGzipped(fullData)

	// Parse the complete envelope
	var completeEnv udpEnvelope
	if err := json.Unmarshal(fullData, &completeEnv); err != nil {
		debug.Log("lanchat-udp", "fragment reassembly parse error: %v", err)
		delete(t.fragments, env.FragmentID)
		return
	}

	// Clean up
	delete(t.fragments, env.FragmentID)

	// Process the complete message
	completeEnv.APIKey = env.APIKey // preserve auth
	t.hub.handleUDPEnvelope(completeEnv, remoteAddr)
}

// cleanupExpiredFragments removes fragment assemblies that have timed out.
func (t *UDPTransport) cleanupExpiredFragments() {
	t.fragMu.Lock()
	defer t.fragMu.Unlock()
	now := time.Now()
	for id, assembly := range t.fragments {
		if now.After(assembly.deadline) {
			debug.Log("lanchat-udp", "fragment expired: %s (%d/%d received)", id, len(assembly.received), assembly.total)
			delete(t.fragments, id)
		}
	}
}

// Port returns the UDP port the transport is listening on.
func (t *UDPTransport) Port() int {
	if t.conn == nil {
		return 0
	}
	return t.conn.LocalAddr().(*net.UDPAddr).Port
}
