package agentruntime

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

type TunnelHost struct {
	mu sync.Mutex

	projBroker *tunnel.Broker
	projStore  *tunnel.ProjectionStore
	projBroken bool

	onlineBroker *tunnel.Broker

	currentMsgID  string
	needsFinalize bool
	pushMu        sync.Mutex
	finalized     map[string]bool

	session      *session.Session
	sessionStore session.Store

	sessionInfo tunnel.SessionInfoData

	activeShare *tunnelSessionRef

	upgradeMgr *tunnel.UpgradeManager

	p2pFactory tunnel.PeerFactory
	p2pConfig  tunnel.UpgradeConfig
}

func NewTunnelHost() *TunnelHost {
	return &TunnelHost{
		projBroker: tunnel.NewBroker(nil),
	}
}

func (h *TunnelHost) SetP2PEnabled(factory tunnel.PeerFactory, cfg tunnel.UpgradeConfig) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.p2pFactory = factory
	h.p2pConfig = cfg
}

func (h *TunnelHost) handleRTCSignal(cmd tunnel.GatewayMessage) {
	h.mu.Lock()
	mgr := h.upgradeMgr
	h.mu.Unlock()
	if mgr == nil {
		return
	}
	signal, ok := tunnel.DecodeSignalMessage(cmd)
	if !ok {
		return
	}
	mgr.HandleSignalMessage(signal)
}

func (h *TunnelHost) BindSession(ses *session.Session, store session.Store) ProjectionBrokerState {

	h.pushMu.Lock()
	defer h.pushMu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.session = ses
	h.sessionStore = store
	h.currentMsgID = ""

	h.finalized = nil
	h.needsFinalize = false

	if ses == nil || strings.TrimSpace(ses.ID) == "" {
		return ProjectionBrokerState{}
	}

	if h.projStore == nil {
		if s, err := tunnel.NewDefaultProjectionStore(); err == nil {
			h.projStore = s
		} else {

			debug.Log("tunnel-host", "BindSession: projection store unavailable: %v (mobile replay will be empty)", err)
			h.projBroken = true
		}
	}

	if h.projStore != nil {
		h.projBroken = false
	}

	state, perr := PrepareProjectionBroker(h.projBroker, h.projStore, ses, func(ev tunnel.GatewayMessage) {
		h.recordEvent(ev)
	})
	if perr != nil {
		debug.Log("tunnel-host", "BindSession: PrepareProjectionBroker failed: %v (session=%s replay empty, epoch=1, events not persisted until next bind)", perr, ses.ID)
	}
	return state
}

type ShareConfig struct {
	Workspace string
	Model     string
	Provider  string
	Mode      string
	Version   string
	Language  string

	SnapshotProvider func() tunnel.BrokerSnapshot

	OnCommand func(cmd tunnel.GatewayMessage)

	OnConnected func(info tunnel.RelayConnectedState)

	ClientTag string
}

type ShareResult struct {
	ConnectURL string
	QRCode     string
	QRCodePNG  []byte
	Token      string
	RoomID     string

	Broker *tunnel.Broker

	Session *tunnel.Session
}

type tunnelSessionRef struct {
	session *tunnel.Session
	broker  *tunnel.Broker
}

