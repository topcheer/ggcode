package lanchat

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

// maxFragmentTotal caps the declared total fragments of one message (#3732):
// FragmentTotal is packet input, and an authenticated hostile peer could
// otherwise declare a huge total and grow the received map unchecked for the
// full timeout window. A real message is at most a few dozen fragments.
const maxFragmentTotal = 4096

// maxAssemblyBytes caps the accumulated payload of ONE fragment assembly
// (#3732): chunks are ~31.5KB each (udpMaxPayload-512), so 16MB is two orders
// beyond any legitimate chat payload; a hostile peer flooding an assembly
// gets it evicted rather than buffered.
const maxAssemblyBytes = 16 << 20 // 16 MiB

// maxUDPDecompressed caps the decompressed size of any UDP payload. LAN chat
// messages are small; without a cap a ~60KB high-ratio gzip datagram can expand
// to GBs of memory before the auth gate ever sees the envelope — a no-key LAN
// DoS (#991).
const maxUDPDecompressed = 4 * 1024 * 1024

// udpAckWaitFn returns the per-attempt ACK wait used by SendUnicast. Tests
// override it to shrink retry exhaustion below the default 2s×3 (#991).
var udpAckWaitFn = func() time.Duration { return udpACKTimeout }

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
	stopOnce   sync.Once
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
	bytes    int // accumulated payload bytes (#3732 per-assembly cap)
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

	if laddr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		t.listenPort = laddr.Port
	}

	if mcastAddr != "" {
		ma, err := net.ResolveUDPAddr("udp4", mcastAddr)
		if err == nil {
			mcastListen := &net.UDPAddr{IP: net.IPv4zero, Port: ma.Port}
			pc, err := net.ListenPacket("udp4", mcastListen.String())
			if err == nil {
				pconn := ipv4.NewPacketConn(pc)

				ifaces, _ := net.Interfaces()
				for _, iface := range ifaces {
					if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
						continue
					}
					_ = pconn.JoinGroup(&iface, ma)
				}
				t.mcastConn = pc.(*net.UDPConn)

				t.mcastAddr = ma

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

// Stop shuts down the UDP transport. Idempotent — safe to call multiple times.
func (t *UDPTransport) Stop() {
	t.stopOnce.Do(func() {
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
	})
	t.wg.Wait()
}

func (t *UDPTransport) readLoop(conn *net.UDPConn, label string) {
	buf := make([]byte, 64*1024)
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

		data := make([]byte, n)
		copy(data, buf[:n])
		t.processDatagram(data, remoteAddr, label)
	}
}

func (t *UDPTransport) processDatagram(data []byte, remoteAddr *net.UDPAddr, source string) {

	data = decompressIfGzipped(data)

	var env udpEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		debug.Log("lanchat-udp", "%s: parse error from %s: %v", source, remoteAddr, err)
		return
	}

	if t.apiKey == "" || t.apiKey == communityKey {
		if env.APIKey != communityKey {
			debug.Log("lanchat-udp", "%s: auth failed from %s", source, remoteAddr)
			return
		}
	} else if env.APIKey != t.apiKey {
		debug.Log("lanchat-udp", "%s: auth failed from %s", source, remoteAddr)
		return
	}

	if env.Type == "ack" {
		t.signalACK(env.ACKID)
		return
	}

	if env.IsFragment {
		t.handleFragment(env, remoteAddr, source)
		return
	}

	t.restoreSingleDatagram(&env)

	t.hub.handleUDPEnvelope(env, remoteAddr)

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

	if env.FragmentID == "" {
		env.FragmentID = fmt.Sprintf("udp-%d", time.Now().UnixNano())
	}
	ackTimeout := udpAckWaitFn()

	payloadBytes, err := json.Marshal(env)
	if err != nil {
		return err
	}

	compressed := compressIfNeeded(payloadBytes)

	fragments := splitFragments(compressed, env.FragmentID, env.APIKey, env.FromNode, env.Type)

	var sendErr error
	for _, frag := range fragments {
		fragData, _ := json.Marshal(frag)

		ackID := frag.FragmentID
		if frag.IsFragment {
			ackID = fmt.Sprintf("%s-%d", frag.FragmentID, frag.FragmentSeq)
		}
		acked := false
		for attempt := 0; attempt <= udpMaxRetries; attempt++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			ch := t.registerACK(ackID)
			if _, err := t.conn.WriteToUDP(fragData, addr); err != nil {
				t.unregisterACK(ackID)
				debug.Log("lanchat-udp", "unicast send error attempt %d: %v", attempt+1, err)
				continue
			}
			select {
			case <-ch:
				acked = true
			case <-time.After(ackTimeout):
			case <-ctx.Done():
				t.unregisterACK(ackID)
				return ctx.Err()
			}
			t.unregisterACK(ackID)
			if acked {
				break
			}
			debug.Log("lanchat-udp", "unicast ACK timeout attempt %d for %s", attempt+1, ackID)
		}
		if !acked {
			sendErr = fmt.Errorf("udp unicast: fragment %s to %s not ACKed after %d attempts", ackID, addr, udpMaxRetries+1)
			break
		}
	}

	return sendErr
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
			_, _ = t.mcastSend.Write(fragData)
		} else {
			_, _ = t.mcastConn.WriteToUDP(fragData, t.mcastAddr)
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

	payloadStr := base64.StdEncoding.EncodeToString(data)
	if len(payloadStr) <= udpMaxPayload-512 {

		return []udpEnvelope{{
			Type:       msgType,
			APIKey:     apiKey,
			FromNode:   fromNode,
			Payload:    json.RawMessage(`"` + payloadStr + `"`),
			FragmentID: fragID,
		}}
	}

	chunkSize := udpMaxPayload - 512
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

// decompressIfGzipped checks for gzip magic bytes and decompresses, capped at
// maxUDPDecompressed bytes. Oversized payloads are dropped (nil) so callers
// fail the parse — the cap removes the gzip-bomb DoS vector on both the
// datagram and fragment-reassembly paths (#991).
func decompressIfGzipped(data []byte) []byte {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return data
	}
	defer gz.Close()

	out, err := io.ReadAll(io.LimitReader(gz, maxUDPDecompressed+1))
	if err != nil {
		return data
	}
	if len(out) > maxUDPDecompressed {
		debug.Log("lanchat-udp", "decompressed UDP payload exceeds %d bytes, dropping", maxUDPDecompressed)
		return nil
	}
	return out
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

// handleFragment collects fragments and reassembles when complete.
func (t *UDPTransport) handleFragment(env udpEnvelope, remoteAddr *net.UDPAddr, source string) {
	t.fragMu.Lock()
	defer t.fragMu.Unlock()

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

		if env.FragmentTotal <= 0 || env.FragmentTotal > maxFragmentTotal {
			debug.Log("lanchat-udp", "fragment assembly rejected: total=%d out of range (id=%s from=%s)", env.FragmentTotal, env.FragmentID, env.FromNode)
			return
		}
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

	if env.FragmentSeq < 0 || env.FragmentSeq >= assembly.total {
		debug.Log("lanchat-udp", "fragment seq %d out of range [0,%d) dropped (id=%s)", env.FragmentSeq, assembly.total, env.FragmentID)
		return
	}
	chunk := []byte(chunkStr)
	if assembly.bytes+len(chunk) > maxAssemblyBytes {
		debug.Log("lanchat-udp", "fragment assembly %s exceeded %d bytes; evicting", env.FragmentID, maxAssemblyBytes)
		delete(t.fragments, env.FragmentID)
		return
	}
	if _, dup := assembly.received[env.FragmentSeq]; !dup {
		assembly.bytes += len(chunk)
	}
	assembly.received[env.FragmentSeq] = chunk

	if source == "unicast" {
		ackID := fmt.Sprintf("%s-%d", env.FragmentID, env.FragmentSeq)
		ack := udpEnvelope{
			Type:     "ack",
			APIKey:   t.apiKey,
			FromNode: t.nodeID,
			ACKID:    ackID,
		}
		ackData, _ := json.Marshal(ack)

		safego.Go("lanchat.udp.ack", func() {
			_, _ = t.conn.WriteToUDP(ackData, remoteAddr)
		})
	}

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

	fullData, err := base64.StdEncoding.DecodeString(fullStr)
	if err != nil {
		debug.Log("lanchat-udp", "fragment base64 decode error: %v", err)
		delete(t.fragments, env.FragmentID)
		return
	}

	fullData = decompressIfGzipped(fullData)

	// Parse the complete envelope
	var completeEnv udpEnvelope
	if err := json.Unmarshal(fullData, &completeEnv); err != nil {
		debug.Log("lanchat-udp", "fragment reassembly parse error: %v", err)
		delete(t.fragments, env.FragmentID)
		return
	}

	delete(t.fragments, env.FragmentID)

	completeEnv.APIKey = env.APIKey
	t.hub.handleUDPEnvelope(completeEnv, remoteAddr)
}

// restoreSingleDatagram reverses the single-datagram wrapping applied by
// splitFragments: small envelopes travel as one non-fragment datagram whose
// Payload is a base64 JSON string of the original (possibly compressed)
// envelope bytes. Before #2765 the base64 string was passed straight to the
// hub, where every json.Unmarshal (string into a struct) failed and the
// message was silently dropped while the unconditional ACK still told the
// sender it was delivered, suppressing the multicast fallback retry. This
// mirrors handleFragment's restoration: base64 decode -> decompress if
// gzipped (capped by maxUDPDecompressed) -> re-unmarshal the inner
// envelope. Empty or JSON-object payloads (acks, legacy peers) pass
// through untouched; foreign formats log and pass through so the hub's
// type switch retains its existing behavior for them.
func (t *UDPTransport) restoreSingleDatagram(env *udpEnvelope) {
	if len(env.Payload) == 0 || env.Payload[0] == '{' {
		return
	}
	var b64 string
	if err := json.Unmarshal(env.Payload, &b64); err != nil {
		debug.Log("lanchat-udp", "single-datagram payload not a JSON string: %v", err)
		return
	}
	inner, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		debug.Log("lanchat-udp", "single-datagram base64 decode error: %v", err)
		return
	}
	inner = decompressIfGzipped(inner)
	var restored udpEnvelope
	if err := json.Unmarshal(inner, &restored); err != nil {
		debug.Log("lanchat-udp", "single-datagram reassembly parse error: %v", err)
		return
	}
	restored.APIKey = env.APIKey
	*env = restored
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
