package lanchat

import (
	"encoding/json"
	"net/http"
)

// MountHandlers registers lanchat HTTP endpoints on the given mux.
// All endpoints require the same X-API-Key auth as the A2A server.
func MountHandlers(mux *http.ServeMux, hub *Hub) {
	mux.HandleFunc("/lanchat/message", hub.handleReceiveMessage)
	mux.HandleFunc("/lanchat/receipt", hub.handleReceiveReceipt)
	mux.HandleFunc("/lanchat/nick", hub.handleNickChange)
	mux.HandleFunc("/lanchat/participants", hub.handleParticipantQuery)
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