func (h *TunnelHost) StartShare(cfg ShareConfig) (*ShareResult, error) {
	if cfg.Workspace == "" {
		return nil, fmt.Errorf("tunnel: workspace is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clientTag := cfg.ClientTag
	if clientTag == "" {
		clientTag = "ggcode"
	}
	sess := tunnel.NewSession(tunnel.DefaultRelayURL, tunnel.WithClientMetadata(clientTag, cfg.Version))
	info, err := sess.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("tunnel start: %w", err)
	}

	broker := tunnel.NewBroker(sess)

	userHandler := cfg.OnCommand
	broker.OnCommand(func(cmd tunnel.GatewayMessage) {
		if tunnel.IsRTCSignalMessage(cmd) {
			h.handleRTCSignal(cmd)
			return
		}
		if userHandler != nil {
			userHandler(cmd)
		}
	})
	if cfg.SnapshotProvider != nil {
		broker.SetSnapshotProvider(cfg.SnapshotProvider)
	}

	h.AttachOnlineBroker(broker)

	h.SetSessionInfo(tunnel.SessionInfoData{
		Workspace: cfg.Workspace,
		Model:     cfg.Model,
		Provider:  cfg.Provider,
		Mode:      cfg.Mode,
		Version:   cfg.Version,
		Language:  cfg.Language,
	})

	h.PrepareOnlineShare(broker)

	h.mu.Lock()

	if oldRef := h.activeShare; oldRef != nil {
		h.activeShare = nil
		oldMgr := h.upgradeMgr
		h.upgradeMgr = nil
		h.mu.Unlock()
		h.teardownShareRef(oldRef, oldMgr)
		h.mu.Lock()
	}
	h.activeShare = &tunnelSessionRef{session: sess, broker: broker}
	var p2pMgr *tunnel.UpgradeManager
	if h.p2pFactory != nil && h.p2pConfig.Enabled {
		p2pMgr = tunnel.NewUpgradeManager(broker, h.p2pFactory, h.p2pConfig)
		h.upgradeMgr = p2pMgr
	}
	h.mu.Unlock()

	onConnected := cfg.OnConnected
	broker.OnRelayConnected(func(info tunnel.RelayConnectedState) {
		if onConnected != nil {
			onConnected(info)
		}
		if info.Role == "client" && p2pMgr != nil {

			broker.SetP2PNegotiating(true)
			p2pMgr.Restart()
		}
	})

	return &ShareResult{
		ConnectURL: info.ConnectURL,
		QRCode:     info.QRCode,
		QRCodePNG:  info.QRCodePNG,
		Token:      info.Token,
		RoomID:     info.RoomID,
		Broker:     broker,
		Session:    sess,
	}, nil
}

func (h *TunnelHost) StopShare() {
	h.mu.Lock()
	ref := h.activeShare
	h.activeShare = nil
	mgr := h.upgradeMgr
	h.upgradeMgr = nil
	h.mu.Unlock()

	if mgr != nil {
		mgr.Stop()
	}

	h.DetachOnlineBroker()

	if ref != nil {
		if ref.broker != nil {
			ref.broker.Stop()
		}
		if ref.session != nil {
			ref.session.Stop()
		}
	}
}

func (h *TunnelHost) GetShareInfo() *ShareResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.activeShare == nil || h.activeShare.session == nil {
		return nil
	}
	info := h.activeShare.session.Info()
	if info == nil {
		return nil
	}
	return &ShareResult{
		ConnectURL: info.ConnectURL,
		QRCode:     info.QRCode,
		QRCodePNG:  info.QRCodePNG,
		Token:      info.Token,
		RoomID:     info.RoomID,
	}
}

// ShareStatusSnapshot webui /api/tunnel/status 的 share 面数据源(daemon
// 模式无 TUI Model,由 TunnelHost 直出)。字段语义与 TUI TunnelStatusSnapshot
// 对齐:active=有活跃 share session;connected=已接入 relay;qr/png 供前端渲染。
func (h *TunnelHost) ShareStatusSnapshot() map[string]any {
	out := map[string]any{"active": false, "starting": false, "connected": false}
	h.mu.Lock()
	ref := h.activeShare
	h.mu.Unlock()
	if ref == nil || ref.session == nil {
		return out
	}
	out["active"] = true
	if info := ref.session.Info(); info != nil {
		out["connect_url"] = info.ConnectURL
		if len(info.QRCodePNG) > 0 {
			out["qrcode_png_b64"] = base64.StdEncoding.EncodeToString(info.QRCodePNG)
		}
	}
	if ref.session.ConnectedState() != nil {
		out["connected"] = true
	}
	return out
}

func (h *TunnelHost) AttachOnlineBroker(broker *tunnel.Broker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onlineBroker = broker
}

func (h *TunnelHost) SetSessionInfo(info tunnel.SessionInfoData) {
	h.mu.Lock()

	if info.Title == "" && h.session != nil {
		info.Title = h.session.Title
	}
	h.sessionInfo = info
	h.mu.Unlock()
}

func (h *TunnelHost) PrepareOnlineShare(broker *tunnel.Broker) {
	h.mu.Lock()
	ses := h.session
	projStore := h.projStore
	broken := h.projBroken
	info := h.sessionInfo
	h.mu.Unlock()

	if broker == nil || ses == nil || strings.TrimSpace(ses.ID) == "" {
		return
	}

	if info.Title == "" && ses.Title != "" {
		info.Title = ses.Title
		h.mu.Lock()
		h.sessionInfo = info
		h.mu.Unlock()
	}

	broker.SetEventRecorder(nil)

	broker.SendSessionInfo(info)

	broker.BindSession(ses.ID)

	broker.SetReplayProvider(func() []tunnel.GatewayMessage {
		return h.TunnelEvents()
	})

	if projStore != nil && !broken {
		if epoch, err := ProjectionAuthorityEpoch(projStore, ses.ID); err == nil && epoch > 0 {
			broker.SetAuthorityEpoch(epoch)
		}
	}

	replay := h.TunnelEvents()
	if len(replay) > 0 {
		broker.ReplayEvents(replay, false)
	} else {
		broker.SendSnapshotFromProvider()
	}

	broker.SendSessionInfo(info)

	broker.AnnounceActiveSession(ses.ID)
}

