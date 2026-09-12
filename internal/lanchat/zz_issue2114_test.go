package lanchat

// #2114 regression:
//   - sendReceipt ignored http.NewRequest's error: a malformed peer
//     Endpoint (arbitrary string from presence JSON) left req nil and
//     req.Header.Set panicked inside the safego wrapper - the receipt was
//     silently lost.
//   - receipts grew without bound; every DM receipt from every LAN agent
//     added a permanent entry. Now FIFO-evicted at receiptCap, mirroring
//     markSeenLocked.

import (
	"testing"
)

func TestReceiptsFIFOEviction(t *testing.T) {
	h := &Hub{receipts: make(map[string]Receipt)}
	for i := 0; i < receiptCap+50; i++ {
		id := "msg-" + string(rune('a'+i%26)) + "-" + itoa(i)
		h.mu.Lock()
		h.storeReceiptLocked(Receipt{MessageID: id, Status: StatusDelivered})
		h.mu.Unlock()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.receipts) != receiptCap {
		t.Fatalf("receipts = %d, want capped at %d", len(h.receipts), receiptCap)
	}
	if len(h.receiptOrder) != receiptCap {
		t.Fatalf("receiptOrder = %d, want %d", len(h.receiptOrder), receiptCap)
	}
	// The oldest 50 must be evicted; the newest must survive.
	if _, ok := h.receipts["msg-a-0"]; ok {
		t.Fatal("oldest receipt must be evicted")
	}
	newest := "msg-" + string(rune('a'+(receiptCap+49)%26)) + "-" + itoa(receiptCap+49)
	if _, ok := h.receipts[newest]; !ok {
		t.Fatal("newest receipt must survive")
	}
}

func TestStoreReceiptOverwriteKeepsOrderOnce(t *testing.T) {
	h := &Hub{receipts: make(map[string]Receipt)}
	h.mu.Lock()
	h.storeReceiptLocked(Receipt{MessageID: "m1", Status: StatusPending})
	h.storeReceiptLocked(Receipt{MessageID: "m1", Status: StatusApproved})
	h.mu.Unlock()
	if len(h.receiptOrder) != 1 {
		t.Fatalf("re-storing the same MessageID must not duplicate the FIFO entry: %d", len(h.receiptOrder))
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.receipts["m1"].Status != StatusApproved {
		t.Fatal("re-store must update the receipt payload")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
