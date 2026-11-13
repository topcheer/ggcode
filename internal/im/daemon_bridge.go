package im

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/webui"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/daemon"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

type pendingAskUser struct {
	request     toolpkg.AskUserRequest
	response    chan toolpkg.AskUserResponse
	multiSelect bool
	msgIDs      map[string]string
}

type pendingInterruption struct {
	Content []provider.ContentBlock
}

type approvalReply struct {
	Decision permission.Decision
	Always   bool
}

const staleReplySuppressWindow = 5 * time.Second

type DaemonBridge struct {
	manager         *Manager
	emitter         *IMEmitter
	agent           *agent.Agent
	store           session.Store
	sess            *session.Session
	language        string
	workingDir      string
	usageTurnIndex  int
	metricCollector *metrics.Collector
	metricCancel    context.CancelFunc

	mu                      sync.Mutex
	cancelFunc              context.CancelFunc
	pendingAsk              *pendingAskUser
	pendingApproval         chan approvalReply
	staleReplySuppressUntil time.Time
	pendingInterruptions    []pendingInterruption
	interactiveMsgIDs       map[string]string
	multiSelectChosen       map[string]bool
	followSink              daemon.FollowSink
	onActivity              func()
	onRunStateChange        func(bool)
	onUserMessage           func([]provider.ContentBlock)
	onRestart               func()
	onProviderSwitch        func(vendor, endpoint, model string) (string, error)
	visionTurnSelect        func() string
	visionTurnSwitch        func(model string) error
	restartDebug            bool
	eventSubs               []*daemonBridgeSub
	eventSubMu              sync.RWMutex
	cascadeCancel           func()
	emitTextOverride        func(string) error
	remoteDangerousOverride *bool

	// /et (etunnel) IM-inbound support: host-injected handles (daemon wires
	// these to its webui server + TunnelCtl; TUI-hosted IM uses tuiSlashDeps).
	etCtl      webui.EtunnelController
	etLocalURL string
	etConfig   func() (subdomain, token, plan string)
	etSave     func(subdomain, token string) error
}

// SetEtunnel wires the /et IM command to the host tunnel controller.
func (b *DaemonBridge) SetEtunnel(ctl webui.EtunnelController, localURL string, cfgFn func() (subdomain, token, plan string), saveFn func(subdomain, token string) error) {
	b.etCtl = ctl
	b.etLocalURL = localURL
	b.etConfig = cfgFn
	b.etSave = saveFn
}

func (b *DaemonBridge) EtunnelControl(args []string) (string, error) {
	var sub, tok, pln string
	if b.etConfig != nil {
		sub, tok, pln = b.etConfig()
	}
	return webui.ControlEt(b.etCtl, b.etLocalURL, sub, tok, pln, b.etSave, args)
}

func NewDaemonBridge(mgr *Manager, ag *agent.Agent, emitter *IMEmitter, store session.Store, sess *session.Session) *DaemonBridge {
	b := &DaemonBridge{
		manager:  mgr,
		agent:    ag,
		emitter:  emitter,
		store:    store,
		sess:     sess,
		language: emitter.Language(),
	}

	if ag != nil {
		b.workingDir = ag.WorkingDir()
	}
	if sess != nil {
		b.usageTurnIndex = daemonSessionTurnIndex(sess)
	}

	if mgr != nil {
		mgr.SetInteractiveCallback(b.handleInteractiveCallback)
	}

	if ag != nil {
		ag.SetApprovalHandler(b.handleApproval)
		if store != nil && sess != nil {
			collectorCtx, collectorCancel := context.WithCancel(context.Background())
			b.metricCancel = collectorCancel
			b.metricCollector = metrics.NewCollector(collectorCtx, 256, func(ev metrics.MetricEvent) {
				b.recordMetric(ev)
			})
			ag.SetMetricHandler(b.metricCollector.Emit)
		}
	}
	return b
}

func (b *DaemonBridge) handleInteractiveCallback(cb InteractiveCallback) {
	b.mu.Lock()
	pending := b.pendingAsk
	b.mu.Unlock()
	if pending == nil {
		return
	}

	b.mu.Lock()
	expectedMsgID := pending.msgIDs[cb.Adapter]
	lastEmitted := b.interactiveMsgIDs[cb.Adapter]
	b.mu.Unlock()
	if expectedMsgID != "" {
		if cb.MessageID != expectedMsgID {
			debug.Log("im", "dropping stale interactive callback: adapter=%s messageID=%q expected=%q",
				cb.Adapter, cb.MessageID, expectedMsgID)
			return
		}
	} else if lastEmitted != "" {
		debug.Log("im", "dropping stale interactive callback: adapter=%s messageID=%q - current question is text-only, no button was emitted for it",
			cb.Adapter, cb.MessageID)
		return
	}

	choice := ""
	if len(cb.Values) > 0 {
		choice = cb.Values[0]
	}

	if pending.multiSelect && choice != "__done__" {

		b.mu.Lock()
		if b.multiSelectChosen == nil {
			b.multiSelectChosen = make(map[string]bool)
		}
		if b.multiSelectChosen[choice] {
			delete(b.multiSelectChosen, choice)
		} else {
			b.multiSelectChosen[choice] = true
		}
		chosen := b.multiSelectChosen
		b.mu.Unlock()
		debug.Log("im", "multi-select toggle: choice=%s selected=%v", choice, chosen)
		return
	}

	var values []string
	if pending.multiSelect {

		b.mu.Lock()
		for v := range b.multiSelectChosen {
			values = append(values, v)
		}
		b.multiSelectChosen = nil
		b.mu.Unlock()
	} else {
		values = cb.Values
	}

	if len(values) == 0 {
		return
	}

	text := strings.Join(values, ",")
	resp := BuildAskUserResponseFromText(pending.request, text)
	select {
	case pending.response <- resp:
	default:
	}

	b.mu.Lock()
	if b.pendingAsk == pending {
		b.pendingAsk = nil
	}

	b.mu.Unlock()
}

