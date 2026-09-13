package agentruntime

import (
	"strings"

	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tunnel"
)

type ProjectionBrokerState struct {
	AuthorityEpoch uint64
	Replay         []tunnel.GatewayMessage
}

func ProjectionAuthorityEpoch(store *tunnel.ProjectionStore, sessionID string) (uint64, error) {
	sessionID = strings.TrimSpace(sessionID)
	if store == nil || sessionID == "" {
		return 1, nil
	}
	epoch, err := store.AuthorityEpoch(sessionID)
	if err != nil {
		return 1, err
	}
	if epoch == 0 {
		return 1, nil
	}
	return epoch, nil
}

func ProjectionReplay(store *tunnel.ProjectionStore, sessionID string) ([]tunnel.GatewayMessage, error) {
	sessionID = strings.TrimSpace(sessionID)
	if store == nil || sessionID == "" {
		return nil, nil
	}
	return store.ReplayEvents(sessionID)
}

func PrepareProjectionReplay(store *tunnel.ProjectionStore, ses *session.Session) (uint64, []tunnel.GatewayMessage, error) {
	if ses == nil || strings.TrimSpace(ses.ID) == "" {
		return 1, nil, nil
	}
	epoch, err := ProjectionAuthorityEpoch(store, ses.ID)
	if err != nil {
		return 1, nil, err
	}
	replay, err := ProjectionReplay(store, ses.ID)
	if err != nil {
		return epoch, nil, err
	}
	replay, err = HydrateProjectionReplayFromSessionLedger(store, ses, replay)
	if err != nil {
		return epoch, replay, err
	}
	return epoch, replay, nil
}

func PrepareProjectionBroker(broker *tunnel.Broker, store *tunnel.ProjectionStore, ses *session.Session, recorder func(tunnel.GatewayMessage)) (ProjectionBrokerState, error) {
	state := ProjectionBrokerState{AuthorityEpoch: 1}
	if broker == nil || ses == nil || strings.TrimSpace(ses.ID) == "" {
		return state, nil
	}
	// #1493-B: replay-then-switch. SwitchSession used to run FIRST, so a
	// transient PrepareProjectionReplay failure (disk full/permission/corrupt
	// store) left a HALF-SWITCHED broker: the new session was active but the
	// authority epoch stayed 1 and no event recorder was bound - stale-epoch
	// events were refused by the store guard (or worse, a residual HIGHER
	// epoch from a previous session persisted with the wrong value) and new
	// events silently stopped persisting. Everything that can fail now
	// happens before the first mutating call.
	if store != nil {
		epoch, replay, err := PrepareProjectionReplay(store, ses)
		if err != nil {
			return state, err
		}
		state.AuthorityEpoch = epoch
		state.Replay = replay
		broker.SwitchSession(ses.ID)
		if len(replay) > 0 {
			broker.PrimeEventIDs(replay)
		}
	} else {
		broker.SwitchSession(ses.ID)
	}
	broker.SetAuthorityEpoch(state.AuthorityEpoch)
	broker.SetEventRecorder(recorder)
	return state, nil
}

// HydrateProjectionReplayFromSessionLedger is deprecated.
// Tunnel events are no longer stored in session JSONL — the projection store
// is the sole source. This function is now a no-op, retained for API compatibility.
func HydrateProjectionReplayFromSessionLedger(store *tunnel.ProjectionStore, ses *session.Session, replay []tunnel.GatewayMessage) ([]tunnel.GatewayMessage, error) {
	return replay, nil
}

func AppendProjectionEvent(store *tunnel.ProjectionStore, msg tunnel.GatewayMessage) error {
	if store == nil {
		return nil
	}
	return store.Append(msg)
}