func (h *TunnelHost) DetachOnlineBroker() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onlineBroker = nil
}

func (h *TunnelHost) teardownShareRef(oldRef *tunnelSessionRef, oldMgr *tunnel.UpgradeManager) {
	if oldMgr != nil {
		oldMgr.Stop()
	}
	if oldRef.broker != nil {
		oldRef.broker.Stop()
	}

	if oldRef.session != nil {
		oldRef.session.Stop()
	}
	h.mu.Lock()
	if h.onlineBroker == oldRef.broker {
		h.onlineBroker = nil
	}
	h.mu.Unlock()
}

func (h *TunnelHost) Close() {
	h.mu.Lock()
	online := h.onlineBroker
	ref := h.activeShare

	proj := h.projBroker

	mgr := h.upgradeMgr
	h.upgradeMgr = nil
	h.onlineBroker = nil
	h.projBroker = nil
	h.projStore = nil
	h.activeShare = nil
	h.session = nil
	h.sessionStore = nil
	h.mu.Unlock()

	if mgr != nil {
		mgr.Stop()
	}

	if online != nil {
		online.StopSharingGracefully(2 * time.Second)
	}

	if ref != nil && ref.session != nil {
		ref.session.Stop()
	}

	if ref != nil && ref.broker != nil {
		ref.broker.Stop()
	}

	if proj != nil {
		proj.Stop()
	}
}

func (h *TunnelHost) OnlineBroker() *tunnel.Broker {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.onlineBroker
}

func (h *TunnelHost) ProjectionBroker() *tunnel.Broker {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.projBroker
}

func (h *TunnelHost) ProjectionStore() *tunnel.ProjectionStore {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.projStore
}

func (h *TunnelHost) ResetStreamState() {

	h.pushMu.Lock()
	defer h.pushMu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.currentMsgID = ""

	h.finalized = nil
	h.needsFinalize = false
}

func (h *TunnelHost) AuthorityEpoch() uint64 {
	h.mu.Lock()
	store := h.projStore
	ses := h.session
	h.mu.Unlock()
	if store == nil || ses == nil {
		return 1
	}
	if epoch, err := ProjectionAuthorityEpoch(store, ses.ID); err == nil {
		return epoch
	}
	return 1
}

func (h *TunnelHost) TunnelEvents() []tunnel.GatewayMessage {
	h.mu.Lock()
	store := h.projStore
	ses := h.session
	broken := h.projBroken
	h.mu.Unlock()
	if store == nil || ses == nil || broken {
		return nil
	}
	events, err := ProjectionReplay(store, ses.ID)
	if err != nil {

		debug.Log("tunnel-host", "ProjectionReplay failed: %v (session=%s) - projection circuit-broken; snapshot+replay both empty", err, ses.ID)
		h.mu.Lock()
		h.projBroken = true
		h.mu.Unlock()
		return nil
	}
	return events
}

func (h *TunnelHost) PushStreamEvent(ev provider.StreamEvent) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker == nil {
		return
	}

	switch ev.Type {
	case provider.StreamEventText:
		h.markActive()
		h.pushTextEvent(broker, ev.Text)

	case provider.StreamEventReasoning:
		if chunk := tunnel.NormalizeReasoningChunk(ev.Text); chunk != "" {
			h.markActive()
			h.pushReasoningEvent(broker, chunk)
		}

	case provider.StreamEventToolCallDone:
		h.rollover(broker, true)
		name := strings.TrimSpace(ev.Tool.Name)
		if name == "" {
			name = "tool"
		}

		broker.PushToolCall(ev.Tool.ID, name, "", string(ev.Tool.Arguments), "")

	case provider.StreamEventToolResult:
		h.rollover(broker, false)
		content := ev.Result

		rawArgs := ""
		if ev.Tool.Arguments != nil {
			rawArgs = string(ev.Tool.Arguments)
		}
		if pres, ok := toolpkg.DescribeToolResult(ev.Tool.Name, rawArgs, content, ev.IsError); ok && pres.Payload != "" {
			content = pres.Payload
		}
		if len([]rune(content)) > 2000 {
			content = string([]rune(content)[:1997]) + "\n..."
		}
		broker.PushToolResult(ev.Tool.ID, ev.Tool.Name, content, ev.IsError)

	case provider.StreamEventDone:
		h.rollover(broker, true)

	case provider.StreamEventError:
		h.rollover(broker, true)
		if ev.Error != nil {
			broker.PushError(provider.UserFacingError(ev.Error))
		}
	}
}

