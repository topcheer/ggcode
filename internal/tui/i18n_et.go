package tui

// /et tunnel command user-facing strings (#i18n gap: runtime status lines were
// hardcoded zh-CN regardless of session language). Module catalogs follow the
// registerCatalog pattern (en/zh pair; other languages fall back to en).
func enEtCatalog() map[string]string {
	return map[string]string{
		"et.plan_note":       "\nPlan: %s",
		"et.qr_hint":         "Scan to open on your phone (Esc closes the QR code)",
		"et.not_set":         "(not set)",
		"et.paid_full":       "paid(%s subdomain=%s token=%s)",
		"et.paid_short":      "paid(subdomain=%s token=%s)",
		"et.setup_usage":     "Usage: /et setup <subdomain> <token>\nCurrent config: subdomain=%s (the domain itself is not sensitive, shown as-is: %s) token=%s",
		"et.setup_savefail":  "⚠ et setup save failed: %s",
		"et.setup_saved":     "⬢ et config saved: subdomain=%s token=%s\nNext /et on will request this custom domain (paid plan required).",
		"et.status_active":   "⬢ et tunnel active: %s\nPlan: %s",
		"et.status_inactive": "ⓘ et tunnel not active. /et on to activate; Plan: %s",
		"et.usage":           "Usage: /et [on|off|show|setup <subdomain> <token>|login]",
		"et.login_start":     "⬢ et login: starting browser authorization… (the auth page opens automatically; 10 min timeout)",
		"et.login_browser":   "⬢ et login: finish authorization in your browser (if it did not open, visit:\n%s\nOnce done, return to this session and credentials are saved automatically)",
		"et.login_savefail":  "⚠ et login credential save failed: %s",
		"et.login_nodomains": "(no custom domains = free)",
		"et.login_success":   "⬢ et login success: plan=%s token=%s\nAvailable domains: %s\nNext /et on activates with this account's credentials.",
		"et.on_freehint":     "⬢ Opening et tunnel via free plan (random subdomain, ~seconds). Paid custom domain: /et setup <subdomain> <token>",
	}
}

func zhEtCatalog() map[string]string {
	return map[string]string{
		"et.plan_note":       "\n计划: %s",
		"et.qr_hint":         "扫码可用手机打开（Esc 关闭二维码）",
		"et.not_set":         "(未设置)",
		"et.paid_full":       "paid(%s 子域名=%s token=%s)",
		"et.paid_short":      "paid(子域名=%s token=%s)",
		"et.setup_usage":     "用法: /et setup <子域名> <token>\n当前配置: 子域名=%s（域名本身非敏感，直接展示: %s）token=%s",
		"et.setup_savefail":  "⚠ et setup 保存失败: %s",
		"et.setup_saved":     "⬢ et 配置已保存: 子域名=%s token=%s\n下次 /et on 将请求该自定义域名（需付费计划）。",
		"et.status_active":   "⬢ et tunnel active: %s\n计划: %s",
		"et.status_inactive": "ⓘ et tunnel 未激活。/et on 激活;计划: %s",
		"et.usage":           "用法: /et [on|off|show|setup <子域名> <token>|login]",
		"et.login_start":     "⬢ et login: 正在发起浏览器授权…（授权页将自动打开；超时上限 10 分钟）",
		"et.login_browser":   "⬢ et login: 请在浏览器完成授权（若未自动打开，访问:\n%s\n授权完成后回到本会话，凭据将自动保存）",
		"et.login_savefail":  "⚠ et login 凭据保存失败: %s",
		"et.login_nodomains": "(无自定义域名=free)",
		"et.login_success":   "⬢ et login 成功: 套餐=%s token=%s\n可用域名: %s\n下次 /et on 即以本账户凭据激活。",
		"et.on_freehint":     "⬢ Opening et tunnel via free plan (random subdomain, ~seconds)。付费定制域名: /et setup <子域名> <token>",
	}
}

func init() {
	registerCatalog(enEtCatalog(), zhEtCatalog())
}