func (b *DaemonBridge) SetFollowSink(sink daemon.FollowSink) {
	b.mu.Lock()
	b.followSink = sink
	b.mu.Unlock()
}

func (b *DaemonBridge) SetActivityHook(fn func()) {
	b.mu.Lock()
	b.onActivity = fn
	b.mu.Unlock()
}

func (b *DaemonBridge) SetRunStateHook(fn func(bool)) {
	b.mu.Lock()
	b.onRunStateChange = fn
	b.mu.Unlock()
}

func (b *DaemonBridge) SetUserMessageHook(fn func([]provider.ContentBlock)) {
	b.mu.Lock()
	b.onUserMessage = fn
	b.mu.Unlock()
}

func (b *DaemonBridge) SetRestartHook(fn func()) {
	b.mu.Lock()
	b.onRestart = fn
	b.mu.Unlock()
}

func (b *DaemonBridge) SetVisionTurnHook(selectFn func() string, switchFn func(model string) error) {
	b.mu.Lock()
	b.visionTurnSelect = selectFn
	b.visionTurnSwitch = switchFn
	b.mu.Unlock()
}

func (b *DaemonBridge) beginVisionTurn(content []provider.ContentBlock) (func(), bool) {
	hasImage := false
	for _, c := range content {
		if c.Type == "image" {
			hasImage = true
			break
		}
	}
	b.mu.Lock()
	sel := b.visionTurnSelect
	sw := b.visionTurnSwitch
	ag := b.agent
	userModel := ""
	if b.sess != nil {
		userModel = b.sess.Model
	}
	b.mu.Unlock()
	if !hasImage {
		return func() {}, false
	}
	if sel == nil || sw == nil {
		debug.Log("daemon-bridge", "vision turn: hooks not installed - degrading to text-only")
		return func() {}, false
	}
	if ag == nil || ag.SupportsVision() {
		return func() {}, false
	}
	vm := sel()
	if vm == "" {
		debug.Log("daemon-bridge", "vision turn: no comparable vision model on endpoint (user model: %s) - degrading to text-only", userModel)
		return func() {}, false
	}
	if vm == userModel {
		return func() {}, false
	}
	if err := sw(vm); err != nil {
		debug.Log("daemon-bridge", "vision turn switch to %s failed: %v - degrading to text-only", vm, err)
		return func() {}, false
	}
	debug.Log("daemon-bridge", "vision turn: switched to %s for this turn (user model: %s)", vm, userModel)
	var once bool
	return func() {
		if once {
			return
		}
		once = true
		if userModel == "" {
			return
		}
		if err := sw(userModel); err != nil {
			debug.Log("daemon-bridge", "vision turn restore to %s failed: %v", userModel, err)
		}
	}, true
}

func stripImageBlocks(content []provider.ContentBlock) []provider.ContentBlock {
	out := make([]provider.ContentBlock, 0, len(content))
	for _, c := range content {
		if c.Type != "image" {
			out = append(out, c)
		}
	}
	return out
}

func contentEffectivelyEmpty(content []provider.ContentBlock) bool {
	if len(content) == 0 {
		return true
	}
	for _, c := range content {
		if c.Type != "text" || strings.TrimSpace(c.Text) != "" {
			return false
		}
	}
	return true
}

func contentHasImageBlocks(content []provider.ContentBlock) bool {
	for _, c := range content {
		if c.Type == "image" {
			return true
		}
	}
	return false
}

func (b *DaemonBridge) SetProviderSwitchHook(fn func(vendor, endpoint, model string) (string, error)) {
	b.mu.Lock()
	b.onProviderSwitch = fn
	b.mu.Unlock()
}

func (b *DaemonBridge) ConsumeRestartDebug() bool {
	b.mu.Lock()
	v := b.restartDebug
	b.restartDebug = false
	b.mu.Unlock()
	return v
}

func (b *DaemonBridge) HasActiveRun() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cancelFunc != nil
}

