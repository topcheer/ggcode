package tui

// deCatalog returns the German translation for the given key.
// Keys not yet translated fall through to enCatalog.
func deCatalog(key string) string {
	switch key {
	// --- Workspace & header ---
	case "workspace.tagline":
		return "Geek-AI-Arbeitsumgebung"
	case "header.terminal_native":
		return "Terminal-native KI-Codierung"
	case "session.ephemeral":
		return "flüchtig"

	// --- Agents ---
	case "agents.idle":
		return "inaktiv"
	case "agents.running":
		return "%d aktiv"
	case "cron.firing":
		return "⏰ Cron-Job ausgelöst"
	case "activity.idle":
		return "inaktiv"

	// --- Panel ---
	case "panel.conversation":
		return "Unterhaltung"
	case "panel.composer":
		return "Eingabe"
	case "panel.composer_locked":
		return "Eingabe gesperrt"
	case "panel.commands":
		return "Befehle:"
	case "panel.files":
		return "Dateien:"
	case "panel.agent_status":
		return "Agent-Status"
	case "panel.mode_policy":
		return "Modus-Richtlinie"
	case "panel.session_usage":
		return "Sitzungsnutzung"
	case "panel.metrics":
		return "Metriken"
	case "panel.context":
		return "Kontext"
	case "panel.im":
		return "IM"
	case "panel.mcp":
		return "MCP"
	case "panel.mcp.install_spec_required":
		return "Geben Sie zuerst eine Installations-Spezifikation ein."
	case "panel.mcp.installing_server":
		return "MCP-Server wird installiert..."
	case "panel.mcp.reconnect_unavailable":
		return "Erneut verbinden in dieser Sitzung nicht verfügbar."
	case "panel.mcp.reconnecting":
		return "Verbinde %s erneut..."
	case "panel.mcp.reconnect_failed":
		return "%s konnte nicht erneut verbunden werden."
	case "panel.mcp.uninstalling":
		return "Deinstalliere %s..."
	case "panel.startup":
		return "Initialisierung"
	case "panel.approval_required":
		return "Genehmigung erforderlich"
	case "panel.bypass_approval":
		return "Bypass-Modus-Genehmigung"
	case "panel.review_file_change":
		return "Dateiänderung prüfen"

	// --- Labels ---
	case "label.vendor":
		return "Anbieter"
	case "label.endpoint":
		return "Endpunkt"
	case "label.model":
		return "Modell"
	case "label.mode":
		return "Modus"
	case "label.session":
		return "Sitzung"
	case "label.agents":
		return "Agenten"
	case "label.cwd":
		return "Verz."
	case "label.branch":
		return "Branch"
	case "label.context":
		return "Kontext"
	case "label.skills":
		return "Fähigkeiten"
	case "label.activity":
		return "Aktivität"
	case "label.window":
		return "Fenster"
	case "label.usage":
		return "Nutzung"
	case "label.compact":
		return "kompakt"
	case "label.total":
		return "gesamt"
	case "label.cost":
		return "gesch. Kosten"
	case "label.approval_policy":
		return "Genehmigung"
	case "label.tool_policy":
		return "Werkzeuge"
	case "label.agent_policy":
		return "Agent"
	case "label.tool":
		return "Werkzeug"
	case "label.input":
		return "Eingabe"
	case "label.output":
		return "Ausgabe"
	case "label.cache_read":
		return "Cache-Lesen"
	case "label.cache_write":
		return "Cache-Schreiben"
	case "label.cache_hit":
		return "Cache-Treffer"
	case "label.turns":
		return "Züge"
	case "label.avg_ttft":
		return "Ø TTFT"
	case "label.p95_ttft":
		return "p95 TTFT"
	case "label.avg_duration":
		return "Ø Dauer"
	case "label.p95_duration":
		return "p95 Dauer"
	case "label.avg_think":
		return "Ø Denken"
	case "label.fail_rate":
		return "Fehlerrate"
	case "label.slow_tools":
		return "langsame Werkz."
	case "label.recent_turns":
		return "aktuelle Züge"
	case "label.file":
		return "Datei"
	case "label.directory":
		return "Verzeichnis"

	// --- Context & metrics ---
	case "context.unavailable":
		return "Noch keine Kontextdaten"
	case "metrics.empty":
		return "Noch keine Metriken"
	case "im.none":
		return "Keine Adapter konfiguriert"
	case "im.summary":
		return "%d Adapter • %d gesund"
	case "im.more":
		return "+%d mehr (/qq)"
	case "im.runtime.available":
		return "Laufzeit verfügbar"
	case "im.runtime.disabled":
		return "deaktiviert"
	case "im.runtime.not_started":
		return "aktiviert • Neustart zum Initialisieren"
	case "im.status.not_started":
		return "nicht gestartet"
	case "context.until_compact":
		return "übrig"

	// --- Empty ---
	case "empty.ask":
		return "Fragen Sie nach einem Refactor, Bugfix, Erklärung oder Tests."
	case "empty.tips":
		return "Tipps: @pfad für Dateien, /? für Hilfe, Shift+Tab zum Moduswechsel."
	case "startup.banner":
		return "Terminal-UI wird vorbereitet und Start-Terminalrauschen wird gefiltert. Sie können sofort tippen; dieser Banner verschwindet, sobald der Start abgeschlossen ist."

	// --- Harness ---
	case "harness.views":
		return "Ansichten"
	case "harness.items":
		return "Elemente"
	case "harness.action":
		return "Aktion"
	case "harness.details":
		return "Details"
	case "harness.none":
		return "(keine)"
	case "harness.unknown":
		return "unbekannt"
	case "harness.unscoped":
		return "ohne Gültigkeitsbereich"
	case "harness.unavailable":
		return "Harness nicht verfügbar"
	case "harness.unavailable_intro":
		return "Beginnen Sie hier in einem bestehenden Projekt:"
	case "harness.unavailable_step_init":
		return "  1. Enter oder i drücken, um Harness zu initialisieren"
	case "harness.unavailable_step_refresh":
		return "  2. r drücken, um nach Abschluss zu aktualisieren"
	case "harness.section.init":
		return "Init"
	case "harness.section.check":
		return "Prüfung"
	case "harness.section.doctor":
		return "Doctor"
	case "harness.section.monitor":
		return "Monitor"
	case "harness.section.gc":
		return "GC"
	case "harness.section.contexts":
		return "Kontexte"
	case "harness.section.tasks":
		return "Aufgaben"
	case "harness.section.queue":
		return "Warteschlange"
	case "harness.section.run":
		return "Ausführen"
	case "harness.section.run_queued":
		return "Wartende ausführen"
	case "harness.section.inbox":
		return "Posteingang"
	case "harness.section.review":
		return "Review"
	case "harness.section.promote":
		return "Übernehmen"
	case "harness.section.release":
		return "Release"
	case "harness.section.rollouts":
		return "Rollouts"
	case "harness.hints.unavailable":
		return "Enter/i init Harness • r aktualisieren • Esc schließen"
	case "harness.hints.move":
		return "j/k bewegen"
	case "harness.hints.tab":
		return "Tab wechseln"
	case "harness.hints.refresh":
		return "r aktualisieren"
	case "harness.hints.close":
		return "Esc schließen"
	case "harness.hints.check":
		return "Enter Prüfungen ausführen"
	case "harness.hints.monitor":
		return "Enter Snapshot aktualisieren"
	case "harness.hints.gc":
		return "Enter GC ausführen"
	case "harness.hints.type_goal":
		return "Ziel eingeben"
	case "harness.hints.queue":
		return "Enter einreihen"
	case "harness.hints.run":
		return "Enter ausführen"
	case "harness.hints.focus_input":
		return "Tab Eingabe fokussieren"
	case "harness.hints.rerun":
		return "Enter Fehlgeschlagene wiederholen"
	case "harness.hints.next":
		return "Enter nächste"
	case "harness.hints.all":
		return "a alle"
	case "harness.hints.retry_failed":
		return "f fehlgeschlagene wiederholen"
	case "harness.hints.resume":
		return "s fortsetzen"
	case "harness.hints.promote_owner":
		return "p Besitzer übernehmen"
	case "harness.hints.retry_owner":
		return "f Besitzer wiederholen"
	case "harness.hints.approve":
		return "Enter genehmigen"
	case "harness.hints.reject":
		return "x ablehnen"
	case "harness.hints.promote":
		return "Enter übernehmen"
	case "harness.hints.apply_batch":
		return "Enter Batch anwenden"
	case "harness.hints.advance":
		return "Enter fortschreiten"
	case "harness.hints.approve_gate":
		return "g Gate genehmigen"
	case "harness.hints.pause_resume":
		return "p pausieren/fortsetzen"
	case "harness.hints.abort":
		return "x abbrechen"
	case "harness.hint.primary.check":
		return "Enter drücken, um Prüfungen auszuführen."
	case "harness.hint.primary.monitor":
		return "Enter drücken, um den Monitor-Snapshot zu aktualisieren."
	case "harness.hint.primary.gc":
		return "Enter drücken, um Garbage Collection auszuführen."
	case "harness.hint.primary.queue":
		return "Ziel eingeben, dann Enter drücken zum Einreihen."
	case "harness.hint.primary.run":
		return "Ziel eingeben, dann Enter drücken zum Starten."
	case "harness.hint.primary.tasks":
		return "Enter drücken, um die ausgewählte fehlgeschlagene Aufgabe zu wiederholen."
	case "harness.hint.primary.run_queued":
		return "Enter für nächste; a führt alle aus; f wiederholt fehlgeschlagene; s setzt unterbrochene fort."
	case "harness.hint.primary.inbox":
		return "p drücken, um diesen Besitzer zu übernehmen, oder f, um ihn zu wiederholen."
	case "harness.hint.primary.review":
		return "Enter drücken zum Genehmigen oder x zum Ablehnen."
	case "harness.hint.primary.promote":
		return "Enter drücken, um die ausgewählte Aufgabe zu übernehmen."
	case "harness.hint.primary.release":
		return "Enter drücken, um den aktuellen Release-Batch anzuwenden."
	case "harness.hint.primary.rollouts":
		return "Enter drücken zum Fortschreiten; g genehmigt Gate; p pausiert/setzt fort; x bricht ab."
	case "harness.hint.primary.none":
		return "Für diesen Abschnitt ist keine direkte Eingabe erforderlich."
	case "harness.message.read_only":
		return "Harness-Panel ist schreibgeschützt, während ein anderer Lauf aktiv ist."
	case "harness.message.monitor_refreshed":
		return "Harness-Monitor aktualisiert."
	case "harness.message.rerun_failed_only":
		return "Harness-Aufgabe %s ist %s; nur fehlgeschlagene Aufgaben können wiederholt werden."
	case "harness.message.review_approved":
		return "Review genehmigt für %s"
	case "harness.message.review_rejected":
		return "Review abgelehnt für %s"
	case "harness.message.promoted":
		return "Übernommen: %s"
	case "harness.message.no_release_tasks":
		return "Keine Harness-Aufgaben für das Release bereit."
	case "harness.message.release_applied":
		return "Release-Batch %s angewendet"
	case "harness.message.no_rollouts":
		return "Keine gespeicherten Rollouts gefunden."
	case "harness.message.rollout_advanced":
		return "Rollout %s fortgesetzt"
	case "harness.message.owner_promoted":
		return "%d Aufgabe(n) für %s übernommen"
	case "harness.message.owner_retried":
		return "Fehlgeschlagene Aufgaben für %s wiederholt"
	case "harness.message.gate_approved":
		return "Nächstes Gate für %s genehmigt"
	case "harness.message.rollout_resumed":
		return "Rollout %s fortgesetzt"
	case "harness.message.rollout_paused":
		return "Rollout %s pausiert"
	case "harness.message.rollout_aborted":
		return "Rollout %s abgebrochen"
	case "harness.message.check_passed":
		return "Harness-Prüfung bestanden."
	case "harness.message.check_failed":
		return "Harness-Prüfung hat Probleme gefunden."
	case "harness.message.gc_complete":
		return "Harness-GC abgeschlossen."
	case "harness.message.queue_goal_required":
		return "Zuerst ein Warteschlangen-Ziel in die Eingabe eingeben."
	case "harness.message.queued":
		return "Harness-Aufgabe %s eingereiht"
	case "harness.activity.status":
		return "Harness %s"
	case "harness.log.phase":
		return "Phase"
	case "harness.log.worker":
		return "Worker"
	case "harness.tool.read_file":
		return "Datei lesen"
	case "harness.tool.write_file":
		return "Datei schreiben"
	case "harness.tool.browse_files":
		return "Dateien durchsuchen"
	case "harness.tool.search_code":
		return "Code suchen"
	case "harness.tool.run_command":
		return "Befehl ausführen"
	case "harness.tool.fetch_web_page":
		return "Webseite abrufen"
	case "harness.tool.run_subagent":
		return "Sub-Agent ausführen"
	case "harness.tool.update_task_state":
		return "Aufgabenstatus aktualisieren"
	case "harness.message.run_goal_required":
		return "Zuerst ein Lauf-Ziel in die Eingabe eingeben."
	case "harness.message.no_queued_executed":
		return "Keine eingereihten Harness-Aufgaben wurden ausgeführt."
	case "harness.message.queue_retried":
		return "%d fehlgeschlagene(n) eingereihte Aufgabe(n) wiederholt."
	case "harness.message.queue_resumed":
		return "%d unterbrochene(n) eingereihte Aufgabe(n) fortgesetzt."
	case "harness.message.queue_ran":
		return "%d eingereihte Aufgabe(n) ausgeführt."
	case "harness.preview.not_initialized":
		return "Harness ist in diesem Projekt noch nicht initialisiert.\n\nEnter oder i drücken, um Harness init im aktuellen Repository auszuführen."
	case "harness.preview.check":
		return "Harness-Prüfungen für das aktuelle Projekt ausführen.\n\nEnter: erforderliche Datei-/Inhalts-/Kontextprüfungen sowie konfigurierte Validierungsbefehle ausführen."
	case "harness.preview.gc":
		return "Harness-Garbage Collection ausführen.\n\nEnter: veraltete Aufgaben archivieren, blockierte/laufende Arbeiten abbrechen, alte Logs bereinigen und verwaiste Worktrees entfernen."
	case "harness.preview.queue_help":
		return "Hier das Harness-Ziel eingeben, dann Enter drücken zum Einreihen."
	case "harness.preview.run_help":
		return "Hier das Harness-Ziel eingeben, dann Enter drücken zum Starten."
	case "harness.preview.run_queued":
		return "Warteschlangenstatus:\neingereiht=%d aktiv=%d blockiert=%d fehlgeschlagen=%d\n\nEnter führt die nächste ausführbare Aufgabe aus.\na führt alle ausführbaren Aufgaben aus.\nf wiederholt fehlgeschlagene Aufgaben.\ns setzt unterbrochene Aufgaben fort."
	case "harness.preview.no_owner":
		return "Kein Harness-Besitzer ausgewählt."
	case "harness.preview.no_context":
		return "Kein Harness-Kontext ausgewählt."
	case "harness.preview.no_task":
		return "Keine Harness-Aufgabe ausgewählt."
	case "harness.preview.project_not_initialized":
		return "Harness ist in diesem Projekt noch nicht initialisiert."
	case "harness.preview.project_initialized":
		return "Harness ist initialisiert."
	case "harness.preview.project_help":
		return "/harness verwenden, um die Steuerungsebene zu durchsuchen und zu bedienen."
	case "harness.preview.no_doctor":
		return "Kein Harness-Doctor-Bericht."
	case "harness.preview.monitor_unavailable":
		return "Harness-Monitor nicht verfügbar."
	case "harness.label.context_title":
		return "Kontext"
	case "harness.label.owner_title":
		return "Besitzer"
	case "harness.label.id":
		return "id"
	case "harness.label.status":
		return "status"
	case "harness.label.goal":
		return "ziel"
	case "harness.label.attempts":
		return "versuche"
	case "harness.label.depends_on":
		return "abhängt_von"
	case "harness.label.context":
		return "kontext"
	case "harness.label.workspace":
		return "workspace"
	case "harness.label.branch":
		return "branch"
	case "harness.label.worker":
		return "worker"
	case "harness.label.progress":
		return "fortschritt"
	case "harness.label.verification":
		return "verifikation"
	case "harness.label.changed_files":
		return "geänderte_dateien"
	case "harness.label.delivery_report":
		return "delivery_report"
	case "harness.label.delivery_report_human":
		return "Lieferbericht"
	case "harness.label.log":
		return "log"
	case "harness.label.review":
		return "review"
	case "harness.label.review_notes":
		return "review_notizen"
	case "harness.label.promotion":
		return "promotion"
	case "harness.label.promotion_notes":
		return "promotion_notizen"
	case "harness.label.release_batch":
		return "release_batch"
	case "harness.label.release_batch_human":
		return "Release-Batch"
	case "harness.label.release_notes":
		return "release_notes"
	case "harness.label.error":
		return "fehler"
	case "harness.label.name":
		return "name"
	case "harness.label.description":
		return "beschreibung"
	case "harness.label.owner":
		return "besitzer"
	case "harness.label.commands":
		return "befehle"
	case "harness.label.tasks":
		return "aufgaben"
	case "harness.label.rollouts":
		return "rollouts"
	case "harness.label.gates":
		return "gates"
	case "harness.label.latest":
		return "neueste"
	case "harness.label.repo":
		return "repo"
	case "harness.label.config":
		return "config"
	case "harness.label.project":
		return "projekt"
	case "harness.label.structure":
		return "struktur"
	case "harness.label.contexts":
		return "kontexte"
	case "harness.label.workers":
		return "worker"
	case "harness.label.workflow":
		return "workflow"
	case "harness.label.quality":
		return "qualität"
	case "harness.label.worktrees":
		return "worktrees"
	case "harness.label.snapshot":
		return "snapshot"
	case "harness.label.events":
		return "ereignisse"
	case "harness.label.target":
		return "ziel"
	case "harness.label.review_ready":
		return "review_bereit"
	case "harness.label.promotion_ready":
		return "promotion_bereit"
	case "harness.label.retryable":
		return "wiederholbar"
	case "harness.task_title":
		return "Harness-Aufgabe"
	case "harness.doctor_title":
		return "Harness-Doctor"
	case "harness.monitor_title":
		return "Harness-Monitor"
	case "harness.latest_task":
		return "Neueste Aufgabe"
	case "harness.latest_event":
		return "Neuestes Ereignis"
	case "harness.focus":
		return "Fokus"
	case "harness.status.ok":
		return "ok"
	case "harness.status.needs_attention":
		return "Aufmerksamkeit erforderlich"
	case "harness.group.review":
		return "review"
	case "harness.group.promotion":
		return "promotion"
	case "harness.group.retry":
		return "wiederholen"
	case "harness.review_ready_short":
		return "review"
	case "harness.promote_ready_short":
		return "übernehmen"
	case "harness.tasks_count":
		return "Aufgaben"
	case "harness.input_empty":
		return "(Eingabefeld ist leer)"
	case "harness.no_waves":
		return "keine Wellen"
	case "harness.mixed":
		return "gemischt"

	// --- Hints ---
	case "hint.autocomplete":
		return "Tab/Shift+Tab wechseln • Enter anwenden • Esc schließen"
	case "hint.mention":
		return "@ hängt Dateien/Ordner an • Tab/Shift+Tab wechseln • Enter anwenden"
	case "hint.mode":
		return "Modus"

	// --- Mode approval ---
	case "mode.approval.ask":
		return "bei Bedarf fragen"
	case "mode.approval.none":
		return "keine Genehmigungs-Stopps"
	case "mode.approval.critical":
		return "nur kritische"
	case "mode.tools.rules":
		return "Werkzeugregeln befolgen"
	case "mode.tools.readonly":
		return "nur Lesezugriff"
	case "mode.tools.safe":
		return "nur sichere Operationen"
	case "mode.tools.open":
		return "fast alle Werkzeuge"
	case "mode.agent.waits":
		return "wartet auf Sie"
	case "mode.agent.autocontinue":
		return "macht weiter"

	// --- Hints ---
	case "hint.enter_send":
		return "Enter senden"
	case "hint.ctrlv_image":
		return "Ctrl+V / Ctrl+Shift+V Bild einfügen"
	case "hint.ctrlr_sidebar":
		return "Ctrl+R Seitenleiste"
	case "hint.help":
		return "/? Hilfe"
	case "hint.add_context":
		return "@ Kontext hinzufügen"
	case "hint.scroll":
		return "PgUp/PgDn scrollen"
	case "hint.shift_tab_mode":
		return "Shift+Tab Modus"
	case "hint.ctrlc_cancel":
		return "Ctrl+C abbrechen"
	case "hint.ctrlc_exit":
		return "Ctrl+C löschen/beenden"
	case "hint.image_attached":
		return "Bild angehängt"
	case "hint.image_attached_count":
		return "%d Bild(er) angehängt"
	case "hint.follow_panel":
		return "Ctrl+N folgen"
	case "hint.unfollow_panel":
		return "Ctrl+N nicht mehr folgen"

	// --- Queued ---
	case "queued.count":
		return "%d eingereiht"
	case "queued.output":
		return "[eingereiht %d ausstehend]\n\n"
	case "interrupt.delivered":
		return "[an aktiven Lauf geliefert; Plan wird überarbeitet]\n"

	// --- Status ---
	case "status.thinking":
		return "Denke nach..."
	case "status.writing":
		return "Schreibe..."
	case "status.cancelling":
		return "Wird abgebrochen..."
	case "status.compacting":
		return "Komprimiere Kontext..."
	case "status.compacted":
		return "[Unterhaltung komprimiert]"
	case "reasoning.effort.status":
		return "Denkanstrengung: %s"
	case "reasoning.effort.set":
		return "Denkanstrengung für diese Sitzung auf %s gesetzt"
	case "reasoning.effort.unsupported.status":
		return "Denkanstrengung vom aktuellen Anbieter nicht unterstützt"
	case "reasoning.effort.unsupported":
		return "Denkanstrengung wird vom aktuellen Anbieter nicht unterstützt"

	// --- Follow ---
	case "follow.loading":
		return "  Folgen-Ansicht wird geladen..."
	case "follow.active_agent":
		return "Folge Agent %s — Eingabe pausiert. Esc drücken zum Zurückkehren."
	case "follow.active_teammate":
		return "Folge Teammitglied %s — Eingabe pausiert. Esc drücken zum Zurückkehren."
	case "follow.status_running":
		return "aktiv"
	case "follow.status_done":
		return "fertig"
	case "follow.more":
		return "  +%d mehr"
	case "follow.hint":
		return "  ↑↓←→ wechseln  Esc schließen"

	// --- Tools ---
	case "status.tools_used":
		return "%d Werkzeuge verwendet"
	case "tool.done":
		return "fertig"
	case "tool.failed":
		return "fehlgeschlagen"
	case "tool.no_output":
		return "keine Ausgabe"
	case "tool.output":
		return "ausgabe"
	case "tool.content":
		return "inhalt"
	case "tool.match":
		return "treffer"
	case "tool.matches":
		return "treffer"
	case "tool.entry":
		return "eintrag"
	case "tool.result":
		return "ergebnis"

	// --- Approval ---
	case "approval.rejected":
		return "  Abgelehnt.\n"
	case "approval.allow":
		return "Erlauben"
	case "approval.allow_always":
		return "Immer erlauben"
	case "approval.deny":
		return "Ablehnen"
	case "approval.accept":
		return "Akzeptieren"
	case "approval.reject":
		return "Ablehnen"

	// --- Exit ---
	case "exit.confirm":
		return "Ctrl-C erneut drücken zum Beenden.\n\n"
	case "cancel.confirm":
		return "Ctrl-C oder Esc erneut drücken, um den laufenden Agent abzubrechen.\n\n"
	case "interrupted":
		return "[unterbrochen]\n\n"

	// --- Language ---
	case "lang.current":
		return "Aktuelle Sprache: %s\n/lang für interaktive Auswahl verwenden, oder /lang <en|zh-CN> zum direkten Wechseln.\n%s\n\n"
	case "lang.invalid":
		return "Nicht unterstützte Sprache: %s\n%s\n\n"
	case "lang.switch":
		return "Sprache gewechselt zu: %s\n\n"
	case "lang.selection.current":
		return " Aktuell: %s"
	case "lang.selection.hint":
		return " Tab/j/k bewegen • Enter bestätigen • e/z Kurzbefehle • Esc abbrechen"
	case "lang.first_use.title":
		return "Wählen Sie Ihre bevorzugte Sprache"
	case "lang.first_use.body":
		return " Wählen Sie die Sprache, die ggcode ab sofort verwenden soll."
	case "lang.first_use.hint":
		return " Tab/j/k bewegen • Enter bestätigen • e/z Kurzbefehle"

	// --- Mode ---
	case "mode.current":
		return "Aktueller Modus: %s\nVerwendung: /mode <supervised|plan|auto|bypass|autopilot>\n  supervised  Fragen, wenn ein Werkzeug keine explizite Regel hat\n  plan        Nur-Lese-Erkundung; Schreiben und Befehle ablehnen\n  auto        Sichere Operationen erlauben, gefährliche ablehnen\n  bypass      Fast alles erlauben; nur bei kritischen Aktionen stoppen\n  autopilot   bypass + weitermachen, wenn das Modell zurückfragt\n\n"
	case "mode.persist_failed":
		return "Modus-Einstellung konnte nicht gespeichert werden: %v"

	// --- Input ---
	case "input.placeholder":
		return "Nachricht eingeben... ($ Shell, # Chat)"

	// --- Model panel ---
	case "panel.model_filter.prompt":
		return "Filter> "
	case "panel.model_filter.placeholder":
		return "Tippen zum Filtern von Modellen"
	case "panel.model_list.none":
		return "(keine)"
	case "panel.model_list.no_matches":
		return "(keine Treffer)"
	case "panel.model_list.showing":
		return "zeige %d/%d Modelle"
	case "panel.model_list.hidden_above":
		return "%d darüber"
	case "panel.model_list.hidden_more":
		return "%d mehr"

	// --- Provider panel ---
	case "panel.provider.vendors":
		return "Anbieter"
	case "panel.provider.endpoints":
		return "Endpunkte"
	case "panel.provider.models":
		return "Modelle"
	case "panel.provider.active_draft":
		return "Aktiver Entwurf"
	case "panel.provider.protocol":
		return "Protokoll"
	case "panel.provider.protocol.unknown":
		return "(unbekannt)"
	case "panel.provider.auth":
		return "Auth"
	case "panel.provider.env_var":
		return "Env-Var"
	case "panel.provider.api_key":
		return "API-Schlüssel"
	case "panel.provider.api_key.missing":
		return "fehlt"
	case "panel.provider.api_key.configured":
		return "konfiguriert"
	case "panel.provider.auth.connected":
		return "verbunden"
	case "panel.provider.auth.not_connected":
		return "nicht verbunden"
	case "panel.provider.base_url":
		return "Basis-URL"
	case "panel.provider.base_url.not_set":
		return "(nicht gesetzt)"
	case "panel.provider.enterprise_url":
		return "Enterprise-URL"
	case "panel.provider.tags":
		return "Tags"
	case "panel.provider.model.set_with_m":
		return "(mit m gesetzt)"
	case "panel.provider.edit":
		return "Bearbeiten"
	case "panel.provider.edit.vendor_api_key":
		return "anbieter api-schlüssel"
	case "panel.provider.edit.endpoint_api_key":
		return "endpunkt api-schlüssel"
	case "panel.provider.edit.endpoint_base_url":
		return "endpunkt basis-url"
	case "panel.provider.edit.custom_model":
		return "benutzerdefiniertes modell"
	case "panel.provider.edit.new_endpoint_name":
		return "neuer endpunktname"
	case "panel.provider.hint.edit":
		return "Enter speichern • Esc abbrechen"
	case "panel.provider.hint.main":
		return "Tab/Shift+Tab Fokus wechseln • j/k bewegen • / Filter fokussieren • Enter oder s anwenden • a Anbieter-Schlüssel • u Endpunkt-Schlüssel • b Basis-URL • m benutzerdef. Modell • e Endpunkt hinzufügen • Esc schließen"
	case "panel.provider.hint.copilot":
		return "GitHub Copilot: l anmelden • x abmelden • b Enterprise-Domain bearbeiten"
	case "panel.provider.saved":
		return "Gespeichert."
	case "panel.provider.saved_activated":
		return "Gespeichert und aktiviert."
	case "panel.provider.login.starting":
		return "GitHub Copilot-Anmeldung wird gestartet..."
	case "panel.provider.login.instructions":
		return "%s öffnen und Code %s eingeben. Warten auf Autorisierung..."
	case "panel.provider.login.copied":
		return "Gerätecode in Zwischenablage kopiert."
	case "panel.provider.login.copy_failed":
		return "Kopieren des Gerätecodes fehlgeschlagen: %s"
	case "panel.provider.login.browser_opened":
		return "Verifizierungsseite im Browser geöffnet."
	case "panel.provider.login.browser_failed":
		return "Öffnen der Verifizierungsseite fehlgeschlagen: %s"
	case "panel.provider.login.success":
		return "GitHub Copilot verbunden."
	case "panel.provider.login.failed":
		return "GitHub Copilot-Anmeldung fehlgeschlagen: %s"
	case "panel.provider.logout.success":
		return "GitHub Copilot getrennt."
	case "panel.provider.refreshing_vendor":
		return "Modelle für %s werden aktualisiert..."
	case "panel.provider.refresh.save_failed":
		return "Modelle aktualisiert, aber Speichern der Konfiguration fehlgeschlagen: %s"
	case "panel.provider.refresh.partial":
		return "%d Endpunkt(en) aktualisiert, %d Modell(e) entdeckt. Einige Endpunkte fehlgeschlagen: %v"
	case "panel.provider.refresh.success":
		return "%d Endpunkt(en) aktualisiert, %d Modell(e) entdeckt."
	case "panel.provider.refresh.failed":
		return "Modellaktualisierung fehlgeschlagen: %s"
	case "panel.provider.refresh.none":
		return "Keine aktualisierbaren Endpunkte für diesen Anbieter."

	// --- Model panel details ---
	case "panel.model.models":
		return "Modelle"
	case "panel.model.vendor":
		return "Anbieter"
	case "panel.model.endpoint":
		return "Endpunkt"
	case "panel.model.protocol":
		return "Protokoll"
	case "panel.model.source":
		return "Quelle"
	case "panel.model.source.builtin":
		return "eingebaut"
	case "panel.model.source.remote":
		return "remote"
	case "panel.model.refreshing":
		return "Neueste Modelle werden aktualisiert..."
	case "panel.model.hint.main":
		return "j/k bewegen • Enter oder s anwenden • w Kontextfenster • o max. Tokens • r aktualisieren • / filtern • Esc schließen"
	case "panel.model.hint.edit":
		return "Enter speichern • Esc abbrechen (0 oder leer = auto, K/M/G-Suffix zulässig z.B. 256k)"
	case "panel.model.context_window":
		return "Kontextfenster"
	case "panel.model.max_tokens":
		return "Max. Ausgabe-Tokens"
	case "panel.model.edit":
		return "Bearbeiten"
	case "panel.model.saved_runtime_inactive":
		return "Konfiguration gespeichert, aber aktuelle Laufzeit ist noch inaktiv: %s"
	case "panel.model.context_applied":
		return "context_window=%d, max_tokens=%d angewendet (gespeichert)"
	case "panel.model.context_cleared":
		return "Auf Auto-Erkennung zurückgesetzt (gespeichert)"
	case "panel.model.switched":
		return "Modell gewechselt zu %s."
	case "panel.model.refresh.save_failed":
		return "Modelle aktualisiert, aber Speichern der Konfiguration fehlgeschlagen: %s"
	case "panel.model.refresh.builtin_reason":
		return "Eingebaute Modelle verwenden: %s"
	case "panel.model.refresh.remote_loaded":
		return "%d remote Modell(e) geladen."
	case "panel.model.refresh.builtin_loaded":
		return "Eingebaute Modelle geladen."
	case "panel.model.vendor_not_found":
		return "厂商未找到"
	case "panel.model.endpoint_not_found":
		return "端点未找到"
	case "panel.model.save_failed":
		return "保存失败：%s"
	case "panel.model.endpoint_save_failed":
		return "端点配置保存失败：%s"

	// --- Commands ---
	case "command.unknown":
		return "Unbekannter Befehl: %s\n"
	case "command.retry_empty":
		return "Keine vorherige Eingabe zum Wiederholen."
	case "command.retry_busy":
		return "Agent ist beschäftigt. Warten Sie, bis der aktuelle Lauf abgeschlossen ist, bevor Sie es erneut versuchen."
	case "command.edit_empty":
		return "Keine vorherige Eingabe zum Bearbeiten."
	case "command.edit_busy":
		return "Agent ist beschäftigt. Warten Sie, bis der aktuelle Lauf abgeschlossen ist, bevor Sie bearbeiten."
	case "command.edit_ready":
		return "Letzte Eingabe geladen — bearbeiten und Enter drücken zum Senden."
	case "command.help_hint":
		return "/help für verfügbare Befehle eingeben\n\n"
	case "command.usage.allow":
		return "Verwendung: /allow <werkzeugname>\n\n"
	case "command.usage.resume":
		return "Verwendung: /resume <sitzungs-id>\n\n"
	case "command.usage.export":
		return "Verwendung: /export <sitzungs-id>\n\n"

	// --- Init ---
	case "init.resolve_failed":
		return "Init-Ziel konnte nicht aufgelöst werden: %v\n\n"
	case "init.generate_failed":
		return "GGCODE.md-Inhalt konnte nicht generiert werden: %v\n\n"
	case "init.collecting":
		return "Projektwissen wird gesammelt..."
	case "init.prompt.title":
		return "Projekt initialisieren"
	case "init.prompt.body":
		return "Keine GGCODE.md in diesem Projekt gefunden. Eine erstellen, damit der Agent Ihre Codebase-Konventionen versteht?"
	case "init.prompt.yes":
		return "Erstellen"
	case "init.prompt.no":
		return "Überspringen"
	case "init.prompt.hint":
		return " y = GGCODE.md erstellen • n/Esc = überspringen"

	// --- Model commands ---
	case "command.model_switched":
		return "Modell gewechselt zu: %s (Anbieter: %s)\n\n"
	case "command.model_failed":
		return "Modellwechsel fehlgeschlagen: %v\n\n"
	case "command.model_current":
		return "Aktuelles Modell: %s (Anbieter: %s)\nVerfügbare Modelle: %s\n/model für das Modell-Panel verwenden oder /model <modellname> zum direkten Wechseln.\n\n"
	case "command.provider_unknown":
		return "Unbekannter Anbieter: %s (verfügbar: %v)\n\n"
	case "command.provider_switched":
		return "Anbieter gewechselt zu: %s (Modell: %s)\n\n"
	case "command.provider_failed":
		return "Anbieterauswahl konnte nicht aktualisiert werden: %v\n\n"
	case "command.provider_current":
		return "Aktueller Anbieter: %s (Endpunkt: %s, Modell: %s)\nVerfügbare Anbieter: %s\nVerfügbare Endpunkte: %s\nVerwendung: /provider [anbieter] [endpunkt]\n\n"
	case "command.allow_set":
		return "✓ %s ist jetzt immer erlaubt\n\n"
	case "command.custom":
		return "Benutzerdefinierter Befehl /%s:\n"
	case "command.mention_error":
		return "Erwähnungserweiterungsfehler: %v"

	// --- Sessions ---
	case "session.list_failed":
		return "Fehler beim Auflisten von Sitzungen: %v\n\n"
	case "session.untitled":
		return "unbenannt"
	case "session.store_missing":
		return "Sitzungsspeicher nicht konfiguriert.\n\n"
	case "session.none":
		return "Keine Sitzungen gefunden.\n\n"
	case "session.list.title":
		return "Sitzungen:\n\n"
	case "session.list.item":
		return "  %d. %s  %s  (%s)\n"
	case "session.list.hint":
		return "\n/resume <id> verwenden, um eine Sitzung fortzusetzen\n\n"
	case "session.new":
		return "Neue Sitzung: %s\n\n"
	case "session.resume":
		return "Sitzung fortgesetzt: %s — %s (%d Nachrichten)\n\n"
	case "session.resume_failed":
		return "Fehler beim Fortsetzen der Sitzung %s: %v\n\n"
	case "session.resume_fallback":
		return "Neue Sitzung wird stattdessen gestartet.\n\n"
	case "session.export_failed":
		return "Fehler beim Exportieren der Sitzung: %v\n\n"
	case "session.write_failed":
		return "Fehler beim Schreiben der Datei: %v\n\n"
	case "session.exported":
		return "Sitzung %s nach %s exportiert\n\n"

	// --- Checkpoints ---
	case "checkpoint.disabled":
		return "Checkpointing nicht aktiviert.\n\n"
	case "checkpoint.undo_failed":
		return "Rückgängig fehlgeschlagen: %v\n\n"
	case "checkpoint.undid":
		return "Rückgängig gemacht: %s auf %s (Checkpoint %s)\n"
	case "checkpoint.none":
		return "Keine Checkpoints.\n\n"

	// --- Files ---
	case "files.disabled":
		return "Checkpointing nicht aktiviert.\n\n"
	case "files.none":
		return "Keine vom Agent geänderten Dateien in dieser Sitzung.\n\n"
	case "files.title":
		return "Vom Agent geänderte Dateien (%d Dateien, %d Bearbeitungen):\n\n"
	case "files.item":
		return "  %s  %d Bearb.  zuletzt: %s%s\n"
	case "files.hint":
		return "\n/undo verwenden, um die letzte Bearbeitung rückgängig zu machen, /checkpoints für Details.\n\n"
	case "checkpoint.list.title":
		return "Checkpoints (%d):\n\n"
	case "checkpoint.list.item":
		return "  %d. %s  %s  %s  %s\n"
	case "checkpoint.list.hint":
		return "\n/undo verwenden, um die letzte rückgängig zu machen.\n\n"

	// --- Memory ---
	case "memory.auto_unavailable":
		return "Auto-Memory nicht initialisiert.\n\n"
	case "memory.list_failed":
		return "Fehler beim Auflisten der Erinnerungen: %v\n\n"
	case "memory.none":
		return "Keine Auto-Erinnerungen gespeichert.\n\n"
	case "memory.auto_title":
		return "Auto-Erinnerungen:\n"
	case "memory.clear_failed":
		return "Fehler beim Löschen der Erinnerungen: %v\n\n"
	case "memory.cleared":
		return "Alle Auto-Erinnerungen gelöscht.\n\n"
	case "memory.title":
		return "Memory:\n"
	case "memory.project":
		return "Projekt-Memory:\n"
	case "memory.project_none":
		return "  Keine Projekt-Memory-Dateien geladen.\n"
	case "memory.auto":
		return "Auto-Memory:\n"
	case "memory.auto_none":
		return "  Keine Auto-Erinnerungen geladen.\n"
	case "memory.usage":
		return "\nVerwendung: /memory [list|clear]\n\n"

	// --- Compact ---
	case "compact.unavailable":
		return "Kontext-Manager nicht verfügbar.\n\n"
	case "compact.failed":
		return "Komprimierung fehlgeschlagen: %v\n\n"
	case "compact.done":
		return "Unterhaltungsverlauf komprimiert.\n\n"
	case "compact.done_with_stats":
		return "Unterhaltungsverlauf komprimiert (%d → %d Tokens).\n\n"

	// --- Todo ---
	case "todo.cleared":
		return "Todo-Liste gelöscht.\n\n"
	case "todo.clear_failed":
		return "Fehler beim Löschen der Todos: %v\n\n"
	case "todo.none":
		return "Keine Todo-Liste gefunden. Verwenden Sie das todo_write Werkzeug, um eine zu erstellen.\n\n"
	case "todo.read_failed":
		return "Fehler beim Lesen der Todos: %v\n\n"
	case "todo.parse_failed":
		return "Fehler beim Parsen der Todos: %v\n\n"
	case "todo.title":
		return "Todo-Liste:\n%s\n\n"

	// --- Bug report ---
	case "bug.title":
		return "=== Fehlerbericht-Diagnose ===\n\n"
	case "bug.version":
		return "Version: %s\n"
	case "bug.os":
		return "OS: %s %s\n"
	case "bug.go":
		return "Go: %s\n"
	case "bug.provider":
		return "Anbieter: %s\n"
	case "bug.model":
		return "Modell: %s\n"
	case "bug.session":
		return "Sitzung: %s (%d Nachrichten)\n"
	case "bug.mcp":
		return "MCP-Server: %d\n"
	case "bug.last_error":
		return "Letzter Fehler: %s\n"
	case "bug.hint":
		return "\nBitte geben Sie diese Informationen beim Melden eines Fehlers an.\n\n"

	// --- Config ---
	case "config.usage":
		return "Verwendung: /config set <key> <value>\n\nKeys: model, vendor, endpoint, language, apikey [--vendor]\n\nEndpunkte: /config add-endpoint <name> <base_url> [--protocol openai] [--apikey sk-xxx]\n          /config remove-endpoint <name>\n\n"
	case "config.not_loaded":
		return "Konfiguration nicht geladen.\n\n"
	case "config.model_set":
		return "Konfig: model = %s\n\n"
	case "config.provider_set":
		return "Konfig: anbieter = %s\n\n"
	case "config.language_set":
		return "Konfig: sprache = %s\n\n"
	case "config.unknown_key":
		return "Unbekannter Konfig-Schlüssel: %s\nUnterstützt: model, provider, language\n\n"
	case "config.title":
		return "Aktuelle Konfiguration:\n"
	case "status.title":
		return "Status:\n"

	// --- Update ---
	case "panel.update":
		return "Update"
	case "label.version":
		return "Version"
	case "label.latest":
		return "Neueste"
	case "update.sidebar_hint":
		return "Neues Release verfügbar. /update ausführen."
	case "update.up_to_date":
		return "Sie sind auf dem neuesten Stand."
	case "update.available":
		return "Update verfügbar: %s"
	case "update.current":
		return "aktuell: %s (neueste: %s)"
	case "update.unknown":
		return "noch nicht geprüft"
	case "update.check_failed":
		return "Prüfung fehlgeschlagen: %s"
	case "update.unavailable":
		return "Update in dieser Sitzung nicht verfügbar.\n\n"
	case "update.preparing":
		return "Update wird vorbereitet"
	case "update.failed":
		return "Update fehlgeschlagen: %v\n\n"
	case "update.restart_failed":
		return "Update vorbereitet, aber Neustart fehlgeschlagen: %v\n\n"
	case "update.pm_hint.brew":
		return "Update installiert. Hinweis: ggcode wurde über Homebrew installiert.\nFühren Sie `brew upgrade ggcode` aus, um Homebrew synchron zu halten.\n\n"
	case "update.pm_hint.scoop":
		return "Update installiert. Hinweis: ggcode wurde über Scoop installiert.\nFühren Sie `scoop update ggcode` aus, um Scoop synchron zu halten.\n\n"
	case "update.pm_hint.winget":
		return "Update installiert. Hinweis: ggcode wurde über winget installiert.\nFühren Sie `winget upgrade ggcode` aus, um winget synchron zu halten.\n\n"
	case "update.pm_hint.snap":
		return "Update installiert. Hinweis: ggcode wurde über Snap installiert.\nFühren Sie `sudo snap refresh ggcode` aus, um Snap synchron zu halten.\n\n"
	case "update.other_installs":
		return "Weitere ggcode-Installationen auf diesem System gefunden:\n%s\nWenn eine andere ggcode-Installation zuerst im PATH erscheint, erwägen Sie, diese ebenfalls zu aktualisieren oder die PATH-Reihenfolge anzupassen.\n\n"
	case "update.dual_scope":
		return "Warnung: Sowohl Benutzer- als auch systemweite ggcode-Installationen gefunden:\n  Benutzer: %s\n  System: %s\nDies kann zu PATH-Konflikten führen. Erwägen Sie, eine über Einstellungen > Apps zu deinstallieren.\n\n"

	// --- Plugins ---
	case "plugins.unavailable":
		return "Plugin-Manager nicht verfügbar.\n\n"
	case "plugins.none":
		return "Keine Plugins geladen.\n\n"
	case "plugins.title":
		return "Plugins:\n"

	// --- MCP ---
	case "mcp.none":
		return "Keine MCP-Server konfiguriert.\n\n"
	case "mcp.title":
		return "MCP-Server:\n"
	case "mcp.active_tools":
		return "Aktive Werkzeuge"
	case "mcp.more":
		return "… %d mehr • /mcp"

	// --- Image ---
	case "image.usage":
		return "Verwendung: /image <pfad/zur/datei.png> oder /image paste\n"
	case "image.formats":
		return "Unterstützte Formate: PNG, JPEG, GIF, WebP (max. 20MB)\n\n"
	case "image.attached":
		return "Bild angehängt: %s\n"
	case "image.attached_hint":
		return "Nachricht senden, um das Bild einzuschließen, oder /image für ein weiteres.\n\n"
	case "image.clipboard_failed":
		return "Bild konnte nicht aus der Zwischenablage eingefügt werden: %v"
	case "image.clipboard_no_image_windows":
		return "Kein Bild in der Zwischenablage gefunden. Unter Windows wird Ctrl+V oft vom Terminal abgefangen. Versuchen Sie Ctrl+Shift+V oder /image paste."

	// --- Agents ---
	case "agents.unavailable":
		return "Sub-Agent-Manager nicht konfiguriert.\n\n"
	case "agents.none":
		return "Noch keine Sub-Agenten erstellt.\nVerwendung: Die LLM kann das spawn_agent Werkzeug verwenden, um Sub-Agenten zu erstellen.\n\n"
	case "agents.title":
		return "%d Sub-Agent(en):\n"
	case "agents.item":
		return "  %s [%s]%s - %s\n"
	case "agents.hint":
		return "\n/agent <id> für Details verwenden, /agent cancel <id> zum Abbrechen.\n\n"
	case "agent.usage":
		return "Verwendung: /agent <id> oder /agent cancel <id>\n\n"
	case "agent.cancelled":
		return "Sub-Agent %s abgebrochen\n\n"
	case "agent.cancel_failed":
		return "%s konnte nicht abgebrochen werden (nicht gefunden oder nicht aktiv)\n\n"
	case "agent.not_found":
		return "Sub-Agent %s nicht gefunden\n\n"
	case "agent.title":
		return "Agent: %s\nStatus: %s\nAufgabe: %s\n"
	case "agent.result":
		return "Ergebnis: %s\n"
	case "agent.error":
		return "Fehler: %v\n"

	// --- Slash command descriptions ---
	case "slash.help":
		return "Hilfemeldung anzeigen"
	case "slash.sessions":
		return "Gespeicherte Sitzungen auflisten"
	case "slash.resume":
		return "Vorherige Sitzung fortsetzen"
	case "slash.export":
		return "Sitzung als Markdown exportieren"
	case "slash.model":
		return "Modell wechseln"
	case "slash.provider":
		return "Anbieter-Manager öffnen"
	case "slash.clear":
		return "Unterhaltung löschen"
	case "slash.mcp":
		return "MCP-Server anzeigen"
	case "slash.memory":
		return "Memory verwalten"
	case "slash.undo":
		return "Letzte Dateibearbeitung rückgängig machen"
	case "slash.files":
		return "Vom Agent geänderte Dateien anzeigen"
	case "slash.checkpoints":
		return "Checkpoints auflisten"
	case "slash.allow":
		return "Ein Werkzeug immer erlauben"
	case "slash.plugins":
		return "Geladene Plugins auflisten"
	case "slash.image":
		return "Bild anhängen"
	case "slash.init":
		return "Projekt-GGCODE.md generieren"
	case "slash.harness":
		return "Harness-Workflow-Befehle ausführen"
	case "slash.lang":
		return "Oberflächensprache wechseln"
	case "slash.skills":
		return "Verfügbare Fähigkeiten durchsuchen"
	case "slash.exit":
		return "ggcode beenden"
	case "slash.compact":
		return "Unterhaltungsverlauf komprimieren"
	case "slash.todo":
		return "Todo-Liste anzeigen/verwalten"
	case "slash.bug":
		return "Fehler melden"
	case "slash.config":
		return "Konfiguration anzeigen/ändern"
	case "slash.qq":
		return "QQ-Kanalbindung verwalten"
	case "slash.telegram":
		return "Telegram-Kanalbindung verwalten"
	case "slash.pc":
		return "PC-Kanalbindung verwalten"
	case "slash.discord":
		return "Discord-Kanalbindung verwalten"
	case "slash.feishu":
		return "Feishu-Kanalbindung verwalten"
	case "slash.slack":
		return "Slack-Kanalbindung verwalten"
	case "slash.dingtalk":
		return "DingTalk-Kanalbindung verwalten"
	case "slash.wechat":
		return "WeChat-Kanalbindung verwalten"
	case "slash.wecom":
		return "WeCom (Enterprise WeChat) Kanalbindung verwalten"
	case "slash.mattermost":
		return "Mattermost-Kanalbindung verwalten"
	case "slash.matrix":
		return "Matrix-Kanalbindung verwalten"
	case "slash.signal":
		return "Signal-Kanalbindung verwalten"
	case "slash.irc":
		return "IRC-Kanalbindung verwalten"
	case "slash.nostr":
		return "Nostr-Kanalbindung verwalten"
	case "slash.twitch":
		return "Twitch-Kanalbindung verwalten"
	case "slash.whatsapp":
		return "WhatsApp-Kanalbindung verwalten"
	case "slash.impersonate":
		return "CLI-Tool für Shell-Prompt-Anzeige imitieren"
	case "slash.knight":
		return "Autonomen Hintergrund-Agent verwalten"
	case "slash.stream":
		return "Streaming-Ausgabemodus konfigurieren"
	case "slash.diff":
		return "Git-Diff im Chat anzeigen (unterstützt --cached, <file>, --stat)"
	case "slash.hooks":
		return "Konfigurierte Hooks anzeigen (alle Events, Typen, Match-Muster)"
	case "slash.cost":
		return "Sitzungs-Token-Nutzung und geschätzte Kosten anzeigen"
	case "slash.review":
		return "KI-Code-Review aktueller Änderungen (Bugs, Sicherheit, Races)"
	case "slash.copy":
		return "Letzte Assistenten-Antwort in Zwischenablage kopieren"
	case "slash.context":
		return "Kontextfenster-Nutzung aufschlüsseln (Tokens, Nachrichten, Kapazität)"
	case "slash.im":
		return "Einheitliches IM-Kanal-Panel öffnen"

	// --- QQ panel ---
	case "panel.qq.directory":
		return "Verzeichnis"
	case "panel.qq.runtime":
		return "Laufzeit"
	case "panel.qq.bots":
		return "QQ-Bots"
	case "panel.qq.created":
		return "Erstellt: %d"
	case "panel.qq.bound":
		return "Gebunden: %d"
	case "panel.qq.available":
		return "Verfügbar: %d"
	case "panel.qq.current_binding":
		return "Aktuelle Bindung"
	case "panel.qq.none":
		return "(keine)"
	case "panel.qq.default":
		return "(Standard)"
	case "panel.qq.adapter":
		return "Adapter: %s"
	case "panel.qq.target":
		return "Ziel: %s"
	case "panel.qq.channel":
		return "Kanal: %s"
	case "panel.qq.bot_list":
		return "QQ-Bot-Liste"
	case "panel.qq.no_bots":
		return "Keine QQ-Bots konfiguriert."
	case "panel.qq.entry.available":
		return "Verfügbar"
	case "panel.qq.entry.bound":
		return "Gebunden"
	case "panel.qq.entry.active":
		return "Aktiv"
	case "panel.qq.entry.bound_other":
		return "Gebunden: %s"
	case "panel.qq.entry.muted":
		return "Stummgeschaltet"
	case "panel.qq.details":
		return "Details"
	case "panel.qq.status":
		return "Status: %s"
	case "panel.qq.transport":
		return "Transport: %s"
	case "panel.qq.bound_directory":
		return "Gebundenes Verzeichnis: %s"
	case "panel.qq.current_directory_target":
		return "Aktuelles Verzeichnis-Ziel: %s"
	case "panel.qq.current_directory_channel":
		return "Aktueller Verzeichnis-Kanal: %s"
	case "panel.qq.waiting_for_pairing":
		return "(warte auf Pairing)"
	case "panel.qq.last_error":
		return "Letzter Fehler: %s"
	case "panel.qq.occupied_by":
		return "Belegt von: %s"
	case "panel.qq.create":
		return "Erstellen"
	case "panel.qq.bot_input":
		return "QQ-Bot: %s"
	case "panel.qq.create_format":
		return "Format: <bot-id> <appid> <appsecret>"
	case "panel.qq.create_example":
		return "Beispiel: qq-main 123456 secret-value"
	case "panel.qq.create_hint":
		return "Enter Bot erstellen • Esc abbrechen"
	case "panel.qq.actions_hint":
		return "j/k bewegen • Enter oder b Bot binden • c Kanal binden • x Kanal lösen • u Bot lösen • i Bot erstellen • Esc schließen"
	case "panel.qq.bind_channel":
		return "Kanal binden"
	case "panel.qq.scan_hint":
		return "QR-Code scannen, Bot hinzufügen, dann eine Nachricht senden, um das Pairing zu starten."
	case "panel.qq.qr_code":
		return "QR-Code:"
	case "panel.qq.share_link":
		return "Freigabe-Link:"
	case "panel.qq.message.no_bot":
		return "Kein QQ-Bot verfügbar."
	case "panel.qq.message.bound_success":
		return "QQ-Bot an aktuelle Workspace gebunden. c verwenden, um den QR-Code für die Kanalbindung zu generieren."
	case "panel.qq.message.share_generated":
		return "QQ-Freigabe-Link generiert. QR-Code scannen, Bot hinzufügen, dann eine Nachricht senden, um das Pairing zu starten."
	case "panel.qq.message.unbound":
		return "QQ-Kanal gelöst."
	case "panel.qq.message.cleared":
		return "QQ-Kanalautorisierung für aktuelle Workspace gelöscht."
	case "panel.qq.message.added_bot":
		return "QQ-Bot %s hinzugefügt."
	case "panel.qq.error.config_unavailable":
		return "Konfiguration nicht verfügbar"
	case "panel.qq.error.config_format":
		return "QQ-Bot-Konfiguration muss sein: <bot-id> <appid> <appsecret>"
	case "panel.qq.error.adapter_required":
		return "QQ-Adaptername erforderlich"
	case "panel.qq.error.not_configured":
		return "QQ-Bot %q ist nicht konfiguriert"
	case "panel.qq.error.disabled":
		return "QQ-Bot %q ist deaktiviert"
	case "panel.qq.error.not_qq_adapter":
		return "Adapter %q ist kein QQ-Bot"
	case "panel.qq.error.not_online":
		return "QQ-Bot %q ist nicht online"
	case "panel.qq.error.not_online_detail":
		return "QQ-Bot %q ist nicht online: %s"
	case "panel.qq.runtime.available":
		return "verfügbar"
	case "panel.qq.runtime.disabled":
		return "deaktiviert (im.enabled: true setzen und ggcode neu starten)"
	case "panel.qq.runtime.not_started":
		return "nicht gestartet (ggcode neu starten, um IM-Laufzeit zu initialisieren)"
	case "panel.qq.status.not_started":
		return "nicht gestartet"
	case "panel.qq.status.unknown":
		return "unbekannt"

	// --- More slash commands ---
	case "slash.status":
		return "Aktuellen Status anzeigen"
	case "slash.update":
		return "ggcode aktualisieren"
	case "slash.cron":
		return "Geplante Cron-Jobs verwalten (list, pause, resume, create)"
	case "slash.branch":
		return "Aktuelle Sitzung abzweigen (Unterhaltung forken)"
	case "slash.chat":
		return "LAN-Chat-Panel öffnen"
	case "slash.edit":
		return "Letzte Nachricht bearbeiten und erneut senden"
	case "slash.inspector":
		return "Inspector-Panel umschalten"
	case "slash.mode":
		return "Berechtigungsmodus anzeigen oder wechseln"
	case "slash.nick":
		return "LAN-Chat-Spitznamen setzen"
	case "slash.reflect":
		return "Selbst-Reflexion der aktuellen Sitzung ausführen"
	case "slash.regenerate":
		return "Letzte KI-Antwort neu generieren (verwerfen und erneut ausführen)"
	case "slash.restart":
		return "ggcode-Prozess neu starten"
	case "slash.retry":
		return "Letzten fehlgeschlagenen Agent-Lauf wiederholen"
	case "slash.rules":
		return "Gelernte Ratchet-Regeln anzeigen"
	case "slash.share":
		return "Sitzung über Tunnel teilen (Mobile-Relay)"
	case "slash.stats":
		return "Sitzungsstatistiken anzeigen (Tokens, Iterationen, Werkzeuge)"
	case "slash.tmux":
		return "tmux-Pane-Verwaltungsmenü öffnen"
	case "slash.tunnel":
		return "Tunnelverbindung für Mobile-Relay umschalten"
	case "slash.unshare":
		return "Sitzungsfreigabe über Tunnel stoppen"

	// --- Regenerate ---
	case "regenerate.busy":
		return "Neugenerierung nicht möglich, während der Agent läuft. Zuerst Ctrl+C drücken zum Abbrechen."
	case "regenerate.no_agent":
		return "Agent nicht initialisiert."
	case "regenerate.no_context":
		return "Kontext-Manager nicht verfügbar."
	case "regenerate.no_response":
		return "Keine Assistenten-Antwort zum Neu generieren."

	// --- Branch ---
	case "branch.busy":
		return "Abzweigen nicht möglich, während der Agent läuft. Zuerst Ctrl+C drücken zum Abbrechen."
	case "branch.no_session":
		return "Keine aktive Sitzung zum Abzweigen."
	case "branch.empty":
		return "Sitzung hat keine Nachrichten zum Abzweigen."
	case "branch.save_failed":
		return "Abgezweigte Sitzung konnte nicht erstellt werden: %v"
	case "branch.success":
		return "Abgezweigt zu neuer Sitzung %s (von: %s). Ursprüngliche Sitzung bleibt erhalten."

	// --- Help text ---
	case "help.text":
		return `Verfügbare Befehle:

Sitzung & Verlauf:
  /help, /?          Diese Hilfemeldung anzeigen
  /sessions          Alle gespeicherten Sitzungen auflisten
  /resume <id>       Vorherige Sitzung fortsetzen
  /export <id>       Sitzung als Markdown-Datei exportieren
  /clear             Unterhaltungsverlauf löschen
  /compact           Unterhaltungsverlauf komprimieren (manuell)
  /undo              Letzte Dateibearbeitung rückgängig (Checkpoint-Rollback)
  /checkpoints       Alle Dateibearbeitungs-Checkpoints auflisten
  /regenerate        Letzte Antwort verwerfen und neu generieren (Alias: /regen)
  /branch            Aktuelle Unterhaltung in neue Sitzung forken (Alias: /fork)

Modell & Anbieter:
  /model [name]      Modell-Panel öffnen oder direkt wechseln
  /provider [vendor] Anbieter-Manager öffnen
  /mode <mode>       Agent-Modus setzen (supervised|plan|auto|bypass|autopilot)

Entwicklung:
  /diff [opts]       Git-Diff im Chat anzeigen (--cached, --stat, <file>)
  /review [opts]     KI-Code-Review aktueller Änderungen (--cached, --staged)
  /copy              Letzte Assistenten-Antwort in Zwischenablage kopieren
  /cost              Sitzungs-Token-Nutzung und geschätzte Kosten anzeigen
  /context           Kontextfenster-Nutzung aufschlüsseln
  /hooks             Konfigurierte Hooks anzeigen
  /init              GGCODE.md aus aktuellem Projekt generieren
  /harness ...       Harness-Steuerungsebene-Befehle ausführen
  /todo              Todo-Liste anzeigen
  /todo clear        Todo-Liste löschen

Integrationen:
  /im                Einheitliches IM-Kanal-Panel öffnen
  /mcp               Verbundene MCP-Server und Werkzeuge anzeigen
  /plugins           Geladene Plugins und deren Werkzeuge auflisten
  /skills            Verfügbare Fähigkeiten durchsuchen
  /memory            Geladene Memory-Dateien anzeigen
  /agents            Sub-Agenten auflisten
  /cron <sub>        Geplante Jobs verwalten (list|get|pause|resume|pauseall|resumeall)

System:
  /lang [code]       Oberflächensprache wählen oder wechseln
  /config            Aktuelle Konfiguration anzeigen
  /config set <k> <v> Konfigurationswert setzen
  /status            Aktuellen Status anzeigen
  /update            ggcode auf neueste Version aktualisieren
  /restart           ggcode neu starten (aktuellsten Binary laden)
  /bug               Fehler mit Diagnose melden
  /exit, /quit       ggcode beenden

Tastenkürzel:
  Tab                Autovervollständigung oder Genehmigungsoptionen durchschalten
  Shift+Tab          Rückwärts durchschalten, sonst Berechtigungsmodus umschalten
  Ctrl+R             Seitenleiste umschalten
  Ctrl+N/P           Neue/vorherige Sitzung
  Ctrl+T             Tunnel öffnen (Mobile-Freigabe)
  Enter              Nachricht senden / aktuelle Auswahl anwenden
  Esc                Autovervollständigung abbrechen / Idle-Shell-Modus verlassen
  Up/Down            Befehlsverlauf durchsuchen (oder Autovervollständigung)
  PgUp/PgDn          Unterhaltungsausgabe scrollen
  Ctrl+C             Aktuelle Aktivität abbrechen, sonst Eingabe löschen, dann erneut drücken zum Beenden
  Ctrl+D             Sofort beenden
  Ctrl+A / Ctrl+E    Cursor an Zeilenanfang / -ende bewegen
  Ctrl+K             Vom Cursor bis Zeilenende löschen
  Ctrl+U             Vom Zeilenanfang bis Cursor löschen
  Ctrl+W             Wort vor dem Cursor löschen
  Ctrl+Backspace     Zuletzt angehängtes Bild entfernen
  Shift+Enter        Zeilenumbruch einfügen (Ctrl+J oder Alt+Enter in tmux)
  $ / !              Shell-Modus aktivieren
  #                  LAN-Chat-Schnellsende-Modus aktivieren

Maus:
  Option+Ziehen / Shift+Ziehen  Text zum Kopieren auswählen (umgeht App-Maus-Erfassung)
  Mausrad                      Unterhaltungsausgabe scrollen`

	// --- Harness commands ---
	case "command.harness_usage":
		return "Verwendung: /harness <init|check|queue|tasks|run|rerun|run-queued|monitor|contexts|inbox|review|promote|release|gc|doctor> ... (release unterstützt rollouts|advance|pause|resume|abort|approve|reject)"
	case "command.harness_queue_usage":
		return "Verwendung: /harness queue <ziel>"
	case "command.harness_run_usage":
		return "Verwendung: /harness run <ziel>"
	case "command.harness_rerun_usage":
		return "Verwendung: /harness rerun <task-id>"
	case "command.skill_agent_only":
		return "Fähigkeit %s kann nur vom Agent aufgerufen werden."
	case "command.harness_owner_promoted":
		return "%d Harness-Aufgabe(n) für Besitzer %s übernommen."
	case "command.harness_review_approved":
		return "Harness-Aufgabe %s genehmigt."
	case "command.harness_review_rejected":
		return "Harness-Aufgabe %s abgelehnt."
	case "command.harness_promoted_many":
		return "%d Harness-Aufgabe(n) übernommen."
	case "command.harness_promoted_one":
		return "Harness-Aufgabe %s übernommen."
	case "command.harness_task_queued_detail":
		return "Harness-Aufgabe %s eingereiht.\n- Ziel: %s"
	case "command.harness_tasks_empty":
		return "Keine Harness-Aufgaben aufgezeichnet."
	case "command.harness_run_start":
		return "Nachverfolgter Harness-Lauf wird gestartet...\n/harness monitor oder die Aufgaben-/Monitor-Ansichten für Live-Status verwenden."
	case "command.harness_rerun_start":
		return "Nachverfolgte Harness-Wiederholung wird gestartet...\n/harness monitor oder die Aufgaben-/Monitor-Ansichten für Live-Status verwenden."
	case "command.harness_rerun_invalid_status":
		return "Harness-Aufgabe %s ist %s; nur fehlgeschlagene Aufgaben können wiederholt werden."
	case "command.harness_status_starting_run":
		return "Harness-Lauf wird gestartet..."
	case "command.harness_status_starting_rerun":
		return "Harness-Wiederholung wird gestartet..."
	case "command.harness_spinner_running":
		return "Harness wird ausgeführt"
	case "command.harness_cancelled":
		return "Harness-Lauf abgebrochen."

	// --- Tunnel ---
	case "tunnel.stopped":
		return "Tunnel gestoppt."
	case "tunnel.not_active":
		return "Keine aktive Freigabe-Sitzung."
	case "tunnel.mobile_connected":
		return "Mobile-Client verbunden."

	// --- Config save scope ---
	case "config.save_scope_global":
		return "Speicherziel → Global"
	case "config.save_scope_instance":
		return "Speicherziel → Instanz"
	case "config.save_scope_instance_new":
		return "Speicherziel → Instanz (neue Konfiguration wird erstellt)"
	case "config.instance_unavailable":
		return "Instanz-Konfiguration für diese Workspace nicht verfügbar"
	case "config.scope_instance":
		return "Instanz"
	case "config.scope_global":
		return "Global"
	case "config.save_target_new_hint":
		return " (neue Konfiguration wird beim Speichern erstellt)"
	case "config.save_target_line":
		return " Speicherziel: %s%s  [Ctrl+T umschalten]"

	// --- Shell ---
	case "shell.empty":
		return "Shell-Befehl ist leer."

	// --- LAN Chat ---
	case "lanchat.unavailable":
		return "LAN-Chat ist nicht verfügbar."

	// --- Reflect ---
	case "reflect.no_agent":
		return "Agent nicht initialisiert."
	case "reflect.no_workdir":
		return "Arbeitsverzeichnis nicht gesetzt."
	case "reflect.no_memory":
		return "Projekt-Memory für dieses Verzeichnis nicht verfügbar."
	case "reflect.load_failed":
		return "Einsichten konnten nicht geladen werden: %v"
	case "reflect.empty":
		return "Noch keine Lauf-Einsichten. Einsichten werden nach jedem Agent-Lauf mit 3+ Werkzeugaufrufen oder Dateibearbeitungen automatisch generiert."
	case "reflect.title":
		return "## Gesammelte Lauf-Einsichten\n\n"
	case "reflect.memory_location":
		return "Memory-Speicherort: %s\n"

	// --- Knight ---
	case "knight.unavailable":
		return "Knight ist nicht verfügbar"

	// --- Pairing ---
	case "pairing.rejected":
		return "Die aktuelle Pairing-Anfrage wurde abgelehnt. Bitte neu initiieren, um fortzufahren."
	case "pairing.blacklisted":
		return "Dieser Kanal wurde aufgrund mehrfacher Ablehnungen auf die Blacklist gesetzt."

	default:
		return enCatalog(key)
	}
}
