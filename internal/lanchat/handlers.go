package lanchat

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/topcheer/ggcode/internal/debug"
)

// MountHandlers registers lanchat HTTP endpoints on the given mux and
// optionally starts a UDP transport on the same port for fallback delivery.
func MountHandlers(mux *http.ServeMux, hub *Hub, tcpPort int) {
	apiKey := hub.APIKey()
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return AuthMiddleware(apiKey, next)
	}
	mux.HandleFunc("/lanchat/message", auth(hub.handleReceiveMessage))
	mux.HandleFunc("/lanchat/receipt", auth(hub.handleReceiveReceipt))
	mux.HandleFunc("/lanchat/nick", auth(hub.handleNickChange))
	mux.HandleFunc("/lanchat/presence", auth(hub.handlePresence))
	mux.HandleFunc("/lanchat/participants", auth(hub.handleParticipantQuery))
	if hub.attachments != nil {
		// #768: every other /lanchat/* endpoint is behind auth(); the raw
		// registration here let any LAN host fetch an attachment by UUID.
		mux.HandleFunc("/lanchat/attach/", auth(hub.attachments.HandleAttachmentDownload))
	}

	// Start UDP transport on the same port as TCP for fallback delivery.
	if tcpPort > 0 {
		// #989: pass the hub's EFFECTIVE key (hub.APIKey()), not the hardcoded
		// community key - a custom-key node's UDP fallback envelopes must carry
		// the custom key or peers running the same key reject them (#988 sibling).
		udp, err := NewUDPTransport(tcpPort, udpMulticastAddr, hub, hub.NodeID(), hub.APIKey())
		if err != nil {
			debug.Log("lanchat", "UDP transport not started (port %d): %v", tcpPort, err)
			return
		}
		udp.Start()
		hub.SetUDPTransport(udp)
		debug.Log("lanchat", "UDP transport started on port %d (unicast + multicast %s)", tcpPort, udpMulticastAddr)
	}
}

// communityKey is the built-in shared key for zero-config LAN Chat.
// It is accepted ONLY when no custom API key is configured; setting
// a2a.auth.api_key disables community-key access entirely (#986).
const communityKey = "ggcode-lan-a2a-v1"

// AuthMiddleware wraps an http.HandlerFunc with API key validation.
//
// Zero-config (no custom key configured): only the built-in community key
// is accepted, so out-of-the-box instances interoperate as before.
//
// When a custom API key IS configured, the community key is rejected: the
// well-known constant is public knowledge and must never silently bypass
// an operator's configured key (#986).
func AuthMiddleware(apiKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if apiKey == "" || apiKey == communityKey {
			// Zero-config mode: the hub runs on the community key.
			if key != communityKey {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		} else if key != apiKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// lanchatMaxBodyBytes caps any single lanchat request body (#1583-A).
// Attachments travel out-of-band (50MB cap there); chat messages are
// text - 1MB is orders of magnitude above any legitimate message.
const lanchatMaxBodyBytes = 1 << 20

func (h *Hub) handleReceiveMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// #1583-A: authenticated-but-unbounded bodies - a single oversized
	// message entered memory AND hit persistMessage's synchronous disk
	// write (message count caps did not bound per-message SIZE). Auth is
	// orthogonal; resource ceilings apply after auth too.
	r.Body = http.MaxBytesReader(w, r.Body, lanchatMaxBodyBytes)

	var msg Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "message body exceeds "+fmt.Sprintf("%d", mbe.Limit)+" bytes", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid message: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Ignore messages from self (loop prevention)
	if msg.FromNodeID == h.nodeID {
		w.WriteHeader(http.StatusOK)
		return
	}

	h.HandleIncomingMessage(msg)
	w.WriteHeader(http.StatusOK)
}

func (h *Hub) handleReceiveReceipt(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()
	r.Body = http.MaxBytesReader(w, r.Body, lanchatMaxBodyBytes)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var receipt Receipt
	if err := json.NewDecoder(r.Body).Decode(&receipt); err != nil {
		http.Error(w, "invalid receipt: "+err.Error(), http.StatusBadRequest)
		return
	}

	h.HandleReceipt(receipt)
	w.WriteHeader(http.StatusOK)
}

func (h *Hub) handleNickChange(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()
	r.Body = http.MaxBytesReader(w, r.Body, lanchatMaxBodyBytes)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var change NickChange
	if err := json.NewDecoder(r.Body).Decode(&change); err != nil {
		http.Error(w, "invalid nick change: "+err.Error(), http.StatusBadRequest)
		return
	}

	h.HandleNickChange(change)
	w.WriteHeader(http.StatusOK)
}

func (h *Hub) handleParticipantQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	p := h.HandleParticipantQuery()
	json.NewEncoder(w).Encode(p)
}

func (h *Hub) handlePresence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var p Participant
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid presence: "+err.Error(), http.StatusBadRequest)
		return
	}

	if p.NodeID == h.nodeID {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Respond with our own presence so both sides learn each other
	h.HandlePresence(p)
	json.NewEncoder(w).Encode(h.SelfParticipant())
}
