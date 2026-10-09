package lanchat

// #3732 probes: fragment reassembly must sanity-bound packet-declared
// totals and per-assembly bytes. An authenticated hostile LAN peer could
// declare FragmentTotal=1000000 (map growth for the full timeout window),
// send seq beyond the declared total, or flood an assembly past any bound.

import (
	"encoding/json"
	"testing"
)

func issue3732Transport(t *testing.T) *UDPTransport {
	t.Helper()
	tr := &UDPTransport{
		nodeID:    "self",
		fragments: make(map[string]*fragmentAssembly),
	}
	return tr
}

func issue3732FragEnv(id string, total, seq int, chunk string) udpEnvelope {
	payload, _ := json.Marshal(chunk)
	return udpEnvelope{
		Type:          "fragment",
		FromNode:      "peer",
		FragmentID:    id,
		FragmentTotal: total,
		FragmentSeq:   seq,
		Payload:       payload,
	}
}

func TestIssue3732_AbsurdTotalRejected(t *testing.T) {
	tr := issue3732Transport(t)
	tr.handleFragment(issue3732FragEnv("huge", 1000000, 0, "x"), nil, "multicast")
	if len(tr.fragments) != 0 {
		t.Fatalf("absurd FragmentTotal must not create an assembly, got %d", len(tr.fragments))
	}
	// Zero/negative totals are equally invalid.
	tr.handleFragment(issue3732FragEnv("zero", 0, 0, "x"), nil, "multicast")
	if len(tr.fragments) != 0 {
		t.Fatal("zero FragmentTotal must be rejected")
	}
}

func TestIssue3732_SeqOutOfBoundsDropped(t *testing.T) {
	tr := issue3732Transport(t)
	tr.handleFragment(issue3732FragEnv("oob", 2, 0, "a"), nil, "multicast")
	tr.handleFragment(issue3732FragEnv("oob", 2, 5, "b"), nil, "multicast")
	if len(tr.fragments["oob"].received) != 1 {
		t.Fatal("out-of-range seq must be dropped, assembly kept")
	}
}

func TestIssue3732_ByteCapEvictsAssembly(t *testing.T) {
	tr := issue3732Transport(t)
	big := string(make([]byte, 1024*1024)) // 1MB chunks
	tr.handleFragment(issue3732FragEnv("flood", maxFragmentTotal, 0, big), nil, "multicast")
	for i := 1; i < maxAssemblyBytes/(1024*1024)+2 && tr.fragments["flood"] != nil; i++ {
		tr.handleFragment(issue3732FragEnv("flood", maxFragmentTotal, i, big), nil, "multicast")
	}
	if _, alive := tr.fragments["flood"]; alive {
		t.Fatal("assembly exceeding maxAssemblyBytes must be evicted")
	}
}

func TestIssue3732_LegitimateSmallMessageStillReassembles(t *testing.T) {
	tr := issue3732Transport(t)
	tr.handleFragment(issue3732FragEnv("ok", 2, 0, "hello "), nil, "multicast")
	a := tr.fragments["ok"]
	if a == nil || len(a.received) != 1 || string(a.received[0]) != "hello " {
		t.Fatal("legitimate fragment must be stored")
	}
	if a.bytes != len("hello ") {
		t.Fatalf("byte accounting wrong: %d", a.bytes)
	}
}
