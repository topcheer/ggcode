package lanchat

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/topcheer/ggcode/internal/debug"
)

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

		mux.HandleFunc("/lanchat/attach/", auth(hub.attachments.HandleAttachmentDownload))
	}

	if tcpPort > 0 {

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

const communityKey = "ggcode-lan-a2a-v1"

func AuthMiddleware(apiKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if apiKey == "" || apiKey == communityKey {

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

const lanchatMaxBodyBytes = 1 << 20

func (h *Hub) handleReceiveMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

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
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "receipt body too large", http.StatusRequestEntityTooLarge)
			return
		}
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
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "nick body too large", http.StatusRequestEntityTooLarge)
			return
		}
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

	defer func() { _ = r.Body.Close() }()
	r.Body = http.MaxBytesReader(w, r.Body, lanchatMaxBodyBytes)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var p Participant
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "presence body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid presence: "+err.Error(), http.StatusBadRequest)
		return
	}

	if p.NodeID == h.nodeID {
		w.WriteHeader(http.StatusOK)
		return
	}

	h.HandlePresence(p)
	json.NewEncoder(w).Encode(h.SelfParticipant())
}
