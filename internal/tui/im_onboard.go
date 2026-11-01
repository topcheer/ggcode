package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// #4861: guided IM onboarding wizard + full-registry status table
// (Hermes gateway-setup pattern). The wizard is the guidance layer:
// per-platform numbered steps with anti-confusion notes, then reuse of the
// EXISTING per-platform config panels for secret entry (AddIMAdapter /
// panel seams) and /im status as the end-of-wizard connectivity verdict.

// imOnboardStep is one numbered instruction for one platform.
type imOnboardStep struct {
	Text string
	// Note is the anti-confusion warning ("this ID is NOT that ID" traps).
	Note string
	// URL, when set, is the step's entry deep-link: rendered as a copyable
	// line and auto-opened in the browser (#4861 checklist 5, mcp_panel
	// urlOpener precedent).
	URL string
}

// imOnboardEntry is one platform's wizard definition.
type imOnboardEntry struct {
	Platform string
	Steps    []imOnboardStep
	// Panel names the command that opens the existing config panel where the
	// credentials are entered (the masked-secret entry seam).
	Panel string
	// QR marks pairing-QR platforms (#4861 checklist 5): when the adapter
	// emits its pairing QR, the panel renders it as a large terminal QR
	// (qrcode_terminal path, whatsapp_panel precedent) with a URL fallback
	// line - Hermes print_ascii parity without touching the pairing flow.
	QR bool
}

