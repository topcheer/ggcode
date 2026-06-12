package agentruntime

import (
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/tunnel"
)

func PublishShareState(broker *tunnel.Broker, sessionID string, snapshot tunnel.BrokerSnapshot, replay []tunnel.GatewayMessage, reset bool) bool {
	if broker == nil {
		return false
	}
	switchedSession := false
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		if reset {
			broker.SwitchSession(sessionID)
			switchedSession = true
		} else {
			broker.AnnounceActiveSession(sessionID)
		}
	} else if reset {
		broker.ResetSession()
	}
	if len(replay) > 0 {
		broker.ReplayEvents(replay, reset && !switchedSession)
		// Always ensure session_info is sent even when replaying events.
		// Mobile relies on receiving session_info to clear its
		// _awaitingSnapshotProjection flag and set sessionReady=true.
		if snapshot.SessionInfo != (tunnel.SessionInfoData{}) {
			broker.SendSessionInfo(snapshot.SessionInfo)
		}
		return true
	}
	broker.SendSnapshot(snapshot)
	return false
}

func StopSharedTunnelGracefully(sess *tunnel.Session, broker *tunnel.Broker, timeout time.Duration) {
	if broker != nil {
		broker.StopSharingGracefully(timeout)
		return
	}
	if sess != nil {
		sess.DestroyGracefully(timeout)
	}
}

func ShareSnapshotMatches(a, b tunnel.BrokerSnapshot) bool {
	if a.SessionInfo != b.SessionInfo || a.Status != b.Status || a.Activity != b.Activity {
		return false
	}
	if len(a.History) != len(b.History) || len(a.ExtraEvents) != len(b.ExtraEvents) {
		return false
	}
	for i := range a.History {
		if a.History[i] != b.History[i] {
			return false
		}
	}
	for i := range a.ExtraEvents {
		if a.ExtraEvents[i].Type != b.ExtraEvents[i].Type || a.ExtraEvents[i].StreamID != b.ExtraEvents[i].StreamID || string(a.ExtraEvents[i].Data) != string(b.ExtraEvents[i].Data) {
			return false
		}
	}
	return true
}
