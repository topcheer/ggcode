package lanchat

import (
	"encoding/json"
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
		mux.HandleFunc("/lanchat/attach/", hub.attachments.HandleAttachmentDownload)
	}

	// Start UDP transport on the same port as TCP for fallback delivery.
	if tcpPort > 0 {
		udp, err := NewUDPTransport(tcpPort, udpMulticastAddr, hub, hub.NodeID(), communityKey)
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
// It is always accepted regardless of the configured A2A API key.
const communityKey = "ggcode-lan-a2a-v1"

// AuthMiddleware wraps an http.HandlerFunc with API key validation.
// Accepts either the configured API key or the built-in community key
// (for zero-config LAN Chat between instances with different auth configs).
func AuthMiddleware(apiKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Accept the community key always.
		if key == communityKey {
			next(w, r)
			return
		}
		// Accept the configured key if set.
		if apiKey != "" && key == apiKey {
			next(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

func (h *Hub) handleReceiveMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
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