func (h *TunnelHost) PushUserMessage(text string) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker == nil {
		return
	}
	broker.PushUserMessage(text)
}

func (h *TunnelHost) PushUserMessageData(data tunnel.MessageData) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker == nil {
		return
	}
	broker.PushUserMessageData(data)
}

func (h *TunnelHost) PushStatus(status, message string) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker == nil {
		return
	}
	broker.PushStatus(status, message)
}

func (h *TunnelHost) PushActivity(activity string) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker == nil {
		return
	}
	broker.PushActivity(activity)
}

func (h *TunnelHost) pushTextEvent(broker *tunnel.Broker, text string) {
	h.pushMu.Lock()
	defer h.pushMu.Unlock()
	h.mu.Lock()
	id := h.currentMsgID
	if id == "" || h.finalized[id] {
		id = broker.NextMessageID()
		h.currentMsgID = id
	}
	h.mu.Unlock()
	broker.PushReasoningDone(TunnelReasoningMsgID(id))
	broker.PushText(id, text)
}

func (h *TunnelHost) pushReasoningEvent(broker *tunnel.Broker, chunk string) {
	h.pushMu.Lock()
	defer h.pushMu.Unlock()
	h.mu.Lock()
	id := h.currentMsgID
	if id == "" || h.finalized[id] {
		id = broker.NextMessageID()
		h.currentMsgID = id
	}
	h.mu.Unlock()
	broker.PushReasoning(TunnelReasoningMsgID(id), chunk)
}

func (h *TunnelHost) markActive() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.needsFinalize = true
}

func (h *TunnelHost) rollover(broker *tunnel.Broker, startNew bool) {

	h.pushMu.Lock()
	defer h.pushMu.Unlock()
	h.mu.Lock()
	msgID := h.currentMsgID
	finalize := h.needsFinalize
	h.needsFinalize = false
	if broker != nil && msgID != "" && finalize {
		if h.finalized == nil {
			h.finalized = make(map[string]bool)
		}
		h.finalized[msgID] = true
	}
	if startNew && broker != nil {
		h.currentMsgID = broker.NextMessageID()
	}
	h.mu.Unlock()
	if broker == nil || msgID == "" {
		return
	}
	if finalize {
		broker.PushTextDone(msgID)
	}
}

func (h *TunnelHost) recordEvent(ev tunnel.GatewayMessage) {

	h.mu.Lock()
	projStore := h.projStore
	ses := h.session
	h.mu.Unlock()

	if ev.EventID == "" || ev.Type == tunnel.EventSnapshotReset {
		return
	}

	if projStore != nil {
		if err := AppendProjectionEvent(projStore, ev); err != nil {
			debug.Log("tunnel-host", "AppendProjectionEvent failed: %v - projection circuit-broken (TunnelEvents now nil; snapshot+replay both empty)", err)
			h.mu.Lock()
			h.projBroken = true
			h.mu.Unlock()
		}
	}

	if ses != nil {
		record := session.TunnelEvent{
			EventID:  ev.EventID,
			StreamID: ev.StreamID,
			Type:     ev.Type,
			Data:     append([]byte(nil), ev.Data...),
		}

		ses.TunnelEventsMu.Lock()
		ses.TunnelEvents = append(ses.TunnelEvents, record)
		if len(ses.TunnelEvents) > session.MaxTunnelEvents {
			pruneIdx := len(ses.TunnelEvents) - session.MaxTunnelEvents
			ses.TunnelEvents = ses.TunnelEvents[pruneIdx:]
		}
		ses.TunnelEventsMu.Unlock()
	}

	h.mu.Lock()
	online := h.onlineBroker
	h.mu.Unlock()
	if online != nil {
		online.PublishRecordedEvent(ev)
	}
}
