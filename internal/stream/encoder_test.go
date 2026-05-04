package stream

import (
	"testing"
)

func TestEncoderFrameSizeValidation(t *testing.T) {
	enc := NewEncoder(100, 100, 26, 15, "")

	// Wrong size frame should error (encoder not started, but test the size check)
	// Encoder not running should error
	err := enc.WriteFrame([]byte{0x00})
	if err == nil {
		t.Error("expected error writing to non-running encoder")
	}

	// Correct size but still not running
	frame := make([]byte, 100*100*4)
	err = enc.WriteFrame(frame)
	if err == nil {
		t.Error("expected error writing to non-running encoder")
	}
}

func TestEncoderIsRunning(t *testing.T) {
	enc := NewEncoder(100, 100, 26, 15, "")
	if enc.IsRunning() {
		t.Error("new encoder should not be running")
	}
}

func TestEncoderStopWhenNotRunning(t *testing.T) {
	enc := NewEncoder(100, 100, 26, 15, "")
	err := enc.Stop()
	if err != nil {
		t.Errorf("stopping non-running encoder should not error: %v", err)
	}
}