// imOnboardWizard returns the full wizard definition. Platforms without a
// dedicated panel fall back to the unified /im panel + config file entry.
func imOnboardWizard() map[string]imOnboardEntry {
	return map[string]imOnboardEntry{
		"qq": {
			Platform: "qq",
			Steps: []imOnboardStep{
				{Text: "Pick a lane first: QR-pairing (self-bot, scans a login QR from your already-logged-in QQ client) OR official Bot (BotToken from q.qq.com, appid + secret)."},
				{Text: "QR-pairing lane: run the adapter once and scan the QR it prints/receives - no token entry needed."},
				{Text: "Bot lane: create the bot at q.qq.com, copy the appid and BotToken.", Note: "The BotToken is the bot's token from the open-platform console - NOT your QQ password and NOT the bot's QQ number."},
				{Text: "Enter the credentials in the QQ panel (masked input); the pairing state saves automatically."},
			},
			Panel: "/qq",
			QR:    true,
		},
		"telegram": {
			Platform: "telegram",
			Steps: []imOnboardStep{
				{Text: "Open a chat with @BotFather in Telegram and send /newbot.", URL: "https://t.me/BotFather"},
				{Text: "Follow the prompts (bot name, then a username ending in 'bot')."},
				{Text: "Copy the token BotFather prints (format 123456789:AA...).", Note: "The token is a long colon-separated string from BotFather - NOT your Telegram @username and NOT your phone number."},
				{Text: "Enter the token in the Telegram panel (masked input)."},
			},
			Panel: "/telegram",
		},
		"discord": {
			Platform: "discord",
			Steps: []imOnboardStep{
				{Text: "Create an application at discord.com/developers/applications, then add a Bot to it.", URL: "https://discord.com/developers/applications"},
				{Text: "Copy the bot token from the Bot tab.", Note: "Use the Bot TOKEN, not the client ID / application ID - the token is ~70 chars, the IDs are 17-20 digit numbers."},
				{Text: "Enable the Message Content intent on the Bot tab (required to read commands)."},
				{Text: "Generate an OAuth2 URL with the 'bot' scope + 'Send Messages'/'Read Message History' permissions and invite it to your server."},
				{Text: "Enter the token in the Discord panel (masked input)."},
			},
			Panel: "/discord",
		},
		"feishu": {
			Platform: "feishu",
			Steps: []imOnboardStep{
				{Text: "Create an app at open.feishu.cn (Enterprise Self-built App).", URL: "https://open.feishu.cn/app"},
				{Text: "Copy the App ID and App Secret from Credentials & Basic Info."},
				{Text: "Enable the Bot capability, then subscribe the events (im.message.receive_v1) under Event Subscriptions.", Note: "Three credentials exist (App ID, App Secret, Verification Token/Encrypt Key) - the adapter needs App ID + App Secret; the verification tokens are for Feishu's callback validation, do not confuse them."},
				{Text: "Publish the app version and grant it im:message scopes, then enter the trio in the Feishu panel (masked input)."},
			},
			Panel: "/feishu",
		},
		"slack": {
			Platform: "slack",
			Steps: []imOnboardStep{
				{Text: "Pick a lane: Incoming-Webhook (post-only, one URL) OR full Bot (Socket Mode, reads commands)."},
				{Text: "Webhook lane: api.slack.com/messaging/webhooks -> create one for a channel, copy the https://hooks.slack.com/services/... URL.", Note: "The webhook URL is per-channel and post-ONLY - commands sent over it are not received; pick the bot lane if you want two-way.", URL: "https://api.slack.com/messaging/webhooks"},
				{Text: "Bot lane: create an app, enable Socket Mode, copy the Bot token (xoxb-...), the app-level token (xapp-...), and subscribe message events."},
				{Text: "Enter the credentials in the Slack panel (masked input)."},
			},
			Panel: "/slack",
		},
		"dingtalk": {
			Platform: "dingtalk",
			Steps: []imOnboardStep{
				{Text: "Create a robot in DingTalk (group robot for webhook lane, or an enterprise app for Stream Mode)."},
				{Text: "Webhook lane: group settings -> Robot -> add, copy the webhook URL + secret.", Note: "The secret is the SEC... string shown next to the webhook - NOT your DingTalk login password."},
				{Text: "Stream Mode lane: open-platform app -> copy Client ID (AppKey) + Client Secret and enable Stream Mode."},
				{Text: "Enter the credentials in the DingTalk panel (masked input)."},
			},
			Panel: "/dingtalk",
		},
		"wechat": {
			Platform: "wechat",
			Steps: []imOnboardStep{
				{Text: "Pick a lane: personal WeChat bridge (relay-based, follows the provider's QR login) OR WeCom/Enterprise (official app with a corp ID)."},
				{Text: "WeCom lane: create an app in the WeCom admin console, copy CorpID + AgentId + Secret.", Note: "CorpID is the enterprise ID (from My Enterprise page), AgentId is the app's number, Secret is the app's - all three are different, none is your WeChat password."},
				{Text: "Enter the credentials in the WeChat/WeCom panel (masked input)."},
			},
			Panel: "/wechat",
		},
		"mattermost": {
			Platform: "mattermost",
			Steps: []imOnboardStep{
				{Text: "Create a bot account (Integrations -> Bot Accounts) or use a Personal Access Token."},
				{Text: "Copy the token; note the server URL and the team name.", Note: "The token is the Account Settings -> Security -> Personal Access Token value - NOT your login password and NOT the bot's username."},
				{Text: "Enter server URL + token in the Mattermost panel (masked input)."},
			},
			Panel: "/mm",
		},
		"matrix": {
			Platform: "matrix",
			Steps: []imOnboardStep{
				{Text: "Register a dedicated account (or create one on your homeserver)."},
				{Text: "Create an access token (Element: Settings -> Help & About -> Access Token, or the login API).", Note: "The access token starts with 'syt_' or 'MDA...' - NOT your Matrix @user:domain ID and NOT the password."},
				{Text: "Enter homeserver URL + access token in the Matrix panel (masked input)."},
			},
			Panel: "/matrix",
		},
		"signal": {
			Platform: "signal",
			Steps: []imOnboardStep{
				{Text: "Install signal-cli on a reachable host and link a device (it prints a QR)."},
				{Text: "Note the signal-cli REST endpoint URL for the adapter."},
				{Text: "Point the adapter at the endpoint in the unified IM panel (/im)."},
			},
			Panel: "/im",
		},
		"irc": {
			Platform: "irc",
			Steps: []imOnboardStep{
				{Text: "Pick network + channel; register a nickname (NickServ) if the network requires it."},
				{Text: "Enter server, port, TLS flag, nickname and channels in the unified IM panel (/im)."},
			},
			Panel: "/im",
		},
		"nostr": {
			Platform: "nostr",
			Steps: []imOnboardStep{
				{Text: "Generate a Nostr keypair (or reuse one); note a relay URL (wss://...)."},
				{Text: "Enter the private key (nsec...) and relay URL in the unified IM panel (/im).", Note: "The nsec... private key is the SECRET - the npub... public key alone cannot send or receive."},
			},
			Panel: "/im",
		},
		"twitch": {
			Platform: "twitch",
			Steps: []imOnboardStep{
				{Text: "Register the bot as a Twitch application (dev console) and take an OAuth token with chat permissions."},
				{Text: "Note the bot login name and the channel it joins.", Note: "The OAuth token is the oauth:... string - NOT your Twitch login password."},
				{Text: "Enter the credentials in the unified IM panel (/im)."},
			},
			Panel: "/im",
		},
		"whatsapp": {
			Platform: "whatsapp",
			Steps: []imOnboardStep{
				{Text: "Pick a lane: WhatsApp Cloud API (official, Meta business app) OR a bridge (whatsapp-web.js/Baileys style, QR-linked)."},
				{Text: "Cloud lane: Meta app dashboard -> phone number ID + access token (permanent System User token recommended).", Note: "The phone number ID is a numeric ID from the dashboard - NOT the phone number itself in E.164."},
				{Text: "Enter the credentials in the WhatsApp panel (masked input)."},
			},
			Panel: "/wa",
			QR:    true,
		},
		"synology": {
			Platform: "synology",
			Steps: []imOnboardStep{
				{Text: "Enable the Chat Server package on the Synology NAS; create a bot integration in Chat settings."},
				{Text: "Copy the incoming-webhook token from the integration.", Note: "Use the integration's token - NOT your DSM login password."},
				{Text: "Enter NAS URL + token in the unified IM panel (/im)."},
			},
			Panel: "/im",
		},
	}
}

