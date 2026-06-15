package agentruntime

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
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
	// If we already have projected events, hydrate from session ledger
	// to fill any gaps.
	if store == nil || ses == nil || !ses.TunnelEventsComplete || len(ses.TunnelEvents) == 0 {
		// Fallback: if replay is empty and session has messages, build
		// replay from session message history. This handles old sessions
		// that were never projected through the tunnel system.
		if len(replay) == 0 && ses != nil && len(ses.Messages) > 0 {
			built := BuildReplayFromMessages(ses.ID, ses.Messages)
			if len(built) > 0 {
				return built, nil
			}
		}
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

// BuildReplayFromMessages converts session message history into tunnel replay
// events. This is used when sharing sessions that predate the tunnel event
// projection system — their conversation lives in ses.Messages, not in
// the projection store. The replay events are sent to the mobile client
// and also persisted to the projection store for future shares.
func BuildReplayFromMessages(sessionID string, messages []provider.Message) []tunnel.GatewayMessage {
	var events []tunnel.GatewayMessage
	seq := 0

	for _, msg := range messages {
		for _, block := range msg.Content {
			seq++
			eventID := fmt.Sprintf("replay-%08d", seq)
			role := msg.Role

			switch block.Type {
			case "text":
				data, _ := json.Marshal(map[string]any{
					"text": block.Text,
					"id":   fmt.Sprintf("msg-%d", seq),
					"done": true,
				})
				events = append(events, tunnel.GatewayMessage{
					SessionID: sessionID,
					EventID:   eventID,
					Type:      mapTextEventType(role),
					Data:      data,
				})

			case "tool_use":
				argsStr := ""
				if len(block.Input) > 0 {
					argsStr = string(block.Input)
				}
				data, _ := json.Marshal(map[string]any{
					"id":   block.ToolID,
					"name": block.ToolName,
					"args": argsStr,
				})
				events = append(events, tunnel.GatewayMessage{
					SessionID: sessionID,
					EventID:   eventID,
					Type:      "tool_call_done",
					Data:      data,
				})

			case "tool_result":
				data, _ := json.Marshal(map[string]any{
					"id":      block.ToolID,
					"payload": block.Output,
				})
				events = append(events, tunnel.GatewayMessage{
					SessionID: sessionID,
					EventID:   eventID,
					Type:      "tool_result",
					Data:      data,
				})

			case "image":
				// Skip images in replay — they'd be too large
			}

			// Handle reasoning content on text blocks
			if block.Type == "text" && block.ReasoningContent != "" && role == "assistant" {
				rdata, _ := json.Marshal(map[string]any{
					"text": block.ReasoningContent,
					"id":   fmt.Sprintf("reasoning-%d", seq),
					"done": true,
				})
				events = append(events, tunnel.GatewayMessage{
					SessionID: sessionID,
					EventID:   fmt.Sprintf("replay-r-%08d", seq),
					Type:      "reasoning",
					Data:      rdata,
				})
			}
		}
	}

	return events
}

func mapTextEventType(role string) string {
	switch role {
	case "user":
		return "user_message"
	case "assistant":
		return "text"
	default:
		return "system_message"
	}
}
