package webrtc

// Issue #549 characteristic tests:
//   - Bug A: Close() read p.dc outside the lock, racing with attachDataChannel.
//   - Bug B: two same-label DataChannels -> two OnOpen callbacks -> double
//     close of dcReadyCh -> panic in a pion callback goroutine (no recover).
//   - Bug C: ICE candidates arriving before the remote description are
//     buffered and replayed instead of silently dropped.
//
// The race tests rely on `go test -race` to detect the pre-fix data race.

import (
	"strings"
	"sync"
	"testing"

	"github.com/pion/webrtc/v4"
)

func ptrString(s string) *string { return &s }
func ptrUint16(v uint16) *uint16 { return &v }

// TestIssue549BugA_CloseVsAttachDataChannelRace races Close() against
// attachDataChannel calls on distinct DataChannels — mirroring production,
// where pion's OnDataChannel callback attaches each incoming channel exactly
// once while Close() may run concurrently. Pre-fix, Close() read p.dc without
// holding p.mu while attachDataChannel wrote it under the lock (-race hits).
// Note: each DataChannel is attached exactly once; concurrently re-registering
// handlers on the SAME channel is not supported by pion and not a production
// code path.
func TestIssue549BugA_CloseVsAttachDataChannelRace(t *testing.T) {
	p, err := NewPeer()
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	defer p.Close()

	// Pre-create data channels with the same label, simulating renegotiation
	// delivering multiple same-label channels to the answerer.
	const n = 16
	dcs := make([]*webrtc.DataChannel, 0, n)
	for i := 0; i < n; i++ {
		dc, err := p.pc.CreateDataChannel(DataChannelLabel, nil)
		if err != nil {
			t.Fatalf("CreateDataChannel: %v", err)
		}
		dcs = append(dcs, dc)
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(dc *webrtc.DataChannel) {
			defer wg.Done()
			p.attachDataChannel(dc)
		}(dcs[i])
	}

	// Close concurrently with the attach goroutines.
	_ = p.Close() // pre-fix: raced on p.dc read
	wg.Wait()
}

// TestIssue549BugB_DCReadyChClosedExactlyOnce simulates two same-label
// DataChannels both reaching the OnOpen code path (plus Close racing them).
// Pre-fix this panics with "close of closed channel".
func TestIssue549BugB_DCReadyChClosedExactlyOnce(t *testing.T) {
	p, err := NewPeer()
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	defer p.Close()

	dc1, err := p.pc.CreateDataChannel(DataChannelLabel, nil)
	if err != nil {
		t.Fatalf("CreateDataChannel 1: %v", err)
	}
	dc2, err := p.pc.CreateDataChannel(DataChannelLabel, nil)
	if err != nil {
		t.Fatalf("CreateDataChannel 2: %v", err)
	}
	p.attachDataChannel(dc1)
	p.attachDataChannel(dc2)

	// Simulate both OnOpen callbacks plus Close firing concurrently. Each
	// path goes through signalDCReady (exactly-once guarded by sync.Once).
	var wg sync.WaitGroup
	wg.Add(4)
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			p.signalDCReady() // OnOpen path
		}()
	}
	go func() {
		defer wg.Done()
		_ = p.Close() // also signals readiness on the failure path
	}()
	wg.Wait()

	// The channel must be closed (receivable, not blocking).
	select {
	case <-p.dcReadyCh:
	default:
		t.Fatal("dcReadyCh not closed after OnOpen/Close signaling")
	}
}

// TestIssue549BugC_EarlyICECandidateBufferedAndReplayed verifies that a
// candidate added before the remote description is buffered, and replayed
// once the remote description is applied (loopback offer/answer pair).
func TestIssue549BugC_EarlyICECandidateBufferedAndReplayed(t *testing.T) {
	offerer, err := NewPeer()
	if err != nil {
		t.Fatalf("NewPeer offerer: %v", err)
	}
	defer offerer.Close()
	answerer, err := NewPeer()
	if err != nil {
		t.Fatalf("NewPeer answerer: %v", err)
	}
	defer answerer.Close()

	offerSDP, err := offerer.CreateOffer()
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	if err := answerer.SetRemoteOffer(offerSDP); err != nil {
		t.Fatalf("SetRemoteOffer: %v", err)
	}
	answerSDP, err := answerer.CreateAnswer()
	if err != nil {
		t.Fatalf("CreateAnswer: %v", err)
	}

	// Candidate arrives before the offerer has applied the remote answer:
	// must be buffered (no error surfaced to the signal processor) instead
	// of hitting pion's InvalidStateError and being dropped.
	cand, err := encodeCandidate(webrtc.ICECandidateInit{
		Candidate:     "candidate:1 1 UDP 2130706431 192.168.1.10 8998 typ host",
		SDPMid:        ptrString("0"),
		SDPMLineIndex: ptrUint16(0),
	})
	if err != nil {
		t.Fatalf("encodeCandidate: %v", err)
	}
	if err := offerer.AddICECandidate(cand); err != nil {
		t.Fatalf("AddICECandidate before remote description should be buffered, got error: %v", err)
	}
	offerer.mu.Lock()
	buffered := len(offerer.pendingCandidates)
	offerer.mu.Unlock()
	if buffered != 1 {
		t.Fatalf("expected 1 buffered candidate, got %d", buffered)
	}

	// Applying the remote answer must replay the buffered candidate.
	if err := offerer.SetRemoteAnswer(answerSDP); err != nil {
		t.Fatalf("SetRemoteAnswer: %v", err)
	}
	offerer.mu.Lock()
	buffered = len(offerer.pendingCandidates)
	offerer.mu.Unlock()
	if buffered != 0 {
		t.Fatalf("buffered candidates not drained after SetRemoteAnswer, %d left", buffered)
	}

	// After the remote description is set, candidates go straight to pion.
	if err := offerer.AddICECandidate(cand); err != nil {
		t.Fatalf("AddICECandidate after remote description: %v", err)
	}
}

// TestIssue549BugC_DecodeFailureStillSurfaced ensures malformed candidates
// still return an error (only valid-but-early ones get buffered).
func TestIssue549BugC_DecodeFailureStillSurfaced(t *testing.T) {
	p, err := NewPeer()
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	defer p.Close()
	if err := p.AddICECandidate("not-json"); err == nil {
		t.Fatal("expected decode error for malformed candidate")
	} else if !strings.Contains(err.Error(), "decode candidate") {
		t.Fatalf("unexpected error: %v", err)
	}
}