// imOnboardGenericTail is appended to every platform's step list: the shared
// allowlist / home-channel / reply-mode configuration guidance.
const imOnboardGenericTail = `Shared settings (all platforms):
  - allowlist: comma-separated sender IDs allowed to command the agent (empty = deny all inbound commands; your own platform user ID - NOT your display name).
  - home channel: where cron results and notifications deliver by default.
  - reply-mode: flat (each reply is a new message) vs thread (replies nest under the triggering message).
Finish: open the platform panel, enter the credentials (secrets are masked on input), save, then run /im status to verify the adapter comes online.`

// renderIMOnboardIntro renders the wizard's platform picker.
func renderIMOnboardIntro() string {
	var sb strings.Builder
	sb.WriteString("IM Onboarding Wizard\n")
	sb.WriteString("====================\n")
	sb.WriteString("Pick a platform (run: /im onboard <platform>):\n")
	names := make([]string, 0, len(imOnboardWizard()))
	for name := range imOnboardWizard() {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sb.WriteString(fmt.Sprintf("  - %s\n", name))
	}
	sb.WriteString("\nEach platform walks numbered setup steps with anti-confusion notes,\nthen opens the existing config panel (masked secret input) and verifies\nconnectivity via /im status.")
	return sb.String()
}

// renderIMOnboardSteps renders one platform's wizard: numbered steps,
// anti-confusion notes, copyable URL deep-links, the panel seam, and the
// shared settings tail.
func renderIMOnboardSteps(platform string) (string, bool) {
	entry, ok := imOnboardWizard()[strings.ToLower(strings.TrimSpace(platform))]
	if !ok {
		return "", false
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("IM Onboarding: %s\n", entry.Platform))
	sb.WriteString(strings.Repeat("=", len(entry.Platform)+16) + "\n")
	for i, s := range entry.Steps {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, s.Text))
		if s.URL != "" {
			sb.WriteString(fmt.Sprintf("   Open (copyable): %s\n", s.URL))
		}
		if s.Note != "" {
			sb.WriteString(fmt.Sprintf("   NOTE: %s\n", s.Note))
		}
	}
	if entry.QR {
		sb.WriteString("\n[QR pairing] when the adapter emits its pairing QR, the panel renders it\n")
		sb.WriteString("as a large terminal QR (scan with the phone app); a URL fallback line is\n")
		sb.WriteString("shown alongside for terminals that cannot render blocks.\n")
	}
	sb.WriteString(fmt.Sprintf("\nThen open the config panel with: %s (secrets are masked on input)\n", entry.Panel))
	sb.WriteString("\n" + imOnboardGenericTail)
	return sb.String(), true
}

