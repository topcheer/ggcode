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
	broker.SwitchSession(ses.ID)
	if store != nil {
		epoch, replay, err := PrepareProjectionReplay(store, ses)
		if err != nil {
			return state, err
		}
		state.AuthorityEpoch = epoch
		state.Replay = replay
		if len(replay) > 0 {
			broker.PrimeEventIDs(replay)
		}
	}
	broker.SetAuthorityEpoch(state.AuthorityEpoch)
	broker.SetEventRecorder(recorder)
	return state, nil
}

func HydrateProjectionReplayFromSessionLedger(store *tunnel.ProjectionStore, ses *session.Session, replay []tunnel.GatewayMessage) ([]tunnel.GatewayMessage, error) {
	if store == nil || ses == nil || !ses.TunnelEventsComplete || len(ses.TunnelEvents) == 0 {
		return replay, nil
	}
	sessionID := strings.TrimSpace(ses.ID)
	if sessionID == "" {
		return replay, nil
	}

	seen := make(map[string]struct{}, len(replay))
	for _, msg := range replay {
		if msg.EventID != "" {
			seen[msg.EventID] = struct{}{}
		}
	}

	appended := false
	for _, ev := range ses.TunnelEvents {
		if ev.EventID != "" {
			if _, ok := seen[ev.EventID]; ok {
				continue
			}
			seen[ev.EventID] = struct{}{}
		}
		if err := store.Append(tunnel.GatewayMessage{
			SessionID: sessionID,
			EventID:   ev.EventID,
			StreamID:  ev.StreamID,
			Type:      ev.Type,
			Data:      append([]byte(nil), ev.Data...),
		}); err != nil {
			return replay, err
		}
		appended = true
	}
	if !appended {
		return replay, nil
	}
	return store.ReplayEvents(sessionID)
}

func AppendProjectionEvent(store *tunnel.ProjectionStore, msg tunnel.GatewayMessage) error {
	if store == nil {
		return nil
	}
	return store.Append(msg)
}
