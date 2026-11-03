package webrtc

import (
	"encoding/json"
	"fmt"

	"github.com/pion/webrtc/v4"
)

func encodeSDP(desc webrtc.SessionDescription) string {
	data, _ := json.Marshal(desc)
	return string(data)
}

func decodeSDP(s string) (webrtc.SessionDescription, error) {
	var desc webrtc.SessionDescription
	if err := json.Unmarshal([]byte(s), &desc); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("decode SDP: %w", err)
	}
	return desc, nil
}

func encodeCandidate(init webrtc.ICECandidateInit) (string, error) {
	data, err := json.Marshal(init)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeCandidate(s string) (webrtc.ICECandidateInit, error) {
	var init webrtc.ICECandidateInit
	if err := json.Unmarshal([]byte(s), &init); err != nil {
		return webrtc.ICECandidateInit{}, fmt.Errorf("decode candidate: %w", err)
	}
	return init, nil
}