// imOnboardStepURLs returns the platform's step deep-links in order (the
// caller auto-opens them via the urlOpener seam, best-effort).
func imOnboardStepURLs(platform string) []string {
	entry, ok := imOnboardWizard()[strings.ToLower(strings.TrimSpace(platform))]
	if !ok {
		return nil
	}
	var urls []string
	for _, s := range entry.Steps {
		if s.URL != "" {
			urls = append(urls, s.URL)
		}
	}
	return urls
}

// renderIMStatusRegistry renders the full-platform registry table:
// every platform in the canonical registry with its configured state and
// adapter name, including NOT-configured platforms (Hermes parity).
func renderIMStatusRegistry(cfg *config.Config) string {
	if cfg == nil {
		return "IM status unavailable (config not loaded)."
	}
	wizard := imOnboardWizard()
	configured := map[string]string{}
	for name, adapterCfg := range cfg.IM.Adapters {
		if !adapterCfg.Enabled {
			continue
		}
		platform := strings.ToLower(strings.TrimSpace(adapterCfg.Platform))
		if platform == "" {
			platform = name
		}
		configured[platform] = name
	}
	names := make([]string, 0, len(wizard))
	for name := range wizard {
		names = append(names, name)
	}
	sort.Strings(names)
	var sb strings.Builder
	sb.WriteString("IM Platform Registry\n")
	sb.WriteString("====================\n")
	sb.WriteString(fmt.Sprintf("%-12s %-12s %s\n", "PLATFORM", "STATE", "ADAPTER"))
	for _, name := range names {
		state := "not configured"
		adapter := "-"
		if adapterName, ok := configured[name]; ok {
			state = "configured"
			adapter = adapterName
		}
		sb.WriteString(fmt.Sprintf("%-12s %-12s %s\n", name, state, adapter))
	}
	// Extra platform configs outside the wizard set (custom adapters).
	for platform, adapterName := range configured {
		if _, known := wizard[platform]; known {
			continue
		}
		sb.WriteString(fmt.Sprintf("%-12s %-12s %s\n", platform, "configured", adapterName))
	}
	sb.WriteString("\n'configured' = enabled adapter in config. Live online/offline state per\nadapter: use the im tool with action 'status'.")
	return sb.String()
}

// handleIMOnboardCommand implements the /im onboard cockpit subcommand
// (#4861, post range-correction): the unified /im surface is a ROUTER, not
// a parallel wizard - it shows the full-state registry, the persisted
// onboarding progress, due notices, and routes each platform to its
// EXISTING vendor onboard panel (/qq, /dingtalk, ...). The step-by-step
// guidance (deep-links + QR notes) lives inside the vendor panels via
// imVendorOnboardGuidance; /im onboard <platform> only records progress
// and points at the panel.
func (m *Model) handleIMOnboardCommand(args []string) {
	if len(args) == 0 {
		text := renderIMOnboardCockpit(m.workspaceDirForIMOnboard())
		m.chatWriteSystem(nextSystemID(), text)
		m.chatListScrollToBottom()
		return
	}
	platform := strings.ToLower(strings.TrimSpace(args[0]))
	if platform == "done" {
		clearIMOnboardRecord(m.workspaceDirForIMOnboard())
		m.chatWriteSystem(nextSystemID(), "IM onboarding record cleared.")
		m.chatListScrollToBottom()
		return
	}
	entry, ok := imOnboardWizard()[platform]
	if !ok {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("unknown platform %q - run /im onboard for the platform registry", platform))
		m.chatListScrollToBottom()
		return
	}
	if err := saveIMOnboardRecord(m.workspaceDirForIMOnboard(), imOnboardRecord{
		Platform:  platform,
		Step:      len(entry.Steps),
		UpdatedAt: time.Now(),
	}); err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("could not persist onboarding progress: %v", err))
		m.chatListScrollToBottom()
		return
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Routing %s onboarding to its existing panel.\n\n", platform))
	sb.WriteString(fmt.Sprintf("  -> Run %s - the panel carries the numbered setup guidance\n", entry.Panel))
	sb.WriteString("     (entry deep-links open automatically; QR-pairing platforms render the\n")
	sb.WriteString("     pairing code as a large terminal QR in-panel), and the masked-secret\n")
	sb.WriteString("     entry + save happen there, as before.\n\n")
	sb.WriteString(imOnboardProbeTail)
	m.chatWriteSystem(nextSystemID(), sb.String())
	m.chatListScrollToBottom()
}

