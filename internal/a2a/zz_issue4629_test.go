package a2a

import (
	"strconv"
	"testing"
)

func TestIssue4629_PushConfigRegistryCapped(t *testing.T) {
	srv := issue715Server(t, nil)

	srv.pushMu.Lock()
	for i := 0; i < maxPushConfigs; i++ {
		srv.pushConfigs[issue4629ID(i)] = PushNotificationConfig{
			TaskID: "t1", URL: "https://push-ok.invalid/cb",
		}
	}
	srv.pushMu.Unlock()

	resp := issue715SetPush(t, srv, "issue715-real-key", PushNotificationConfig{
		ID: "push-new-overflow", TaskID: "t1", URL: "https://push-ok.invalid/cb",
	})
	if resp.Error == nil {
		t.Fatal("expected registry-full rejection for a new ID at cap (#4629)")
	}

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
