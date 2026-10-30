package a2a

import (
	"strconv"
	"testing"
)

// #4629: pushConfigs must have an entry cap - trusted peers could register
// unboundedly (fresh nano-second IDs, no TTL, no eviction). Existing-ID
// re-registration stays an upsert even at cap.
func TestIssue4629_PushConfigRegistryCapped(t *testing.T) {
	srv := issue715Server(t, nil) // default key: issue715-real-key

	// Fill the registry to the cap with distinct IDs.
	srv.pushMu.Lock()
	for i := 0; i < maxPushConfigs; i++ {
		srv.pushConfigs[issue4629ID(i)] = PushNotificationConfig{
			TaskID: "t1", URL: "https://push-ok.invalid/cb",
		}
	}
	srv.pushMu.Unlock()

	// New ID beyond the cap: rejected with a registry-full error.
	resp := issue715SetPush(t, srv, "issue715-real-key", PushNotificationConfig{
		ID: "push-new-overflow", TaskID: "t1", URL: "https://push-ok.invalid/cb",
	})
	if resp.Error == nil {
		t.Fatal("expected registry-full rejection for a new ID at cap (#4629)")
	}

	// Existing ID at cap: still an upsert, not a rejection.
	resp = issue715SetPush(t, srv, "issue715-real-key", PushNotificationConfig{
		ID: issue4629ID(0), TaskID: "t1", URL: "https://push-ok.invalid/cb",
	})
	if resp.Error != nil {
		t.Fatalf("existing-ID re-registration must stay an upsert at cap, got %+v (#4629)", resp.Error)
	}
}

func issue4629ID(i int) string {
	return "push-fill-" + strconv.Itoa(i)
}
