package webrtc

import (
	"encoding/json"
	"fmt"

	"github.com/pion/webrtc/v4"
)

// encodeSDP serializes a SessionDescription to a compact JSON string
// for transport over the relay signaling channel.
func encodeSDP(desc webrtc.SessionDescription) string {
	data, _ := json.Marshal(desc)
	return string(data)
}

// decodeSDP parses a JSON-serialized SessionDescription.
func decodeSDP(s string) (webrtc.SessionDescription, error) {
	var desc webrtc.SessionDescription
	if err := json.Unmarshal([]byte(s), &desc); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("decode SDP: %w", err)
	}
	return desc, nil
}

// encodeCandidate serializes an ICECandidateInit to a JSON string.
func encodeCandidate(init webrtc.ICECandidateInit) (string, error) {
	data, err := json.Marshal(init)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// decodeCandidate parses a JSON-serialized ICECandidateInit.
func decodeCandidate(s string) (webrtc.ICECandidateInit, error) {
	var init webrtc.ICECandidateInit
	if err := json.Unmarshal([]byte(s), &init); err != nil {
		return webrtc.ICECandidateInit{}, fmt.Errorf("decode candidate: %w", err)
	}
	return init, nil
}
