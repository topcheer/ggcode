package audit

import "testing"

func TestHashEntryAttributionSlotsDoNotCollide(t *testing.T) {
	base := Entry{
		Seq: 7, Time: "t", Session: "s", Tool: "tool", Status: "ok",
		InputHash: "ih", DurationMS: 5, Err: "", PrevHash: "ph",
	}
	it, ip, pt := base, base, base
	it.InvariantID, it.TaskID = "A", "B"
	ip.InvariantID, ip.Peer = "A", "B"
	pt.Peer, pt.TaskID = "A", "B"
	perms := map[string]Entry{"inv+task": it, "inv+peer": ip, "peer+task": pt}

	hashes := map[string]string{}
	for name, e := range perms {
		hashes[name] = hashEntry(e)
	}
	if hashes["inv+task"] == hashes["inv+peer"] {
		t.Fatal("(Inv=A,Task=B) and (Inv=A,Peer=B) must NOT collide (#4707)")
	}
	if hashes["inv+task"] == hashes["peer+task"] {
		t.Fatal("(Inv=A,Task=B) and (Peer=A,Task=B) must NOT collide (#4707)")
	}
	if hashes["inv+peer"] == hashes["peer+task"] {
		t.Fatal("(Inv=A,Peer=B) and (Peer=A,Task=B) must NOT collide (#4707)")
	}
}

func TestHashEntryDeterministic(t *testing.T) {
	a := Entry{Seq: 1, Time: "t", Session: "s", Tool: "x", Status: "ok", PrevHash: "p", InvariantID: "INV", Peer: "peer"}
	if hashEntry(a) != hashEntry(a) {
		t.Fatal("hashEntry must be deterministic")
	}
}

func TestHashEntryEmptyAttributionChainVerifies(t *testing.T) {
	e0 := Entry{Seq: 1, Time: "t", Session: "s", Tool: "x", Status: "ok", PrevHash: "p"}
	h0 := hashEntry(e0)
	e1 := e0
	e1.Peer = "someone"
	if hashEntry(e1) == h0 {
		t.Fatal("adding Peer must change the hash")
	}
}
