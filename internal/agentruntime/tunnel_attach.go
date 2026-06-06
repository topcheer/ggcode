package agentruntime

import "github.com/topcheer/ggcode/internal/tunnel"

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
	broker.SetReplayProvider(cfg.ReplayProvider)
	broker.SetEventRecorder(nil)
	if cfg.SessionID != "" {
		broker.BindSession(cfg.SessionID)
		if cfg.AuthorityEpoch == 0 {
			cfg.AuthorityEpoch = 1
		}
		broker.SetAuthorityEpoch(cfg.AuthorityEpoch)
		broker.AnnounceActiveSession(cfg.SessionID)
	}
	if cfg.SessionInfo != nil {
		broker.SendSessionInfo(*cfg.SessionInfo)
	}
	if cfg.Status != nil && cfg.Status.Status != "" {
		broker.PushStatus(cfg.Status.Status, cfg.Status.Message)
	}
	if cfg.Activity != nil {
		broker.PushActivity(*cfg.Activity)
	}
}