func (b *DaemonBridge) InterruptActiveRun() bool {
	b.mu.Lock()
	cancel := b.cancelFunc
	cascade := b.cascadeCancel
	b.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()

	if cascade != nil {
		cascade()
	}
	return true
}

func (b *DaemonBridge) SetCascadeCancel(fn func()) {
	b.mu.Lock()
	b.cascadeCancel = fn
	b.mu.Unlock()
}

func (b *DaemonBridge) notifyRunStateChange(busy bool) {
	b.mu.Lock()
	fn := b.onRunStateChange
	b.mu.Unlock()
	if fn != nil {
		fn(busy)
	}
}

func (b *DaemonBridge) notifyUserMessage(content []provider.ContentBlock) {
	b.mu.Lock()
	fn := b.onUserMessage
	b.mu.Unlock()
	if fn != nil {
		fn(content)
	}
}

func (b *DaemonBridge) tryQueueOrBeginRun(content []provider.ContentBlock, logPrefix string) (context.Context, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancelFunc != nil {
		debug.Log("daemon-bridge", "%squeuing interruption: %s", logPrefix, truncateStr(extractText(content), 80))
		b.pendingInterruptions = append(b.pendingInterruptions, pendingInterruption{Content: content})
		return nil, true
	}

	ctx, cancel := context.WithCancel(context.Background())

	if b.agent == nil {
		debug.Log("daemon-bridge", "%stryQueueOrBeginRun: no agent attached; deferring content", logPrefix)
		b.pendingInterruptions = append(b.pendingInterruptions, pendingInterruption{Content: content})
		cancel()
		return nil, true
	}
	b.agent.SetInterruptionHandler(func() []provider.ContentBlock {
		b.mu.Lock()
		defer b.mu.Unlock()
		if len(b.pendingInterruptions) == 0 {
			return nil
		}
		next := b.pendingInterruptions[0]
		b.pendingInterruptions = b.pendingInterruptions[1:]

		debug.Log("daemon-bridge", "interruption handler returning: %s", truncateStr(extractText(next.Content), 80))
		return next.Content
	})
	b.cancelFunc = cancel
	return ctx, false
}

func (b *DaemonBridge) finishRunSlot() {
	b.mu.Lock()
	cancel := b.cancelFunc
	b.cancelFunc = nil
	b.agent.SetInterruptionHandler(nil)
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	b.notifyRunStateChange(false)
}

func (b *DaemonBridge) nextPendingInterruption() []provider.ContentBlock {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pendingInterruptions) == 0 {
		return nil
	}
	next := b.pendingInterruptions[0].Content
	b.pendingInterruptions = b.pendingInterruptions[1:]
	return next
}

func (b *DaemonBridge) runQueuedLoop(ctx context.Context, content []provider.ContentBlock, logPrefix string, preRun func(context.Context, string) bool, onRunError func(error)) {
	defer b.finishRunSlot()
	for {
		text := extractText(content)
		if preRun != nil && preRun(ctx, text) {
			return
		}
		err := b.runAgentStream(ctx, content)
		if err != nil && !errors.Is(err, context.Canceled) && onRunError != nil {
			onRunError(err)
		}

		nextContent := b.nextPendingInterruption()
		if len(nextContent) == 0 {
			return
		}
		debug.Log("daemon-bridge", "%sdraining queued message: %s", logPrefix, truncateStr(extractText(nextContent), 80))
		content = nextContent
	}
}

