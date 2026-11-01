package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
)

// Guided IM onboarding wizard (#4861). Hermes is used only as a
// completeness checklist: the pattern worth porting is per-platform
// numbered steps WITH anti-confusion notes ("this ID is NOT that ID"),
// plus a status registry that also lists NOT-configured platforms.
// The per-adapter form panels already exist (qq_panel.go etc.) - the
// wizard routes to them instead of duplicating forms.

type imOnboardPlatform struct {
	Key      string // registry key (also the /im onboard <key> argument)
	Title    string
	PanelCmd string // existing panel that owns this platform's form
	Steps    []string
	Notes    []string // anti-confusion traps
}

func imOnboardPlatforms() []imOnboardPlatform {
	return []imOnboardPlatform{
		{
			Key: "qq", Title: "QQ", PanelCmd: "/qq",
			Steps: []string{
				"Open the config panel: /qq",
				"Start pairing - a QR code is rendered in the panel",
				"Scan the QR with the QQ mobile app on the account that should host the bot",
				"Wait for the pairing confirmation, then enable the adapter",
			},
			Notes: []string{
				"The QQ session binds to the account that SCANS the QR - not the account that opened the panel",
				"Pairing can expire if the QR is not scanned promptly; just restart pairing",
			},
		},
		{
			Key: "telegram", Title: "Telegram", PanelCmd: "/tg",
			Steps: []string{
				"Open @BotFather in Telegram, send /newbot, follow the prompts",
				"Copy the bot token (looks like 123456789:AA...) into the panel: /tg",
				"Set allow_from to the numeric user IDs allowed to command the bot",
				"Enable the adapter and send /start to your bot from an allowed account",
			},
			Notes: []string{
				"The allow_from entries are NUMERIC user IDs (123456789) - NOT @usernames",
				"Find your numeric ID by messaging @userinfobot",
				"The bot token is from BotFather - NOT the Telegram Bot API ID your account may have",
			},
		},
		{
			Key: "slack", Title: "Slack", PanelCmd: "/slack",
			Steps: []string{
				"Decide the integration mode: incoming webhook (bot posts only) or bot token (full interaction)",
				"For a webhook: Slack app > Incoming Webhooks > add to workspace, copy the URL",
				"For a bot: Slack app > OAuth & Permissions > install to workspace, copy the bot token (xoxb-...)",
				"Enter the credentials in the panel: /slack, set the target channel",
			},
			Notes: []string{
				"Channel targets are channel IDs starting with C (C0123ABC) - NOT the #channel-name",
				"A webhook URL (hooks.slack.com/...) can only POST messages; it cannot receive commands",
				"The bot token starts with xoxb- - the xoxa-/xoxp- tokens are different scopes",
			},
		},
		{
			Key: "discord", Title: "Discord", PanelCmd: "/discord",
			Steps: []string{
				"Discord Developer Portal > New Application > Bot tab > Reset Token",
				"Copy the bot token into the panel: /discord",
				"Enable the Message Content intent on the Bot tab (required to read commands)",
				"Generate an OAuth2 URL with the bot scope, invite the bot to your server",
			},
			Notes: []string{
				"The bot token is the long secret on the Bot tab - NOT the OAuth2 client ID/secret",
				"The OAuth2 URL is for INVITING the bot, not a credential to paste into ggcode",
				"Guild/server IDs are numeric (right-click > Copy Server ID with developer mode on)",
			},
		},
		{
			Key: "feishu", Title: "Feishu/Lark", PanelCmd: "/feishu",
			Steps: []string{
				"Feishu Open Platform > Create App (企业自建应用)",
				"Copy the App ID and App Secret into the panel: /feishu",
				"Add the bot capability, subscribe to the message event, enter the callback/verification details",
				"Set allow_from to the allowed user IDs, then publish the app version",
			},
			Notes: []string{
				"The credentials are a TRIO: App ID + App Secret (+ optional Encryption Key) - the panel asks for each",
				"App ID is cli_xxxx style; the numeric app number on the portal is NOT the App ID",
				"Events only flow after the app version is PUBLISHED, not just saved",
			},
		},
		{
			Key: "dingtalk", Title: "DingTalk", PanelCmd: "/dingtalk",
			Steps: []string{
				"DingTalk Open Platform > create a robot (应用机器人)",
				"Copy the AppKey/AppSecret/AgentId into the panel: /dingtalk",
				"Configure the message receive mode and callback",
			},
			Notes: []string{
				"AppKey is the client ID - RobotCode is a different identifier; use AppKey where the panel asks for a key",
			},
		},
		{
			Key: "wechat", Title: "WeChat/WeCom", PanelCmd: "/wechat",
			Steps: []string{
				"WeCom admin console > create a self-built app",
				"Copy the CorpID + AgentId + Secret into the panel: /wechat",
				"Set the callback URL and token/encoding key",
			},
			Notes: []string{
				"CorpID (企业ID), AgentId (应用ID) and Secret are THREE different values - all three are required",
				"WeChat personal accounts have no bot API; only WeCom (企业微信) apps do",
			},
		},
		{
			Key: "mattermost", Title: "Mattermost", PanelCmd: "/mattermost",
			Steps: []string{
				"Mattermost > Integrations > Bot Accounts > create a bot",
				"Copy the bot token into the panel: /mattermost",
				"Add the bot to the target team/channel, set the home channel",
			},
			Notes: []string{
				"The Mattermost user ID is a 26-character alphanumeric ID - NOT the @username",
				"Find it via Account Settings > Security > or the API; it starts with a letter and has no @",
			},
		},
		{
			Key: "matrix", Title: "Matrix", PanelCmd: "/matrix",
			Steps: []string{
				"Register a dedicated Matrix account for the bot on your homeserver",
				"Enter homeserver URL + username + password (or access token) in the panel: /matrix",
				"Invite the bot into the rooms it should serve, from an allowed account",
			},
			Notes: []string{
				"The homeserver URL is the full https://... URL - the server name in your @user:server ID may differ from the URL",
				"Allow-from entries are full @user:server IDs - NOT bare usernames",
			},
		},
		{
			Key: "whatsapp", Title: "WhatsApp", PanelCmd: "/whatsapp",
			Steps: []string{
				"Start the pairing flow in the panel: /whatsapp - a QR code is rendered",
				"Scan the QR from WhatsApp mobile > Linked devices > Link a device",
				"Wait for the session confirmation, set allow_from to the allowed numbers",
			},
			Notes: []string{
				"allow_from entries are phone numbers in international format without + or spaces (86138...) - NOT contact names",
				"The linked-device session can be invalidated from the phone; re-pair if messages stop",
			},
		},
		{
			Key: "irc", Title: "IRC", PanelCmd: "/irc",
			Steps: []string{
				"Enter server host/port, TLS, and nickname in the panel: /irc",
				"If the nick is registered, enter the password (NickServ)",
				"Set the channels to join",
			},
			Notes: []string{
				"The NickServ password is the password of your REGISTERED nick - SASL credentials are set separately on servers that require them",
			},
		},
		{
			Key: "signal", Title: "Signal", PanelCmd: "/signal",
			Steps: []string{
				"Install and link a signal-cli / signald daemon for the bot number",
				"Point the panel at the daemon socket/endpoint: /signal",
				"Set allow_from to the allowed Signal numbers",
			},
			Notes: []string{
				"ggcode talks to an EXTERNAL signal daemon - no Signal credentials are entered here",
			},
		},
	}
}

