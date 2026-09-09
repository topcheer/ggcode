package agentruntime

import (
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tunnel"
)

type TunnelAttachConfig struct {
	ReplayProvider func() []tunnel.GatewayMessage
	SessionID      string
	AuthorityEpoch uint64
	SessionInfo    *tunnel.SessionInfoData
	Status         *tunnel.StatusData
	Activity       *string
}

func AttachTunnelBroker(broker *tunnel.Broker, cfg TunnelAttachConfig) {
	if broker == nil {
		return
	}
	// #1497 case C: callers passing a nil ReplayProvider used to wipe
	// BOTH history channels (replay set to nil AND the event recorder
	// cleared) - newly attached clients got zero replay with no log.
	// Keep the existing recorder when there is nothing to replay from.
	if cfg.ReplayProvider != nil {
		broker.SetReplayProvider(cfg.ReplayProvider)
	} else {
		debug.Log("agentruntime", "AttachTunnelBroker: nil ReplayProvider; keeping existing recorder")
	}
	if cfg.SessionInfo != nil {
		broker.SendSessionInfo(*cfg.SessionInfo)
	}
	if cfg.SessionID != "" {
		broker.BindSession(cfg.SessionID)
		if cfg.AuthorityEpoch == 0 {
			cfg.AuthorityEpoch = 1
		}
		broker.SetAuthorityEpoch(cfg.AuthorityEpoch)
		broker.AnnounceActiveSession(cfg.SessionID)
	}
	if cfg.Status != nil && cfg.Status.Status != "" {
		broker.PushStatus(cfg.Status.Status, cfg.Status.Message)
	}
	if cfg.Activity != nil {
		broker.PushActivity(*cfg.Activity)
	}
}