func (b *DaemonBridge) SubmitInboundMessage(ctx context.Context, msg InboundMessage) error {
	if b == nil {
		return fmt.Errorf("daemon bridge not initialized")
	}
	text := strings.TrimSpace(msg.Text)

	if text != "" || len(msg.Attachments) > 0 {
		b.mu.Lock()
		onActivity := b.onActivity
		b.mu.Unlock()
		if onActivity != nil {
			onActivity()
		}
	}

	b.mu.Lock()
	hasPendingApproval := b.pendingApproval != nil
	hasPendingAsk := b.pendingAsk != nil
	b.mu.Unlock()
	route := RouteInboundText(text, hasPendingApproval, hasPendingAsk)

	if route.Kind == InboundRouteSlash {
		return b.handleSlashCommand(ctx, text, msg)
	}

	if route.Kind == InboundRouteShell {

		if !b.remoteDangerousAllowed() {
			if b.emitTextOverride != nil {
				_ = b.emitTextOverride("shell passthrough ($/! commands) over IM requires the im.remote_dangerous_commands opt-in - refused (#2185/#2205)")
			} else {
				_ = b.emitter.EmitText("shell passthrough ($/! commands) over IM requires the im.remote_dangerous_commands opt-in - refused (#2185/#2205)")
			}
			return nil
		}
		b.handleShellInbound(route.Text)
		return nil
	}

	b.mu.Lock()
	staleUntil := b.staleReplySuppressUntil
	b.mu.Unlock()
	if time.Now().Before(staleUntil) {
		if route.Kind == InboundRouteApproval || route.Kind == InboundRouteAskUser {
			debug.Log("daemon-bridge", "dropping reply-shaped text inside stale-reply window: %q", truncateStr(text, 80))
			_ = b.emitter.EmitText("⏱ That looked like a reply to the expired prompt, so it was ignored. Please resend it for the current question.")
			return nil
		}
		if route.Kind == InboundRouteMessage {
			if _, ok := ParseApprovalReply(strings.TrimSpace(text)); ok {
				debug.Log("daemon-bridge", "dropping bare approval token inside stale-reply window (ProbeB2): %q", truncateStr(text, 80))
				_ = b.emitter.EmitText("⏱ That looked like a reply to the expired approval, so it was ignored. Send your actual message again if you meant it as one.")
				return nil
			}
		}
	}

	b.mu.Lock()
	approvalCh := b.pendingApproval
	b.mu.Unlock()
	if route.Kind == InboundRouteApproval {

		if approvalCh == nil {
			debug.Log("daemon-bridge", "dropping approval reply after timeout: %q", truncateStr(text, 80))
			return nil
		}

		select {
		case approvalCh <- approvalReply{Decision: route.Decision, Always: route.AlwaysAllow}:
			b.mu.Lock()
			if b.pendingApproval == approvalCh {
				b.pendingApproval = nil
			}
			b.mu.Unlock()
		default:
			debug.Log("daemon-bridge", "dropping approval reply: no waiting asker")
		}
		return nil
	}

	b.mu.Lock()
	pending := b.pendingAsk
	b.mu.Unlock()
	if route.Kind == InboundRouteAskUser {
		if pending != nil {
			resp := BuildAskUserResponseFromText(pending.request, route.Text)

			select {
			case pending.response <- resp:
			default:
			}
			b.mu.Lock()
			if b.pendingAsk == pending {
				b.pendingAsk = nil
			}
			b.mu.Unlock()
			return nil
		}

		debug.Log("daemon-bridge", "dropping stale ask_user reply: %s", text)
		return nil
	}

	b.mu.Lock()
	runActive := b.cancelFunc != nil
	b.mu.Unlock()
	if b.agent == nil && !runActive {
		return fmt.Errorf("daemon bridge has no agent attached")
	}

	content := msg.ProviderContent()
	if len(content) == 0 {
		return nil
	}

	if text != "" {
		content = withIMProvenance(content, msg.Envelope)
	}

	restoreVision, visionSwitched := b.beginVisionTurn(content)
	if !visionSwitched && contentHasImageBlocks(content) {
		content = stripImageBlocks(content)
	}
	defer restoreVision()

	if text == "" && contentEffectivelyEmpty(content) {
		return nil
	}
	b.notifyUserMessage(content)

	b.emitter.TriggerTyping()

	b.mu.Lock()
	sink := b.followSink
	b.mu.Unlock()
	if sink != nil {
		sink.OnUserMessage(text)
	}

	if len(content) == 0 {
		content = []provider.ContentBlock{{Type: "text", Text: text}}
	}
	ctx2, queued := b.tryQueueOrBeginRun(content, "")
	if queued {
		return nil
	}
	b.notifyRunStateChange(true)
	b.runQueuedLoop(ctx2, content, "", func(ctx context.Context, text string) bool {
		return false
	}, func(err error) {
		_ = b.emitter.EmitText(provider.UserFacingError(err))
	})

	return nil
}

func (b *DaemonBridge) HandleAskUser(ctx context.Context, req toolpkg.AskUserRequest) (toolpkg.AskUserResponse, error) {
	answers := make([]toolpkg.AskUserAnswer, len(req.Questions))
	answeredCount := 0

	for i, q := range req.Questions {

		singleReq := toolpkg.AskUserRequest{
			Title:     req.Title,
			Questions: []toolpkg.AskUserQuestion{q},
		}
		argsJSON, _ := jsonMarshalArgs(singleReq)
		text := b.emitter.FormatAskUserPrompt(argsJSON)
		if text == "" {
			text = strings.TrimSpace(q.Title)
		}

		var msgIDs map[string]string
		isMulti := q.Kind == toolpkg.AskUserKindMulti
		pending := &pendingAskUser{
			request:     singleReq,
			response:    make(chan toolpkg.AskUserResponse, 1),
			multiSelect: isMulti,
		}
		b.mu.Lock()
		b.pendingAsk = pending
		if isMulti {
			b.multiSelectChosen = nil
		}
		b.mu.Unlock()

		if len(q.Choices) > 0 {

			msgIDs = b.emitter.EmitAskUserInteractive(q.Title, q, text)
			if len(msgIDs) > 0 {
				b.mu.Lock()
				b.interactiveMsgIDs = msgIDs

				pending.msgIDs = msgIDs
				b.mu.Unlock()
			}
		} else {

			if text != "" {
				b.emitter.EmitAskUser(text)
			}
		}

		select {
		case resp := <-pending.response:
			if len(resp.Answers) > 0 && resp.Answers[0].Answered {
				answers[i] = resp.Answers[0]
				answeredCount++
			} else {
				answers[i] = toolpkg.AskUserAnswer{
					ID:               q.ID,
					Title:            q.Title,
					Kind:             q.Kind,
					CompletionStatus: toolpkg.AskUserCompletionUnanswered,
					AnswerMode:       toolpkg.AskUserAnswerModeNone,
					Answered:         false,
				}
			}
		case <-ctx.Done():
			b.mu.Lock()
			cleared := false
			if b.pendingAsk == pending {
				b.pendingAsk = nil
				cleared = true
			}
			b.mu.Unlock()

			if cleared {
				b.mu.Lock()
				b.staleReplySuppressUntil = time.Now().Add(staleReplySuppressWindow)
				b.mu.Unlock()
				_ = b.emitter.EmitText("⏱ The question above has expired. Any reply to it now will be treated as a NEW message, not an answer.")
			}
			return toolpkg.AskUserResponse{}, ctx.Err()
		}
	}

	return toolpkg.AskUserResponse{
		Status:        toolpkg.AskUserStatusSubmitted,
		Title:         req.Title,
		QuestionCount: len(req.Questions),
		AnsweredCount: answeredCount,
		Answers:       answers,
	}, nil
}