// imOnboardFind returns the registry entry for key ("" when unknown).
func imOnboardFind(key string) *imOnboardPlatform {
	for i := range imOnboardPlatforms() {
		p := imOnboardPlatforms()[i]
		if strings.EqualFold(p.Key, key) {
			return &p
		}
	}
	return nil
}

// imPlatformAliases maps common adapter-name prefixes to registry keys,
// mirroring the slash-command aliases (/tg, /lark, /wa, /mm, /wecom).
var imPlatformAliases = map[string]string{
	"tg": "telegram", "lark": "feishu", "wa": "whatsapp", "mm": "mattermost", "wecom": "wechat", "ding": "dingtalk",
}

// imCanonicalPlatformKey normalizes a name to its registry key ("" when
// it is not a known platform or alias).
func imCanonicalPlatformKey(name string) string {
	lc := strings.ToLower(strings.TrimSpace(name))
	for _, p := range imOnboardPlatforms() {
		if lc == p.Key {
			return p.Key
		}
	}
	if canonical, ok := imPlatformAliases[lc]; ok {
		return canonical
	}
	return ""
}

// imAdapterHeadOf returns the leading identifier of an adapter name
// before the first separator or trailing digit: "tg-bot"->"tg",
// "qq2"->"qq", "matrix_"->"matrix", "slack"->"slack".
func imAdapterHeadOf(name string) string {
	lc := strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexAny(lc, "-_."); i > 0 {
		lc = lc[:i]
	}
	if i := strings.IndexAny(lc, "0123456789"); i > 0 {
		lc = lc[:i]
	}
	return lc
}