// renderIMOnboardCockpit renders the /im onboard dashboard: per-platform
// routing to the vendor panels, persisted progress rows, and due notices.
func renderIMOnboardCockpit(workspaceDir string) string {
	var sb strings.Builder
	sb.WriteString("IM Onboarding Cockpit\n")
	sb.WriteString("=====================\n")
	rec := loadIMOnboardRecord(workspaceDir)
	if rec != nil {
		sb.WriteString(fmt.Sprintf("In progress: %s (step %d)\n", rec.Platform, rec.Step))
		panel := "-"
		if entry, ok := imOnboardWizard()[rec.Platform]; ok {
			panel = entry.Panel
		}
		sb.WriteString(fmt.Sprintf("  Resume by running the panel: %s ; clear with: /im onboard done\n\n", panel))
	}
	sb.WriteString("Every platform onboards in its OWN existing panel (no parallel flow):\n")
	wiz := imOnboardWizard()
	names := make([]string, 0, len(wiz))
	for name := range wiz {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		marker := ""
		if rec != nil && rec.Platform == name {
			marker = "  <- in progress"
		}
		sb.WriteString(fmt.Sprintf("  %-11s run %s%s\n", name, wiz[name].Panel, marker))
	}
	sb.WriteString("\nProgress + resumability live here (/im onboard); live state: /im status.")
	return sb.String()
}

// imVendorOnboardGuidance renders one platform's numbered setup steps WITH
// deep-links and QR notes - called from the vendor panels' own onboard
// views (post range-correction: guidance lives in the vendor panel, not
// the unified /im surface). Pure renderer; the panel decides placement.
func imVendorOnboardGuidance(platform string) string {
	out, ok := renderIMOnboardSteps(platform)
	if !ok {
		return ""
	}
	return out
}

// handleIMStatusCommand renders the full-state platform registry (#4861
// checklist item 1): runtime adapter states (connected/failed/expired +
// last error) cross-referenced with never-configured platforms.
func (m *Model) handleIMStatusCommand() {
	configured := map[string]string{}
	if m.config != nil {
		for name, adapterCfg := range m.config.IMSnapshot().Adapters {
			if !adapterCfg.Enabled {
				continue
			}
			platform := strings.ToLower(strings.TrimSpace(adapterCfg.Platform))
			if platform == "" {
				platform = name
			}
			configured[platform] = name
		}
	}
	var states []IMAdapterStateInfo
	if m.imManager != nil {
		snap := m.imManager.Snapshot()
		bindings := map[string]bool{}
		for _, b := range snap.CurrentBindings {
			bindings[b.Adapter] = true
		}
		for _, a := range snap.Adapters {
			states = append(states, IMAdapterStateInfo{
				Adapter:   a.Name,
				Platform:  string(a.Platform),
				Healthy:   a.Healthy,
				Status:    a.Status,
				LastError: a.LastError,
				Muted:     bindings[a.Name] && false, // muted detail via tool status; table shows core states
				Disabled:  false,
			})
		}
	}
	m.chatWriteSystem(nextSystemID(), renderIMFullStatusTable(states, configured))
	m.chatListScrollToBottom()
}

// workspaceDirForIMOnboard resolves the record location root.
func (m *Model) workspaceDirForIMOnboard() string {
	if p := m.currentWorkspacePath(); p != "" {
		return p
	}
	return "."
}