func (b *DaemonBridge) handleApproval(ctx context.Context, toolName string, input string) permission.Decision {
	lang := b.language
	var prompt string
	if lang == "zh-CN" || lang == "zh" {
		prompt = FormatApprovalRequest(ToolLangZhCN, toolName, input)
	} else {
		prompt = FormatApprovalRequest(ToolLangEn, toolName, input)
	}
	if err := b.emitter.EmitText(prompt); err != nil {
		debug.Log("daemon", "approval: failed to emit prompt for tool=%s: %v", toolName, err)
	}

	ch := make(chan approvalReply, 1)
	b.mu.Lock()
	b.pendingApproval = ch
	b.mu.Unlock()
	debug.Log("daemon", "approval: waiting for IM reply on tool=%s", toolName)

	select {
	case reply := <-ch:
		decision := reply.Decision
		debug.Log("daemon", "approval: received %v (always=%v) for tool=%s", decision, reply.Always, toolName)
		if reply.Always && decision == permission.Allow {
			persistAlwaysAllowGrant(b.agent, toolName, input)
		}
		var resultMsg string
		decisionStr := "deny"
		if decision == permission.Allow {
			if reply.Always {
				decisionStr = "always"
			} else {
				decisionStr = "allow"
			}
		}
		if lang == "zh-CN" || lang == "zh" {
			resultMsg = FormatApprovalResult(ToolLangZhCN, toolName, decisionStr)
		} else {
			resultMsg = FormatApprovalResult(ToolLangEn, toolName, decisionStr)
		}
		if resultMsg != "" {
			_ = b.emitter.EmitText(resultMsg)
		}
		return decision
	case <-ctx.Done():
		debug.Log("daemon", "approval: context cancelled for tool=%s", toolName)
		b.mu.Lock()
		cleared := false
		if b.pendingApproval == ch {
			b.pendingApproval = nil
			cleared = true
		}
		b.mu.Unlock()

		if cleared {
			b.mu.Lock()
			b.staleReplySuppressUntil = time.Now().Add(staleReplySuppressWindow)
			b.mu.Unlock()
			_ = b.emitter.EmitText("⏱ The approval prompt above has expired. Any reply to it now will be treated as a NEW message, not an approval.")
		}

		return permission.DecisionFromContext(ctx.Err())
	}
}

func persistAlwaysAllowGrant(ag *agent.Agent, toolName, input string) {
	if ag == nil {
		return
	}
	p, ok := ag.PermissionPolicy().(*permission.ConfigPolicy)
	if !ok {
		return
	}
	if cmd := permission.ExtractCommandFromInput(input); cmd != "" {
		if pattern := permission.CommandPrefixToPattern(cmd); pattern != "" {
			p.AllowCommandPattern(pattern)
			return
		}
	}
	p.SetOverride(toolName, permission.Allow)
}

func formatToolInline(toolName, input string) string {
	if input == "" {
		return toolName
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(input), &m); err == nil {
		for _, key := range []string{"command", "path", "file_path", "query", "url", "message"} {
			if v, ok := m[key].(string); ok && v != "" {
				return fmt.Sprintf("%s: %s", toolName, truncate(v, 60))
			}
		}
	}
	return fmt.Sprintf("%s: %s", toolName, truncate(input, 60))
}

