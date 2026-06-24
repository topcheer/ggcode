package lanchat

import (
	"encoding/json"
	"net/http"
)

// MountHandlers registers lanchat HTTP endpoints on the given mux.
// All endpoints are wrapped with AuthMiddleware using the Hub's API key.
func MountHandlers(mux *http.ServeMux, hub *Hub) {
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
}

// AuthMiddleware wraps an http.HandlerFunc with API key validation.
// If apiKey is empty, no auth is enforced.
func AuthMiddleware(apiKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if apiKey != "" {
			key := r.Header.Get("X-API-Key")
			if key != apiKey {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
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