// imPlatformConfigured reports whether any configured IM adapter matches
// the platform key (adapter map key or Platform field; both exact names
// and separator-suffixed variants like "slack2"/"tg-bot" match, with
// alias normalization).
func imPlatformConfigured(cfg *config.Config, key string) (configured, enabled bool) {
	if cfg == nil {
		return false, false
	}
	for name, adapter := range cfg.IM.Adapters {
		for _, c := range []string{name, adapter.Platform} {
			if imCanonicalPlatformKey(imAdapterHeadOf(c)) == key || imCanonicalPlatformKey(c) == key {
				return true, adapter.Enabled
			}
		}
	}
	return false, false
}

// renderIMOnboardOverview lists every supported platform with its
// configured state and the command that opens its wizard steps.
func renderIMOnboardOverview(cfg *config.Config) string {
	var sb strings.Builder
	sb.WriteString("IM onboarding - supported platforms\n\n")
	sb.WriteString(fmt.Sprintf("  %-12s %-11s %s\n", "PLATFORM", "STATE", "NEXT"))
	for _, p := range imOnboardPlatforms() {
		configured, enabled := imPlatformConfigured(cfg, p.Key)
		state := "not set up"
		switch {
		case configured && enabled:
			state = "connected"
		case configured:
			state = "configured, disabled"
		}
		sb.WriteString(fmt.Sprintf("  %-12s %-11s /im onboard %s  (form: %s)\n", p.Title, state, p.Key, p.PanelCmd))
	}
	sb.WriteString("\nPick a platform: /im onboard <platform> for step-by-step setup with common-mistake warnings.")
	return sb.String()
}

// renderIMOnboardSteps renders one platform's numbered steps plus its
// anti-confusion notes.
func renderIMOnboardSteps(cfg *config.Config, key string) string {
	p := imOnboardFind(key)
	if p == nil {
		return fmt.Sprintf("Unknown platform %q. Run /im onboard (no arguments) to list supported platforms.", key)
	}
	configured, enabled := imPlatformConfigured(cfg, p.Key)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("IM onboarding - %s\n", p.Title))
	if configured && enabled {
		sb.WriteString("(already connected - steps below for re-setup)\n")
	} else if configured {
		sb.WriteString("(configured but disabled)\n")
	}
	sb.WriteString("\nSteps:\n")
	for i, s := range p.Steps {
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, s))
	}
	if len(p.Notes) > 0 {
		sb.WriteString("\nWatch out (common mistakes):\n")
		for _, n := range p.Notes {
			sb.WriteString(fmt.Sprintf("  ! %s\n", n))
		}
	}
	sb.WriteString(fmt.Sprintf("\nOpen the form: %s", p.PanelCmd))
	return sb.String()
}

// renderIMStatusRegistry is the full-registry /im status table: every
// supported platform (configured or not) plus extra configured adapters
// that have no registry entry.
func renderIMStatusRegistry(cfg *config.Config) string {
	var sb strings.Builder
	sb.WriteString("IM status registry\n\n")
	sb.WriteString(fmt.Sprintf("  %-12s %-22s %s\n", "PLATFORM", "STATE", "ADAPTER"))
	rows := 0
	for _, p := range imOnboardPlatforms() {
		configured, enabled := imPlatformConfigured(cfg, p.Key)
		if !configured {
			sb.WriteString(fmt.Sprintf("  %-12s %-22s %s\n", p.Title, "not configured", "-"))
			rows++
			continue
		}
		state := "configured, disabled"
		if enabled {
			state = "enabled"
		}
		adapterName := imAdapterNameForPlatform(cfg, p.Key)
		sb.WriteString(fmt.Sprintf("  %-12s %-22s %s\n", p.Title, state, adapterName))
		rows++
	}
	sb.WriteString(fmt.Sprintf("\n%d platforms. Live connection state lives in /im (panel); enable/disable per adapter: /im panel or the platform command.", rows))
	return sb.String()
}

func imAdapterNameForPlatform(cfg *config.Config, key string) string {
	names := make([]string, 0, 2)
	for name, adapter := range cfg.IM.Adapters {
		for _, c := range []string{name, adapter.Platform} {
			if imCanonicalPlatformKey(imAdapterHeadOf(c)) == key || imCanonicalPlatformKey(c) == key {
				names = append(names, name)
				break
			}
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