func (b *DaemonBridge) runAgentStream(ctx context.Context, content []provider.ContentBlock) error {
	round := &daemonRoundState{}
	b.mu.Lock()
	b.usageTurnIndex++
	sink := b.followSink
	b.mu.Unlock()

	b.appendUserMessage(content)

	err := b.agent.RunStreamWithContent(ctx, content, func(event provider.StreamEvent) {
		defer safego.Recover("im.daemonBridge.streamCallback")

		b.broadcastEvent(event)

		switch event.Type {
		case provider.StreamEventText:

			round.AppendText(event.Text)
			b.emitter.TriggerTyping()
			if sink != nil {
				sink.OnStreamText(event.Text)
			}

		case provider.StreamEventToolCallDone:
			toolName := strings.TrimSpace(event.Tool.Name)
			if toolName == "ask_user" {
				round.SetAskUser(b.emitter.FormatAskUserPrompt(string(event.Tool.Arguments)))
			}
			if !isDaemonSkippedTool(toolName) {
				round.NoteToolCall()
			}

			if toolName == "enter_plan_mode" {
				label := "📝 Planning..."
				if toolLang(b.language) == ToolLangZhCN {
					label = "📝 正在规划..."
				}
				var args struct {
					Description string `json:"description"`
				}
				if json.Unmarshal([]byte(event.Tool.Arguments), &args) == nil && args.Description != "" {
					label = "📝 " + args.Description
				}
				_ = b.emitter.EmitText(label)
			}

			if toolName == "sleep" && b.resolveEffectiveOutputMode() == "verbose" {
				b.emitter.EmitEvent(OutboundEvent{
					Kind: OutboundEventToolCall,
					ToolCall: &ToolCallInfo{
						ToolName: "sleep",
						Args:     string(event.Tool.Arguments),
						Detail:   formatSleepDuration(string(event.Tool.Arguments)),
					},
				})
			}

			b.emitter.TriggerTyping()

		case provider.StreamEventToolResult:
			round.NoteToolResult(event.IsError)
			toolInfo := ToolResultInfo{
				ToolName: event.Tool.Name,
				Args:     string(event.Tool.Arguments),
				Result:   event.Result,
				IsError:  event.IsError,
				Lang:     b.language,
			}

			switch b.resolveEffectiveOutputMode() {
			case "summary":

				round.PendingTools = append(round.PendingTools, toolInfo)
			case "quiet":

				if event.IsError {
					b.emitter.EmitEvent(OutboundEvent{
						Kind:    OutboundEventToolResult,
						ToolRes: &toolInfo,
					})
				} else {
					round.PendingTools = append(round.PendingTools, toolInfo)
				}
			default:
				b.emitter.EmitEvent(OutboundEvent{
					Kind:    OutboundEventToolResult,
					ToolRes: &toolInfo,
				})
			}
			b.emitter.TriggerTyping()
			if sink != nil {
				sink.OnToolResult(event.Tool.Name, string(event.Tool.Arguments), event.Result, event.IsError)
			}

		case provider.StreamEventDone:
			text := round.Text()
			mode := b.resolveEffectiveOutputMode()

			if (mode == "quiet" || mode == "summary") && len(round.PendingTools) > 0 {
				summary := formatToolSummary(b.language, round.PendingTools, round.ToolCalls, round.ToolSuccesses, round.ToolFailures)
				if summary != "" {
					_ = b.emitter.EmitText(summary)
				}
			}

			if strings.TrimSpace(text) != "" {
				b.emitter.EmitRoundSummary(text, round.ToolCalls, round.ToolSuccesses, round.ToolFailures)
			}

			b.appendAssistantMessages()
			round.Reset()
			if sink != nil {
				sink.OnRoundDone()
			}

		case provider.StreamEventError:
			if !errors.Is(event.Error, context.Canceled) {
				_ = b.emitter.EmitText(provider.UserFacingError(event.Error))
			}

			round.Reset()
			if sink != nil {
				sink.OnError(event.Error)
			}
		}
	}, "human")

	if ctx.Err() == nil {
		memory.CapturePreferences(b.agent.WorkingDir(), extractText(content))
	}
	return err
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func (b *DaemonBridge) appendUserMessage(content []provider.ContentBlock) {
	if b.store == nil || b.sess == nil {
		return
	}
	text := contentToText(content)
	msg := provider.Message{
		Role:    "user",
		Content: content,
	}
	if b.sess.Title == "" || b.sess.Title == "New session" {
		if len(text) > 60 {
			b.sess.Title = truncateRunes(text, 60, "...")
		} else {
			b.sess.Title = text
		}
	}

	b.sess.Messages = append(b.sess.Messages, msg)
}

func (b *DaemonBridge) appendAssistantMessages() {
	if b.store == nil || b.sess == nil || b.agent == nil {
		return
	}
	messages := b.agent.Messages()
	sessLen := len(b.sess.Messages)

	if len(messages) <= sessLen {
		return
	}

	for i := sessLen; i < len(messages); i++ {
		b.sess.Messages = append(b.sess.Messages, messages[i])
	}
}

func (b *DaemonBridge) recordMetric(ev metrics.MetricEvent) {
	b.mu.Lock()
	ses := b.sess
	store := b.store
	ev.TurnIndex = b.usageTurnIndex
	if ses != nil {
		ev.Model = ses.Model
		ev.Vendor = ses.Vendor
		ev.Endpoint = ses.Endpoint
		ses.AppendMetricEvent(ev)
		ses.AppendMetricForEndpoint(ses.Vendor, ses.Endpoint, ev)
	}
	b.mu.Unlock()
	if ses == nil || store == nil {
		return
	}
	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendMetric(ses, ev)
	}
}

