package webrtc

import (
	"strings"
	"sync"
	"testing"

	"github.com/pion/webrtc/v4"
)

func ptrString(s string) *string { return &s }
func ptrUint16(v uint16) *uint16 { return &v }

func TestIssue549BugA_CloseVsAttachDataChannelRace(t *testing.T) {
	p, err := NewPeer()
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	defer p.Close()

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

	_ = p.Close()
	wg.Wait()
}

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

	var wg sync.WaitGroup
	wg.Add(4)
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			p.signalDCReady()
		}()
	}
	go func() {
		defer wg.Done()
		_ = p.Close()
	}()
	wg.Wait()

	select {
	case <-p.dcReadyCh:
	default:
		t.Fatal("dcReadyCh not closed after OnOpen/Close signaling")
	}
}

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

	if err := offerer.SetRemoteAnswer(answerSDP); err != nil {
		t.Fatalf("SetRemoteAnswer: %v", err)
	}
	offerer.mu.Lock()
	buffered = len(offerer.pendingCandidates)
	offerer.mu.Unlock()
	if buffered != 0 {
		t.Fatalf("buffered candidates not drained after SetRemoteAnswer, %d left", buffered)
	}

	if err := offerer.AddICECandidate(cand); err != nil {
		t.Fatalf("AddICECandidate after remote description: %v", err)
	}
}

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