func (b *DaemonBridge) Close() {
	b.mu.Lock()
	collector := b.metricCollector
	metricCancel := b.metricCancel
	runCancel := b.cancelFunc
	b.metricCollector = nil
	b.metricCancel = nil
	b.cancelFunc = nil
	b.mu.Unlock()
	if metricCancel != nil {
		metricCancel()
	}
	if runCancel != nil {
		runCancel()
	}
	if collector != nil {
		collector.Stop()
	}
}

func daemonSessionTurnIndex(ses *session.Session) int {
	if ses == nil {
		return 0
	}
	last := 0

	if hist := ses.UsageHistorySnapshot(); len(hist) > 0 && hist[len(hist)-1].TurnIndex > last {
		last = hist[len(hist)-1].TurnIndex
	}
	if mets := ses.MetricsSnapshot(); len(mets) > 0 && mets[len(mets)-1].TurnIndex > last {
		last = mets[len(mets)-1].TurnIndex
	}
	return last
}

type daemonRoundState = SummaryRoundState

func isDaemonSkippedTool(name string) bool {
	switch name {
	case "spawn_agent", "wait_agent", "list_agents":
		return true
	}
	return false
}

func contentToText(blocks []provider.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if t := strings.TrimSpace(b.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

func jsonMarshalArgs(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func formatToolSummary(lang string, tools []ToolResultInfo, total, successes, failures int) string {
	if len(tools) == 0 {
		return ""
	}

	var sb strings.Builder

	if ToolLanguage(lang) == ToolLangZhCN {
		sb.WriteString(fmt.Sprintf("⚙ 执行了 %d 个工具调用", total))
		if failures > 0 {
			sb.WriteString(fmt.Sprintf("（%d 成功，%d 失败）", successes, failures))
		}
	} else {
		sb.WriteString(fmt.Sprintf("⚙ %d tool calls", total))
		if failures > 0 {
			sb.WriteString(fmt.Sprintf(" (%d ok, %d failed)", successes, failures))
		}
	}
	sb.WriteString("\n")

	type toolStat struct {
		count    int
		failures int
	}
	toolStats := make(map[string]*toolStat)
	for _, t := range tools {
		name := formatIMToolDisplayName(t.ToolName)
		stat := toolStats[name]
		if stat == nil {
			stat = &toolStat{}
			toolStats[name] = stat
		}
		stat.count++
		if t.IsError {
			stat.failures++
		}
	}

	type kv struct {
		name     string
		count    int
		failures int
	}
	var sorted []kv
	for k, v := range toolStats {
		sorted = append(sorted, kv{k, v.count, v.failures})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	for _, item := range sorted {
		var symbol string
		if item.failures > 0 && item.failures == item.count {
			symbol = "✗"
		} else if item.failures > 0 {
			symbol = "⚠"
		} else {
			symbol = "✓"
		}
		if item.count == 1 {
			sb.WriteString(fmt.Sprintf("  %s %s\n", symbol, item.name))
		} else {
			sb.WriteString(fmt.Sprintf("  %s %s ×%d\n", symbol, item.name, item.count))
		}
	}

	return sb.String()
}

func formatIMToolDisplayName(toolName string) string {
	lang := ToolLanguage("")
	pres := DescribeTool(lang, toolName, "")
	if pres.DisplayName != "" {
		return pres.DisplayName
	}
	return toolName
}

func (b *DaemonBridge) handleSlashCommand(ctx context.Context, text string, msg InboundMessage) error {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "/restart") {
		b.mu.Lock()
		onRestart := b.onRestart
		b.mu.Unlock()
		if onRestart == nil {
			return fmt.Errorf("restart not available")
		}
	}

	if result := ExecuteExtendedIMSlashCommand(ExtendedIMSlashOptions{
		Manager:     b.manager,
		SelfAdapter: msg.Envelope.Adapter,
		Text:        text,
		HelpExtraLines: append([]string{
			"/restart [debug] - Restart daemon (unmutes all adapters; add 'debug' to enable GGCODE_DEBUG=1)",
			"/provider [vendor] [endpoint] - Show or switch LLM provider",
			"/model [name] - Show or switch model",
			"/config - Show current provider and model configuration",
		}, IMSlashHelpLines()...),
		OnRestart: func(debugMode bool) (string, error) {
			b.mu.Lock()
			onRestart := b.onRestart
			b.mu.Unlock()
			if onRestart == nil {
				return "", fmt.Errorf("restart not available")
			}
			if debugMode {
				b.mu.Lock()
				b.restartDebug = true
				b.mu.Unlock()
				safego.Go("im.daemonBridge.restart", func() {
					time.Sleep(1 * time.Second)
					onRestart()
				})
				return "🔄 Restarting daemon with debug logging (GGCODE_DEBUG=1)...", nil
			}
			safego.Go("im.daemonBridge.restart", func() {
				time.Sleep(1 * time.Second)
				onRestart()
			})
			return "🔄 Restarting daemon...", nil
		},
		OnProvider: func(vendor, endpoint string) (string, error) {
			b.mu.Lock()
			fn := b.onProviderSwitch
			b.mu.Unlock()
			if fn == nil {
				return "", fmt.Errorf("provider switching not available in this mode")
			}
			return fn(vendor, endpoint, "")
		},
		OnModel: func(model string) (string, error) {
			b.mu.Lock()
			fn := b.onProviderSwitch
			b.mu.Unlock()
			if fn == nil {
				return "", fmt.Errorf("model switching not available in this mode")
			}
			return fn("", "", model)
		},
		OnConfig: func() (string, error) {
			b.mu.Lock()
			fn := b.onProviderSwitch
			b.mu.Unlock()
			if fn == nil {
				return "", fmt.Errorf("config display not available in this mode")
			}
			return fn("", "", "")
		},
		OnExtra: func(parts []string) (string, bool) {

			return ExecuteRegistrySlashCommand(b, strings.Join(parts, " "))
		},
	}); result.Handled {
		if result.Response != "" {
			_ = b.emitter.EmitText(result.Response)
		}
		if result.MuteSelfAdapter != "" {
			time.Sleep(500 * time.Millisecond)
			b.manager.MuteBinding(result.MuteSelfAdapter)
		}
		return nil
	}
	return nil
}

func (b *DaemonBridge) resolveEffectiveOutputMode() string {
	globalMode := b.emitter.OutputMode()
	if b.manager == nil {
		return globalMode
	}
	snapshot := b.manager.Snapshot()
	for _, binding := range snapshot.CurrentBindings {
		if binding.Muted {
			continue
		}
		for _, adapter := range snapshot.Adapters {
			if adapter.Name == binding.Adapter && adapter.Platform == PlatformWechat {
				if globalMode == "" || globalMode == "verbose" {
					return WechatDefaultOutputMode
				}
			}
		}
	}
	return globalMode
}

func extractText(blocks []provider.ContentBlock) string {
	var text string
	for _, b := range blocks {
		if b.Type == "text" {
			text += b.Text
		}
	}
	return text
}

func (b *DaemonBridge) Messages() []provider.Message {
	return b.agent.Messages()
}

func (b *DaemonBridge) SendUserMessage(content []provider.ContentBlock) {
	text := extractText(content)
	if text == "" && len(content) == 0 {
		return
	}
	b.notifyUserMessage(content)

	if text != "" || len(content) > 0 {
		b.mu.Lock()
		onActivity := b.onActivity
		b.mu.Unlock()
		if onActivity != nil {
			onActivity()
		}
	}

	ctx2, queued := b.tryQueueOrBeginRun(content, "webchat: ")
	if queued {
		return
	}
	b.notifyRunStateChange(true)

	safego.Go("im.daemonBridge.run", func() {
		b.runQueuedLoop(ctx2, content, "webchat: ", func(ctx context.Context, text string) bool {
			return false
		}, func(err error) {
			debug.Log("daemon-bridge", "webchat: agent run error: %v", err)
		})
	})
}

type daemonBridgeSub struct {
	fn   func(provider.StreamEvent)
	ch   chan provider.StreamEvent
	done chan struct{}
}

func (b *DaemonBridge) Subscribe(fn func(provider.StreamEvent)) func() {
	sub := &daemonBridgeSub{
		fn:   fn,
		ch:   make(chan provider.StreamEvent, 256),
		done: make(chan struct{}),
	}

	safego.Go("im.daemonBridge.subscriber", func() {
		defer close(sub.done)
		for event := range sub.ch {
			func() {
				defer safego.Recover("im.daemonBridge.subscriberCallback")
				sub.fn(event)
			}()
		}
	})

	b.eventSubMu.Lock()
	b.eventSubs = append(b.eventSubs, sub)
	idx := len(b.eventSubs) - 1
	b.eventSubMu.Unlock()
	return func() {

		b.eventSubMu.Lock()
		sub := b.eventSubs[idx]
		b.eventSubs[idx] = nil
		if sub != nil {
			close(sub.ch)
		}
		b.eventSubMu.Unlock()
		if sub != nil {
			select {
			case <-sub.done:
			case <-time.After(5 * time.Second):
				debug.Log("daemon-bridge", "webchat subscriber drain timed out after close")
			}
		}
	}
}

func (b *DaemonBridge) broadcastEvent(event provider.StreamEvent) {
	b.eventSubMu.RLock()
	defer b.eventSubMu.RUnlock()
	for _, sub := range b.eventSubs {
		if sub != nil {
			select {
			case sub.ch <- event:
			default:
				debug.Log("daemon-bridge", "webchat subscriber channel full, dropping event")
			}
		}
	}
}
