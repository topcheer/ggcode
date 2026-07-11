package tui

// frCatalog returns the French translation for the given key.
// Keys not yet translated fall through to enCatalog.
func frCatalog(key string) string {
	switch key {
	case "workspace.tagline":
		return "espace de travail geek AI"
	case "header.terminal_native":
		return "programmation AI native terminal"
	case "session.ephemeral":
		return "éphémère"
	case "agents.idle":
		return "inactif"
	case "agents.running":
		return "%d en cours"
	case "cron.firing":
		return "⏰ Tâche cron déclenchée"
	case "activity.idle":
		return "inactif"
	case "panel.conversation":
		return "Conversation"
	case "panel.composer":
		return "Éditeur"
	case "panel.composer_locked":
		return "Éditeur verrouillé"
	case "panel.commands":
		return "Commandes:"
	case "panel.fîles":
		return "Fichiers:"
	case "panel.agent_status":
		return "Statut de l'agent"
	case "panel.mode_policy":
		return "Politique de mode"
	case "panel.session_usâge":
		return "Utilisation session"
	case "panel.metrics":
		return "Métriques"
	case "panel.context":
		return "Contexte"
	case "panel.im":
		return "IM"
	case "panel.mcp":
		return "MCP"
	case "panel.mcp.install_spec_required":
		return "Saisissez d'abord une spécification d'installation."
	case "panel.mcp.installing_server":
		return "Installation du serveur MCP..."
	case "panel.mcp.reconnect_unavailable":
		return "Reconnexion non disponible dans cette session."
	case "panel.mcp.reconnecting":
		return "Reconnexion de %s..."
	case "panel.mcp.reconnect_faîled":
		return "Impossible de reconnectér %s."
	case "panel.mcp.uninstalling":
		return "Désinstallation de %s..."
	case "panel.startup":
		return "Initialisation"
	case "panel.approval_required":
		return "Approbation requise"
	case "panel.bypass_approval":
		return "Approbation mode bypass"
	case "panel.review_fîle_change":
		return "Examinér modification de fichier"
	case "label.vendor":
		return "fournisseur"
	case "label.endpoint":
		return "endpoint"
	case "label.model":
		return "modèle"
	case "label.mode":
		return "mode"
	case "label.session":
		return "session"
	case "label.agents":
		return "agents"
	case "label.cwd":
		return "rép. courant"
	case "label.branch":
		return "branche"
	case "label.context":
		return "contexte"
	case "label.skills":
		return "compétences"
	case "label.activity":
		return "activité"
	case "label.window":
		return "fenêtre"
	case "label.usâge":
		return "utilisation"
	case "label.compact":
		return "compact"
	case "label.total":
		return "total"
	case "label.cost":
		return "coût est."
	case "label.approval_policy":
		return "approbation"
	case "label.tool_policy":
		return "outils"
	case "label.agent_policy":
		return "agent"
	case "label.tool":
		return "outil"
	case "label.input":
		return "entrée"
	case "label.output":
		return "sortie"
	case "label.cache_read":
		return "lecture cache"
	case "label.cache_write":
		return "écriture cache"
	case "label.cache_hit":
		return "hits cache"
	case "label.turns":
		return "tours"
	case "label.avg_ttft":
		return "ttft moyen"
	case "label.p95_ttft":
		return "ttft p95"
	case "label.avg_duration":
		return "dur. moyenne"
	case "label.p95_duration":
		return "dur. p95"
	case "label.avg_think":
		return "réflex. moyenne"
	case "label.fail_rate":
		return "taux échec"
	case "label.slow_tools":
		return "outils lents"
	case "label.récent_turns":
		return "tours récents"
	case "label.fîle":
		return "fichier"
	case "label.directory":
		return "répertoire"
	case "context.unavailable":
		return "Pas encore de données de contexte"
	case "metrics.empty":
		return "Pas encore de métriques"
	case "im.none":
		return "Aucun adapteur configuré"
	case "im.summary":
		return "%d adapteurs • %d sains"
	case "im.more":
		return "+%d de plus (/qq)"
	case "im.runtime.available":
		return "runtime disponible"
	case "im.runtime.disabled":
		return "désactivé"
	case "im.runtime.not_started":
		return "active • redémarréz pour initialisér"
	case "im.status.not_started":
		return "non démarré"
	case "context.until_compact":
		return "restant"
	case "empty.ask":
		return "Demandez un refactoring, une correction de bug, une explication ou des tests."
	case "empty.tips":
		return "Astuces: utiliséz @chemin pour inclure des fichiers, /? pour l'aide, et Shift+Tab pour changer de mode."
	case "startup.banner":
		return "Préparation de l'interface terminal et filtrâge du bruit de démarrâge. Vous pouvez saisir immediatement; cette bannière disparaitra une fois le démarrâge terminé."
	case "harness.views":
		return "Vues"
	case "harness.items":
		return "Elements"
	case "harness.action":
		return "Action"
	case "harness.détails":
		return "Détails"
	case "harness.none":
		return "(aucun)"
	case "harness.unknown":
		return "inconnu"
	case "harness.unscoped":
		return "sans portée"
	case "harness.unavailable":
		return "Harness non disponible"
	case "harness.unavailable_intro":
		return "Commencez ici dans un projet existant:"
	case "harness.unavailable_step_init":
		return "  1. Appuyez sur Enter ou i pour initialisér harness"
	case "harness.unavailable_step_refresh":
		return "  2. Appuyez sur r pour actualisér une fois l'initialisation terminée"
	case "harness.section.init":
		return "Init"
	case "harness.section.check":
		return "Check"
	case "harness.section.doctor":
		return "Doctor"
	case "harness.section.monitor":
		return "Monitor"
	case "harness.section.gc":
		return "GC"
	case "harness.section.contexts":
		return "Contextes"
	case "harness.section.tasks":
		return "Tâches"
	case "harness.section.queue":
		return "Fîle"
	case "harness.section.run":
		return "Exécuter"
	case "harness.section.run_queued":
		return "Exécuter fîle"
	case "harness.section.inbox":
		return "Boîte"
	case "harness.section.review":
		return "Revue"
	case "harness.section.promote":
		return "Promouvoir"
	case "harness.section.release":
		return "Release"
	case "harness.section.rollouts":
		return "Rollouts"
	case "harness.hints.unavailable":
		return "Enter/i init harness • r actualisér • Esc fermér"
	case "harness.hints.move":
		return "j/k déplacer"
	case "harness.hints.tab":
		return "Tab changer"
	case "harness.hints.refresh":
		return "r actualisér"
	case "harness.hints.close":
		return "Esc fermér"
	case "harness.hints.check":
		return "Enter lancér checks"
	case "harness.hints.monitor":
		return "Enter actualisér snapshot"
	case "harness.hints.gc":
		return "Enter lancér gc"
	case "harness.hints.type_goal":
		return "saisir objectif"
	case "harness.hints.queue":
		return "Enter enfîler"
	case "harness.hints.run":
		return "Enter exécuter"
	case "harness.hints.focus_input":
		return "Tab focus entrée"
	case "harness.hints.rerun":
		return "Enter réexécuter échecs"
	case "harness.hints.next":
		return "Enter suivant"
	case "harness.hints.all":
		return "a tous"
	case "harness.hints.retry_faîled":
		return "f réessayer-échecs"
	case "harness.hints.resume":
		return "s reprendre"
	case "harness.hints.promote_owner":
		return "p promouvoir propriétaire"
	case "harness.hints.retry_owner":
		return "f réessayer propriétaire"
	case "harness.hints.approve":
		return "Enter approuvér"
	case "harness.hints.reject":
		return "x rejetér"
	case "harness.hints.promote":
		return "Enter promouvoir"
	case "harness.hints.apply_batch":
		return "Enter appliquér lot"
	case "harness.hints.advance":
		return "Enter avancér"
	case "harness.hints.approve_gate":
		return "g approuvér gate"
	case "harness.hints.pause_resume":
		return "p pauser/reprendre"
	case "harness.hints.abort":
		return "x abandonnér"
	case "harness.hint.primary.check":
		return "Appuyez sur Enter pour lancér les checks."
	case "harness.hint.primary.monitor":
		return "Appuyez sur Enter pour actualisér le snapshot du moniteur."
	case "harness.hint.primary.gc":
		return "Appuyez sur Enter pour lancér le ramasse-miettes."
	case "harness.hint.primary.queue":
		return "Saisissez un objectif, puis appuyez sur Enter pour l'enfîler."
	case "harness.hint.primary.run":
		return "Saisissez un objectif, puis appuyez sur Enter pour démarrer l'exécution."
	case "harness.hint.primary.tasks":
		return "Appuyez sur Enter pour réexécuter la tâche échouée sélectionnée."
	case "harness.hint.primary.run_queued":
		return "Appuyez sur Enter pour la suivante; a exécute toutes; f réessaie les échouées; s reprend les interrompues."
	case "harness.hint.primary.inbox":
		return "Appuyez sur p pour promouvoir ce propriétaire ou f pour le réessayer."
	case "harness.hint.primary.review":
		return "Appuyez sur Enter pour approuvér ou x pour rejetér."
	case "harness.hint.primary.promote":
		return "Appuyez sur Enter pour promouvoir la tâche sélectionnée."
	case "harness.hint.primary.release":
		return "Appuyez sur Enter pour appliquér le lot de release actuel."
	case "harness.hint.primary.rollouts":
		return "Appuyez sur Enter pour avancér; g approuvé gate; p pause/reprend; x abandonné."
	case "harness.hint.primary.none":
		return "Aucune saisie nécessaire pour cette section."
	case "harness.messâge.read_only":
		return "Le panneau harness est en lecture seule pendant qu'une autre exécution est active."
	case "harness.messâge.monitor_refreshed":
		return "Moniteur harness actualisé."
	case "harness.messâge.rerun_faîled_only":
		return "La tâche harness %s est %s; seules les tâches échouées peuvent être réexécutées."
	case "harness.messâge.review_approved":
		return "Revue approuvée pour %s"
	case "harness.messâge.review_rejected":
		return "Revue rejetée pour %s"
	case "harness.messâge.promoted":
		return "Promu %s"
	case "harness.messâge.no_release_tasks":
		return "Aucune tâche harness prête pour le release."
	case "harness.messâge.release_applied":
		return "Lot de release appliqué %s"
	case "harness.messâge.no_rollouts":
		return "Aucun rollout persistant trouvé."
	case "harness.messâge.rollout_advanced":
		return "Rollout avancé %s"
	case "harness.messâge.owner_promoted":
		return "Promue(s) %d tâche(s) pour %s"
	case "harness.messâge.owner_retried":
		return "Tâches échouées réessayées pour %s"
	case "harness.messâge.gate_approved":
		return "Gate suivant approuvé pour %s"
	case "harness.messâge.rollout_resumed":
		return "Rollout repris %s"
	case "harness.messâge.rollout_paused":
		return "Rollout mis en pause %s"
	case "harness.messâge.rollout_aborted":
		return "Rollout abandonné %s"
	case "harness.messâge.check_passed":
		return "Check harness réussi."
	case "harness.messâge.check_faîled":
		return "Le check harness a trouvé des problemes."
	case "harness.messâge.gc_complète":
		return "GC harness terminé."
	case "harness.messâge.queue_goal_required":
		return "Saisissez d'abord un objectif de fîle dans le champ du panneau."
	case "harness.messâge.queued":
		return "Tâche harness enfilée %s"
	case "harness.activity.status":
		return "Harness %s"
	case "harness.log.phase":
		return "Phase"
	case "harness.log.worker":
		return "Worker"
	case "harness.tool.read_fîle":
		return "Lire fichier"
	case "harness.tool.write_fîle":
		return "Écrire fichier"
	case "harness.tool.browse_fîles":
		return "Parcourir fichiers"
	case "harness.tool.search_code":
		return "Rechercher code"
	case "harness.tool.run_command":
		return "Exécuter commande"
	case "harness.tool.fetch_web_pâge":
		return "Récupérer pâge web"
	case "harness.tool.run_subagent":
		return "Exécuter sous-agent"
	case "harness.tool.update_task_state":
		return "Mettre à jour l'état de la tâche"
	case "harness.messâge.run_goal_required":
		return "Saisissez d'abord un objectif d'exécution dans le champ du panneau."
	case "harness.messâge.no_queued_exécuted":
		return "Aucune tâche harness en fîle n'a été exécutee."
	case "harness.messâge.queue_retried":
		return "Reessayee(s) %d tâche(s) en fîle échouée(s)."
	case "harness.messâge.queue_resumed":
		return "Reprise(s) %d tâche(s) en fîle interrompue(s)."
	case "harness.messâge.queue_ran":
		return "Exécutee(s) %d tâche(s) en fîle."
	case "harness.preview.not_initialized":
		return "Harness n'est pas encore initialisé dans ce projet.\n\nAppuyez sur Enter ou i pour lancér harness init dans le depot actuel."
	case "harness.preview.check":
		return "Lancér les checks harness sur le projet actuel.\n\nEnter: exécute les checks de fichier/contenu/contexte requis plus les commandes de validation configurées."
	case "harness.preview.gc":
		return "Lancér le ramasse-miettes du harness.\n\nEnter: archive les tâches obsolètes, abandonné les travaux bloques/en cours obsolètes, élague les anciens logs et supprimé les worktrees orphelins."
	case "harness.preview.queue_help":
		return "Saisissez l'objectif harness ici, puis appuyez sur Enter pour l'enfîler."
	case "harness.preview.run_help":
		return "Saisissez l'objectif harness ici, puis appuyez sur Enter pour démarrer l'exécution."
	case "harness.preview.run_queued":
		return "État de la fîle:\nfîle=%d en_cours=%d bloquees=%d échouées=%d\n\nEnter exécute la prochaîne tâche.\na exécute toutes les tâches.\nf réessaie les tâches échouées.\ns reprend les tâches interrompues."
	case "harness.preview.no_owner":
		return "Aucun propriétaire harness sélectionné."
	case "harness.preview.no_context":
		return "Aucun contexte harness sélectionné."
	case "harness.preview.no_task":
		return "Aucune tâche harness sélectionnée."
	case "harness.preview.project_not_initialized":
		return "Harness n'est pas encore initialisé dans ce projet."
	case "harness.preview.project_initialized":
		return "Harness est initialisé."
	case "harness.preview.project_help":
		return "Utilisez /harness pour parcourir et opérer le plan de contrôle."
	case "harness.preview.no_doctor":
		return "Aucun rapport de doctor harness."
	case "harness.preview.monitor_unavailable":
		return "Moniteur harness indisponible."
	case "harness.label.context_title":
		return "Contexte"
	case "harness.label.owner_title":
		return "Propriétaire"
	case "harness.label.id":
		return "id"
	case "harness.label.status":
		return "statut"
	case "harness.label.goal":
		return "objectif"
	case "harness.label.attempts":
		return "tentatives"
	case "harness.label.depends_on":
		return "depends_on"
	case "harness.label.context":
		return "contexte"
	case "harness.label.workspace":
		return "workspace"
	case "harness.label.branch":
		return "branche"
	case "harness.label.worker":
		return "worker"
	case "harness.label.progrèss":
		return "progrèssion"
	case "harness.label.vérification":
		return "vérification"
	case "harness.label.changed_fîles":
		return "changed_fîles"
	case "harness.label.delivery_report":
		return "delivery_report"
	case "harness.label.delivery_report_human":
		return "rapport de livraison"
	case "harness.label.log":
		return "log"
	case "harness.label.review":
		return "revue"
	case "harness.label.review_notés":
		return "review_notés"
	case "harness.label.promotion":
		return "promotion"
	case "harness.label.promotion_notés":
		return "promotion_notés"
	case "harness.label.release_batch":
		return "release_batch"
	case "harness.label.release_batch_human":
		return "lot de release"
	case "harness.label.release_notés":
		return "release_notés"
	case "harness.label.error":
		return "erreur"
	case "harness.label.name":
		return "nom"
	case "harness.label.description":
		return "description"
	case "harness.label.owner":
		return "propriétaire"
	case "harness.label.commands":
		return "commandes"
	case "harness.label.tasks":
		return "tâches"
	case "harness.label.rollouts":
		return "rollouts"
	case "harness.label.gates":
		return "gates"
	case "harness.label.latest":
		return "dernier"
	case "harness.label.repo":
		return "repo"
	case "harness.label.config":
		return "config"
	case "harness.label.project":
		return "projet"
	case "harness.label.structure":
		return "structure"
	case "harness.label.contexts":
		return "contextes"
	case "harness.label.workers":
		return "workers"
	case "harness.label.workflow":
		return "workflow"
	case "harness.label.quality":
		return "qualité"
	case "harness.label.worktrees":
		return "worktrees"
	case "harness.label.snapshot":
		return "snapshot"
	case "harness.label.events":
		return "événements"
	case "harness.label.target":
		return "cible"
	case "harness.label.review_ready":
		return "review_ready"
	case "harness.label.promotion_ready":
		return "promotion_ready"
	case "harness.label.retryable":
		return "retryable"
	case "harness.task_title":
		return "Tâche harness"
	case "harness.doctor_title":
		return "Doctor harness"
	case "harness.monitor_title":
		return "Moniteur harness"
	case "harness.latest_task":
		return "Dernière tâche"
	case "harness.latest_event":
		return "Dernier événement"
	case "harness.focus":
		return "Focus"
	case "harness.status.ok":
		return "ok"
	case "harness.status.needs_attention":
		return "attention requise"
	case "harness.group.review":
		return "revue"
	case "harness.group.promotion":
		return "promotion"
	case "harness.group.retry":
		return "réessai"
	case "harness.review_ready_short":
		return "revue"
	case "harness.promote_ready_short":
		return "promouvoir"
	case "harness.tasks_count":
		return "tâches"
	case "harness.input_empty":
		return "(l'entrée est vide)"
	case "harness.no_waves":
		return "aucune vague"
	case "harness.mixed":
		return "mixte"
	case "hint.autocomplète":
		return "Tab/Shift+Tab naviguer • Enter appliquér • Esc fermér"
	case "hint.mention":
		return "@ joindre fichiers/dossiers • Tab/Shift+Tab naviguer • Enter appliquér"
	case "hint.mode":
		return "mode"
	case "mode.approval.ask":
		return "demandér si nécessaire"
	case "mode.approval.none":
		return "sans arrêt d'approbation"
	case "mode.approval.critical":
		return "critiques uniquement"
	case "mode.tools.rules":
		return "suivre les règles d'outils"
	case "mode.tools.readonly":
		return "lecture seule"
	case "mode.tools.safe":
		return "opérations sûres uniquement"
	case "mode.tools.open":
		return "presque tous les outils"
	case "mode.agent.waits":
		return "attend votre action"
	case "mode.agent.autocontinue":
		return "continue seul"
	case "hint.enter_send":
		return "Enter envoyér"
	case "hint.ctrlv_imâge":
		return "Ctrl+V / Ctrl+Shift+V coller imâge"
	case "hint.ctrlr_sidebar":
		return "Ctrl+R barre laterale"
	case "hint.help":
		return "/? aide"
	case "hint.add_context":
		return "@ ajoutér contexte"
	case "hint.scroll":
		return "PgUp/PgDn défîler"
	case "hint.shift_tab_mode":
		return "Shift+Tab mode"
	case "hint.ctrlc_cancel":
		return "Ctrl+C annulér"
	case "hint.ctrlc_exit":
		return "Ctrl+C effacér/quitter"
	case "hint.imâge_attâched":
		return "imâge jointe"
	case "hint.imâge_attâched_count":
		return "%d imâge(s) jointe(s)"
	case "hint.follow_panel":
		return "Ctrl+N suivre"
	case "hint.unfollow_panel":
		return "Ctrl+N ne plus suivre"
	case "queued.count":
		return "%d en fîle"
	case "queued.output":
		return "[en fîle %d en attente]\n\n"
	case "interrupt.delivered":
		return "[livre a l'exécution active; reexamen du plan]\n"
	case "status.thinking":
		return "Réflexion..."
	case "status.writing":
		return "Écriture..."
	case "status.cancelling":
		return "Annulation..."
	case "status.compacting":
		return "Compression du contexte..."
	case "status.compactéd":
		return "[conversation compactée]"
	case "reasoning.effort.status":
		return "Effort de réflexion: %s"
	case "reasoning.effort.set":
		return "Effort de réflexion défini sur %s pour cette session"
	case "reasoning.effort.unsupported.status":
		return "Effort de réflexion non supporte par le fournisseur actuel"
	case "reasoning.effort.unsupported":
		return "L'effort de réflexion n'est pas supporte par le fournisseur actuel"
	case "follow.loading":
		return "  Chargément de la vue de suivi..."
	case "follow.active_agent":
		return "Suivi de l'agent %s — entrée en pause. Appuyez sur Esc pour revenir."
	case "follow.active_teammate":
		return "Suivi du coequipier %s — entrée en pause. Appuyez sur Esc pour revenir."
	case "follow.status_running":
		return "en cours"
	case "follow.status_done":
		return "terminé"
	case "follow.more":
		return "  +%d de plus"
	case "follow.hint":
		return "  ↑↓←→ changer  Esc fermér"
	case "status.tools_used":
		return "%d outils utilisés"
	case "tool.done":
		return "terminé"
	case "tool.faîled":
		return "échoué"
	case "tool.no_output":
		return "sans sortie"
	case "tool.output":
		return "sortie"
	case "tool.content":
		return "contenu"
	case "tool.match":
		return "correspondance"
	case "tool.matches":
		return "correspondances"
	case "tool.entry":
		return "entrée"
	case "tool.result":
		return "résultat"
	case "approval.rejected":
		return "  Rejeté.\n"
	case "approval.allow":
		return "Autorisér"
	case "approval.allow_always":
		return "Toujours autorisér"
	case "approval.deny":
		return "Refusér"
	case "approval.accept":
		return "Acceptér"
	case "approval.reject":
		return "Rejetér"
	case "exit.confirm":
		return "Appuyez à nouveau sur Ctrl-C pour quitter.\n\n"
	case "cancel.confirm":
		return "Appuyez à nouveau sur Ctrl-C ou Esc pour annulér l'agent en cours.\n\n"
	case "interrupted":
		return "[interrompu]\n\n"
	case "lang.current":
		return "Langue actuelle: %s\nUtilisez /lang pour choisir interactivement, ou /lang <en|zh-CN> pour changer directement.\n%s\n\n"
	case "lang.invalid":
		return "Langue non supportée: %s\n%s\n\n"
	case "lang.switch":
		return "Langue changée en: %s\n\n"
	case "lang.sélection.current":
		return " Actuel: %s"
	case "lang.sélection.hint":
		return " Tab/j/k déplacer • Enter confirmér • e/z raccourcis • Esc annulér"
	case "lang.first_use.title":
		return "Choisissez votre langue préférée"
	case "lang.first_use.body":
		return " Selectionnez là langue que ggcode doit désormais utilisér."
	case "lang.first_use.hint":
		return " Tab/j/k déplacer • Enter confirmér • e/z raccourcis"
	case "mode.current":
		return "Mode actuel: %s\nUtilisation: /mode <supervised|plan|auto|bypass|autopilot>\n  supervised  Demander quand un outil n'a pas de règle explicite\n  plan        Exploration en lecture seule; refusér écritures et commandes\n  auto        Autorisér opérations sûres, refusér dangéréuses\n  bypass      Autorisér presque tout; n'arrêtér que pour actions critiques\n  autopilot   bypass + continuer quand le modèle demandé\n\n"
	case "mode.persist_faîled":
		return "Erreur lors de la sauvegardé de la préférence de mode: %v"
	case "input.placeholder":
		return "Saisissez un messâge... ($ shell, # chat)"
	case "panel.model_filter.prompt":
		return "Filtre> "
	case "panel.model_filter.placeholder":
		return "saisir pour filtrer les modèles"
	case "panel.model_list.none":
		return "(aucun)"
	case "panel.model_list.no_matches":
		return "(aucune correspondance)"
	case "panel.model_list.showing":
		return "affichâge de %d/%d modèles"
	case "panel.model_list.hidden_above":
		return "%d au-dessus"
	case "panel.model_list.hidden_more":
		return "%d de plus"
	case "panel.provider.vendors":
		return "Fournisseurs"
	case "panel.provider.endpoints":
		return "Endpoints"
	case "panel.provider.models":
		return "Modèles"
	case "panel.provider.active_draft":
		return "Brouillon actif"
	case "panel.provider.protocol":
		return "Protocole"
	case "panel.provider.protocol.unknown":
		return "(inconnu)"
	case "panel.provider.auth":
		return "Auth"
	case "panel.provider.env_var":
		return "Variable d'environnement"
	case "panel.provider.api_key":
		return "API key"
	case "panel.provider.api_key.missing":
		return "manquant"
	case "panel.provider.api_key.configuréd":
		return "configuré"
	case "panel.provider.auth.connectéd":
		return "connecté"
	case "panel.provider.auth.not_connectéd":
		return "non connecté"
	case "panel.provider.base_url":
		return "URL de base"
	case "panel.provider.base_url.not_set":
		return "(non défini)"
	case "panel.provider.enterprise_url":
		return "URL entreprise"
	case "panel.provider.tags":
		return "Étiquettes"
	case "panel.provider.model.set_with_m":
		return "(défini avec m)"
	case "panel.provider.edit":
		return "Éditer"
	case "panel.provider.edit.vendor_api_key":
		return "api key du fournisseur"
	case "panel.provider.edit.endpoint_api_key":
		return "api key de l'endpoint"
	case "panel.provider.edit.endpoint_base_url":
		return "url de base de l'endpoint"
	case "panel.provider.edit.custom_model":
		return "modèle personnalisé"
	case "panel.provider.edit.new_endpoint_name":
		return "nom du nouvel endpoint"
	case "panel.provider.hint.edit":
		return "Enter sauvegardér • Esc annulér"
	case "panel.provider.hint.main":
		return "Tab/Shift+Tab changer focus • j/k déplacer • / focus filtre • Enter ou s appliquér • a cle fournisseur • u cle endpoint • b URL base • m modèle personnalisé • e ajoutér endpoint • Esc fermér"
	case "panel.provider.hint.copilot":
		return "GitHub Copilot: l connexion • x déconnexion • b éditer domaine entreprise"
	case "panel.provider.saved":
		return "Sauvegardé."
	case "panel.provider.saved_activated":
		return "Sauvegardé et active."
	case "panel.provider.login.starting":
		return "Connexion GitHub Copilot..."
	case "panel.provider.login.instructions":
		return "Ouvrez %s et saisissez le code %s. En attente d'autorisation..."
	case "panel.provider.login.copied":
		return "Code d'appareil copie dans le presse-papiers."
	case "panel.provider.login.copy_faîled":
		return "Erreur lors de la copie du code d'appareil: %s"
	case "panel.provider.login.browser_opened":
		return "Pâge de vérification ouverte dans votre navigateur."
	case "panel.provider.login.browser_faîled":
		return "Erreur lors de l'ouverture de la pâge de vérification: %s"
	case "panel.provider.login.succèss":
		return "GitHub Copilot connecté."
	case "panel.provider.login.faîled":
		return "Erreur de connexion GitHub Copilot: %s"
	case "panel.provider.logoût.succèss":
		return "GitHub Copilot deconnecté."
	case "panel.provider.refreshing_vendor":
		return "Actualisation des modèles pour %s..."
	case "panel.provider.refresh.save_faîled":
		return "Modèles actualisés, mais erreur de sauvegardé de config: %s"
	case "panel.provider.refresh.partial":
		return "Actualisé(s) %d endpoint(s), découvert(s) %d modèle(s). Certains endpoints ont échoué: %v"
	case "panel.provider.refresh.succèss":
		return "Actualisé(s) %d endpoint(s), découvert(s) %d modèle(s)."
	case "panel.provider.refresh.faîled":
		return "Erreur d'actualisation des modèles: %s"
	case "panel.provider.refresh.none":
		return "Aucun endpoint actualisable pour ce fournisseur."
	case "panel.model.models":
		return "Modèles"
	case "panel.model.vendor":
		return "Fournisseur"
	case "panel.model.endpoint":
		return "Endpoint"
	case "panel.model.protocol":
		return "Protocole"
	case "panel.model.source":
		return "Source"
	case "panel.model.source.builtin":
		return "intégré"
	case "panel.model.source.remote":
		return "distant"
	case "panel.model.refreshing":
		return "Actualisation des derniers modèles..."
	case "panel.model.hint.main":
		return "j/k déplacer • Enter ou s appliquér • w fenêtre de contexte • o max tokens • r actualisér • / filtrer • Esc fermér"
	case "panel.model.hint.edit":
		return "Enter sauvegardér • Esc annulér (0 ou vide = auto, suffixe K/M/G permis ex. 256k)"
	case "panel.model.context_window":
		return "Fenêtre de Contexte"
	case "panel.model.max_tokens":
		return "Max Tokens de Sortie"
	case "panel.model.edit":
		return "Éditer"
	case "panel.model.saved_runtime_inactive":
		return "Config sauvegardée, mais le runtime reste inactif: %s"
	case "panel.model.context_applied":
		return "Applique context_window=%d, max_tokens=%d (sauvegardé)"
	case "panel.model.context_cleared":
		return "Rétabli en autodetection (sauvegardé)"
	case "panel.model.switched":
		return "Modèle change en %s."
	case "panel.model.refresh.save_faîled":
		return "Modèles actualisés, mais erreur de sauvegardé de config: %s"
	case "panel.model.refresh.builtin_reason":
		return "Utilisation des modèles intégrés: %s"
	case "panel.model.refresh.remote_loaded":
		return "Chargé(s) %d modèle(s) distant(s)."
	case "panel.model.refresh.builtin_loaded":
		return "Modèles intégrés chargés."
	case "command.unknown":
		return "Commande inconnue: %s\n"
	case "command.retry_empty":
		return "Aucun envoi précédent a réessayer."
	case "command.retry_busy":
		return "L'agent est occupé. Attendez la fin de l'exécution actuelle avant de réessayer."
	case "command.edit_empty":
		return "Aucun envoi précédent a éditer."
	case "command.edit_busy":
		return "L'agent est occupé. Attendez la fin de l'exécution actuelle avant d'éditer."
	case "command.edit_ready":
		return "Dernier envoi chargé — éditéz et appuyez sur Enter pour envoyér."
	case "command.help_hint":
		return "Saisissez /help pour voir les commandes disponibles\n\n"
	case "command.usâge.allow":
		return "Usâge: /allow <nom-outil>\n\n"
	case "command.usâge.resume":
		return "Usâge: /resume <id-session>\n\n"
	case "command.usâge.export":
		return "Usâge: /export <id-session>\n\n"
	case "init.resolve_faîled":
		return "Erreur de resolution de la cible d'init: %v\n\n"
	case "init.generate_faîled":
		return "Erreur de génération du contenu GGCODE.md: %v\n\n"
	case "init.collecting":
		return "Collecte des connaissances du projet..."
	case "init.prompt.title":
		return "Initialisér le projet"
	case "init.prompt.body":
		return "Aucun GGCODE.md trouvé dans ce projet. En créer un pour aider l'agent a comprendre les conventions de votre code?"
	case "init.prompt.yes":
		return "Créer"
	case "init.prompt.no":
		return "Passer"
	case "init.prompt.hint":
		return " y = créer GGCODE.md • n/Esc = passer"
	case "command.model_switched":
		return "Modèle change en: %s (fournisseur: %s)\n\n"
	case "command.model_faîled":
		return "Erreur lors du changement de modèle: %v\n\n"
	case "command.model_current":
		return "Modèle actuel: %s (fournisseur: %s)\nModèles disponibles: %s\nUtilisez /model pour ouvrir le panneau de modèles ou /model <nom-modèle> pour changer directement.\n\n"
	case "command.provider_unknown":
		return "Fournisseur inconnu: %s (disponibles: %v)\n\n"
	case "command.provider_switched":
		return "Fournisseur change en: %s (modèle: %s)\n\n"
	case "command.provider_faîled":
		return "Erreur lors de la mise à jour de la sélection de fournisseur: %v\n\n"
	case "command.provider_current":
		return "Fournisseur actuel: %s (endpoint: %s, modèle: %s)\nFournisseurs disponibles: %s\nEndpoints disponibles: %s\nUsâge: /provider [fournisseur] [endpoint]\n\n"
	case "command.allow_set":
		return "✓ %s désormais toujours autorisé\n\n"
	case "command.custom":
		return "Commande personnalisée /%s:\n"
	case "command.mention_error":
		return "Erreur de mention: %v"
	case "session.list_faîled":
		return "Erreur lors du listâge des sessions: %v\n\n"
	case "session.untitled":
		return "sans titre"
	case "session.store_missing":
		return "Stockâge de sessions non configuré.\n\n"
	case "session.none":
		return "Aucune session trouvée.\n\n"
	case "session.list.title":
		return "Sessions:\n\n"
	case "session.list.item":
		return "  %d. %s  %s  (%s)\n"
	case "session.list.hint":
		return "\nUtilisez /resume <id> pour reprendre une session\n\n"
	case "session.new":
		return "Nouvelle session: %s\n\n"
	case "session.resume":
		return "Session reprise: %s — %s (%d messâges)\n\n"
	case "session.resume_faîled":
		return "Erreur lors de la reprise de la session %s: %v\n\n"
	case "session.resume_fallback":
		return "Démarrage d'une nouvelle session à la place.\n\n"
	case "session.export_faîled":
		return "Erreur lors de l'export de session: %v\n\n"
	case "session.write_faîled":
		return "Erreur lors de l'écriture du fichier: %v\n\n"
	case "session.exported":
		return "Session exportée %s vers %s\n\n"
	case "checkpoint.disabled":
		return "Points de contrôle non actives.\n\n"
	case "checkpoint.undo_faîled":
		return "Erreur d'annulation: %v\n\n"
	case "checkpoint.undid":
		return "Annulé %s sur %s (checkpoint %s)\n"
	case "checkpoint.none":
		return "Aucun checkpoint.\n\n"
	case "fîles.disabled":
		return "Points de contrôle non actives.\n\n"
	case "fîles.none":
		return "Aucun fichier modifié par l'agent dans cette session.\n\n"
	case "fîles.title":
		return "Fichiers modifiés par l'agent (%d fichiers, %d éditions):\n\n"
	case "fîles.item":
		return "  %s  %d éditions  dernier: %s%s\n"
	case "fîles.hint":
		return "\nUtilisez /undo pour annulér l'édition la plus récente, /checkpoints pour les détails.\n\n"
	case "checkpoint.list.title":
		return "Checkpoints (%d):\n\n"
	case "checkpoint.list.item":
		return "  %d. %s  %s  %s  %s\n"
	case "checkpoint.list.hint":
		return "\nUtilisez /undo pour annulér le plus récent.\n\n"
	case "memory.auto_unavailable":
		return "Mémoire automatique non initialisée.\n\n"
	case "memory.list_faîled":
		return "Erreur lors du listâge des mémoires: %v\n\n"
	case "memory.none":
		return "Aucune mémoire automatique sauvegardée.\n\n"
	case "memory.auto_title":
		return "Mémoires Automatiques:\n"
	case "memory.clear_faîled":
		return "Erreur lors du nettoyâge des mémoires: %v\n\n"
	case "memory.cleared":
		return "Toutes les mémoires automatiques supprimées.\n\n"
	case "memory.title":
		return "Mémoire:\n"
	case "memory.project":
		return "Mémoire du Projet:\n"
	case "memory.project_none":
		return "  Aucun fichier de mémoire de projet chargé.\n"
	case "memory.auto":
		return "Mémoire Automatique:\n"
	case "memory.auto_none":
		return "  Aucune mémoire automatique chargée.\n"
	case "memory.usâge":
		return "\nUsâge: /memory [list|clear]\n\n"
	case "compact.unavailable":
		return "Gestionnaire de contexte non disponible.\n\n"
	case "compact.faîled":
		return "Erreur de compactâge: %v\n\n"
	case "compact.done":
		return "Historique de conversation compacté.\n\n"
	case "compact.done_with_stats":
		return "Historique de conversation compacté (%d → %d tokens).\n\n"
	case "todo.cleared":
		return "Liste de tâches supprimée.\n\n"
	case "todo.clear_faîled":
		return "Erreur lors du nettoyâge des tâches: %v\n\n"
	case "todo.none":
		return "Aucune liste de tâches trouvée. Utilisez l'outil todo_write pour en créer une.\n\n"
	case "todo.read_faîled":
		return "Erreur de lecture des tâches: %v\n\n"
	case "todo.parse_faîled":
		return "Erreur d'analysé des tâches: %v\n\n"
	case "todo.title":
		return "Liste de tâches:\n%s\n\n"
	case "bug.title":
		return "=== Diagnostics de Rapport de Bug ===\n\n"
	case "bug.version":
		return "Version: %s\n"
	case "bug.os":
		return "OS: %s %s\n"
	case "bug.go":
		return "Go: %s\n"
	case "bug.provider":
		return "Fournisseur: %s\n"
	case "bug.model":
		return "Modèle: %s\n"
	case "bug.session":
		return "Session: %s (%d messâges)\n"
	case "bug.mcp":
		return "Serveurs MCP: %d\n"
	case "bug.last_error":
		return "Dernière erreur: %s\n"
	case "bug.hint":
		return "\nIncluez ces informations lors du signalement d'un bug.\n\n"
	case "config.usâge":
		return "Usâge: /config set <cle> <valeur>\n\nCles: model, vendor, endpoint, languâge, apikey [--vendor]\n\nEndpoints: /config add-endpoint <nom> <url_base> [--protocol openai] [--apikey sk-xxx]\n          /config remove-endpoint <nom>\n\n"
	case "config.not_loaded":
		return "Configuration non chargée.\n\n"
	case "config.model_set":
		return "Config: modèle = %s\n\n"
	case "config.provider_set":
		return "Config: fournisseur = %s\n\n"
	case "config.languâge_set":
		return "Config: langue = %s\n\n"
	case "config.unknown_key":
		return "Cle de config inconnue: %s\nSupportées: model, provider, languâge\n\n"
	case "config.title":
		return "Configuration Actuelle:\n"
	case "status.title":
		return "Statut:\n"
	case "panel.update":
		return "Mise à jour"
	case "label.version":
		return "Version"
	case "label.latest":
		return "Dernière"
	case "update.sidebar_hint":
		return "Nouvelle version disponible. Exécutez /update."
	case "update.up_to_date":
		return "À jour."
	case "update.available":
		return "mise à jour disponible: %s"
	case "update.current":
		return "actuel: %s (dernière: %s)"
	case "update.unknown":
		return "non vérifié encore"
	case "update.check_faîled":
		return "vérification échouée: %s"
	case "update.unavailable":
		return "Mise à jour non disponible dans cette session.\n\n"
	case "update.preparing":
		return "Préparation de la mise à jour"
	case "update.faîled":
		return "Mise à jour échouée: %v\n\n"
	case "update.restart_faîled":
		return "Mise à jour préparée, mais erreur de redémarrage: %v\n\n"
	case "update.pm_hint.brew":
		return "Mise à jour installée. Noté: ggcode a été installe via Homebrew.\nExécutez `brew upgrade ggcode` pour garder Homebrew synchronisé.\n\n"
	case "update.pm_hint.scoop":
		return "Mise à jour installée. Noté: ggcode a été installe via Scoop.\nExécutez `scoop update ggcode` pour garder Scoop synchronisé.\n\n"
	case "update.pm_hint.winget":
		return "Mise à jour installée. Noté: ggcode a été installe via winget.\nExécutez `winget upgrade ggcode` pour garder winget synchronisé.\n\n"
	case "update.pm_hint.snap":
		return "Mise à jour installée. Noté: ggcode a été installe via Snap.\nExécutez `sudo snap refresh ggcode` pour garder Snap synchronisé.\n\n"
	case "update.other_installs":
		return "Autres installations de ggcode détectées sur ce système:\n%s\nSi un ggcode différent apparaît en premier dans PATH, envisagez de le mettre à jour aussi ou d'ajuster l'ordre de PATH.\n\n"
	case "update.dual_scope":
		return "Attention: Installations utilisateur et système de ggcode trouvées:\n  Utilisateur: %s\n  Système: %s\nCela peut causer des conflits de PATH. Envisâgez de desinstaller une via Parametrès > Applications.\n\n"
	case "plugins.unavailable":
		return "Gestionnaire de plugins non disponible.\n\n"
	case "plugins.none":
		return "Aucun plugin chargé.\n\n"
	case "plugins.title":
		return "Plugins:\n"
	case "mcp.none":
		return "Aucun serveur MCP configuré.\n\n"
	case "mcp.title":
		return "Serveurs MCP:\n"
	case "mcp.active_tools":
		return "Outils actifs"
	case "mcp.more":
		return "… %d de plus • /mcp"
	case "imâge.usâge":
		return "Usâge: /imâge <chemin/vers/fichier.png> ou /imâge paste\n"
	case "imâge.formats":
		return "Formats supportes: PNG, JPEG, GIF, WebP (max 20MB)\n\n"
	case "imâge.attâched":
		return "Imâge jointe: %s\n"
	case "imâge.attâched_hint":
		return "Envoyéz un messâge pour inclure l'imâge, ou /imâge pour en joindre une autre.\n\n"
	case "imâge.clipboard_faîled":
		return "Impossible de coller une imâge du presse-papiers: %v"
	case "imâge.clipboard_no_imâge_windows":
		return "Aucune imâge dans le presse-papiers. Sous Windows, Ctrl+V est souvent intercepte par le terminal. Essayez Ctrl+Shift+V ou /imâge paste."
	case "agents.unavailable":
		return "Gestionnaire de sous-agents non configuré.\n\n"
	case "agents.none":
		return "Aucun sous-agent créé pour le moment.\nUsâge: le LLM peut utilisér l'outil spawn_agent pour créer des sous-agents.\n\n"
	case "agents.title":
		return "%d sous-agent(s):\n"
	case "agents.item":
		return "  %s [%s]%s - %s\n"
	case "agents.hint":
		return "\nUtilisez /agent <id> pour les détails, /agent cancel <id> pour annulér.\n\n"
	case "agent.usâge":
		return "Usâge: /agent <id> ou /agent cancel <id>\n\n"
	case "agent.cancelled":
		return "Sous-agent %s annulé\n\n"
	case "agent.cancel_faîled":
		return "Impossible d'annulér %s (introuvable ou pas en cours)\n\n"
	case "agent.not_found":
		return "Sous-agent %s introuvable\n\n"
	case "agent.title":
		return "Agent: %s\nStatut: %s\nTâche: %s\n"
	case "agent.result":
		return "Résultat: %s\n"
	case "agent.error":
		return "Erreur: %v\n"
	case "slash.help":
		return "Afficher le messâge d'aide"
	case "slash.sessions":
		return "Lister les sessions sauvegardées"
	case "slash.resume":
		return "Reprendre une session précédente"
	case "slash.export":
		return "Exporter la session en markdown"
	case "slash.model":
		return "Changer de modèle"
	case "slash.provider":
		return "Ouvrir le gestionnaire de fournisseurs"
	case "slash.clear":
		return "Effacér la conversation"
	case "slash.mcp":
		return "Afficher les serveurs MCP"
	case "slash.memory":
		return "Gérér la mémoire"
	case "slash.undo":
		return "Annulér la dernière édition de fichier"
	case "slash.fîles":
		return "Afficher les fichiers modifiés par l'agent"
	case "slash.checkpoints":
		return "Lister les checkpoints"
	case "slash.allow":
		return "Toujours autorisér un outil"
	case "slash.plugins":
		return "Lister les plugins chargés"
	case "slash.imâge":
		return "Joindre une imâge"
	case "slash.init":
		return "Génèrer le GGCODE.md du projet"
	case "slash.harness":
		return "Exécuter des commandes de workflow harness"
	case "slash.lang":
		return "Changer là langue de l'interface"
	case "slash.skills":
		return "Explorer les compétences disponibles"
	case "slash.exit":
		return "Quitter ggcode"
	case "slash.compact":
		return "Compactér l'historique de conversation"
	case "slash.todo":
		return "Voir/gérér la liste de tâches"
	case "slash.bug":
		return "Signaler un bug"
	case "slash.config":
		return "Voir/modifier la configuration"
	case "slash.qq":
		return "Gérér la liaison de canal QQ"
	case "slash.telegram":
		return "Gérér la liaison de canal Telegram"
	case "slash.pc":
		return "Gérér la liaison de canal PC"
	case "slash.discord":
		return "Gérér la liaison de canal Discord"
	case "slash.feishu":
		return "Gérér la liaison de canal Feishu"
	case "slash.slack":
		return "Gérér la liaison de canal Slack"
	case "slash.dingtalk":
		return "Gérér la liaison de canal DingTalk"
	case "slash.wechat":
		return "Gérér la liaison de canal WeChat"
	case "slash.wecom":
		return "Gérér la liaison de canal WeCom (WeChat Entreprise)"
	case "slash.mattermost":
		return "Gérér la liaison de canal Mattermost"
	case "slash.matrix":
		return "Gérér la liaison de canal Matrix"
	case "slash.signal":
		return "Gérér la liaison de canal Signal"
	case "slash.irc":
		return "Gérér la liaison de canal IRC"
	case "slash.nostr":
		return "Gérér la liaison de canal Nostr"
	case "slash.twitch":
		return "Gérér la liaison de canal Twitch"
	case "slash.whatsapp":
		return "Gérér la liaison de canal WhatsApp"
	case "slash.impersonate":
		return "Usurper un outil CLI pour l'affichâge du prompt shell"
	case "slash.knight":
		return "Gérér l'agent autonome en arriere-plan"
	case "slash.stream":
		return "Configurer le mode de sortie en streaming"
	case "slash.diff":
		return "Afficher git diff dans le chat (supporte --cached, <fichier>, --stat)"
	case "slash.hooks":
		return "Afficher les hooks configurés (tous les événements, types, motifs)"
	case "slash.cost":
		return "Afficher l'utilisation de tokens et le coût estime de la session"
	case "slash.review":
		return "Revue de code par AI des changements actuels (bugs, sécurité, conçurrence)"
	case "slash.copy":
		return "Copier la dernière réponse de l'assistant dans le presse-papiers"
	case "slash.context":
		return "Afficher le détail d'utilisation de la fenêtre de contexte (tokens, messâges, capacité)"
	case "slash.im":
		return "Ouvrir le panneau unifie des canaux IM"
	case "panel.qq.directory":
		return "Répertoire"
	case "panel.qq.runtime":
		return "Runtime"
	case "panel.qq.bots":
		return "Bots QQ"
	case "panel.qq.created":
		return "Créés: %d"
	case "panel.qq.bound":
		return "Lies: %d"
	case "panel.qq.available":
		return "Disponibles: %d"
	case "panel.qq.current_binding":
		return "Liaison Actuelle"
	case "panel.qq.none":
		return "(aucun)"
	case "panel.qq.default":
		return "(par défaut)"
	case "panel.qq.adapter":
		return "Adapteur: %s"
	case "panel.qq.target":
		return "Cible: %s"
	case "panel.qq.channel":
		return "Canal: %s"
	case "panel.qq.bot_list":
		return "Liste des Bots QQ"
	case "panel.qq.no_bots":
		return "Aucun bot QQ configuré."
	case "panel.qq.entry.available":
		return "Disponible"
	case "panel.qq.entry.bound":
		return "Lie"
	case "panel.qq.entry.active":
		return "Actif"
	case "panel.qq.entry.bound_other":
		return "Lie: %s"
	case "panel.qq.entry.muted":
		return "Muet"
	case "panel.qq.détails":
		return "Détails"
	case "panel.qq.status":
		return "Statut: %s"
	case "panel.qq.transport":
		return "Transport: %s"
	case "panel.qq.bound_directory":
		return "Répertoire Lie: %s"
	case "panel.qq.current_directory_target":
		return "Cible de Répertoire Actuel: %s"
	case "panel.qq.current_directory_channel":
		return "Canal de Répertoire Actuel: %s"
	case "panel.qq.waiting_for_pairing":
		return "(en attente de liaison)"
	case "panel.qq.last_error":
		return "Dernière Erreur: %s"
	case "panel.qq.occupied_by":
		return "Occupe par: %s"
	case "panel.qq.create":
		return "Créer"
	case "panel.qq.bot_input":
		return "Bot QQ: %s"
	case "panel.qq.create_format":
		return "Format: <bot-id> <appid> <appsecret>"
	case "panel.qq.create_example":
		return "Exemple: qq-main 123456 valeur-secrete"
	case "panel.qq.create_hint":
		return "Enter créer bot • Esc annulér"
	case "panel.qq.actions_hint":
		return "j/k déplacer • Enter ou b lier bot • c lier canal • x délier canal • u délier bot • i créer bot • Esc fermér"
	case "panel.qq.bind_channel":
		return "Lier Canal"
	case "panel.qq.scan_hint":
		return "Scannez le code QR, ajoutéz le bot, puis envoyéz un messâge pour démarrer la liaison."
	case "panel.qq.qr_code":
		return "Code QR:"
	case "panel.qq.share_link":
		return "Lien de Partâge:"
	case "panel.qq.messâge.no_bot":
		return "Aucun bot QQ disponible."
	case "panel.qq.messâge.bound_succèss":
		return "Bot QQ lie au workspace actuel. Utilisez c pour générer le code QR de liaison de canal."
	case "panel.qq.messâge.share_generated":
		return "Lien de partâge QQ généré. Scannez le code QR, ajoutéz le bot, puis envoyéz un messâge pour démarrer la liaison."
	case "panel.qq.messâge.unbound":
		return "Canal QQ delie."
	case "panel.qq.messâge.cleared":
		return "Autorisation de canal QQ supprimée pour le workspace actuel."
	case "panel.qq.messâge.added_bot":
		return "Bot QQ ajouté %s."
	case "panel.qq.error.config_unavailable":
		return "configuration non disponible"
	case "panel.qq.error.config_format":
		return "La config du bot QQ doit être: <bot-id> <appid> <appsecret>"
	case "panel.qq.error.adapter_required":
		return "Le nom de l'adapteur QQ est requis"
	case "panel.qq.error.not_configuréd":
		return "Le bot QQ %q n'est pas configuré"
	case "panel.qq.error.disabled":
		return "Le bot QQ %q est désactivé"
	case "panel.qq.error.not_qq_adapter":
		return "l'adapteur %q n'est pas un bot QQ"
	case "panel.qq.error.not_online":
		return "Le bot QQ %q n'est pas en ligne"
	case "panel.qq.error.not_online_détail":
		return "Le bot QQ %q n'est pas en ligne: %s"
	case "panel.qq.runtime.available":
		return "disponible"
	case "panel.qq.runtime.disabled":
		return "désactivé (définissez im.enabled: true et redémarréz ggcode)"
	case "panel.qq.runtime.not_started":
		return "non démarré (redémarréz ggcode pour initialisér le runtime IM)"
	case "panel.qq.status.not_started":
		return "non démarré"
	case "panel.qq.status.unknown":
		return "inconnu"
	case "slash.status":
		return "Afficher le statut actuel"
	case "slash.update":
		return "Mettre à jour ggcode"
	case "slash.cron":
		return "Gérér les tâches cron planifiees (lister, pauser, reprendre, créer)"
	case "slash.branch":
		return "Bifurquér la session actuelle vers une nouvelle session (fork de conversation)"
	case "slash.chat":
		return "Ouvrir le panneau LAN Chat"
	case "slash.edit":
		return "Éditer et renvoyér votre dernier messâge"
	case "slash.inspector":
		return "Basculer le panneau inspecteur"
	case "slash.mode":
		return "Afficher ou changer le mode de permission"
	case "slash.nick":
		return "Définir votre pseudo LAN Chat"
	case "slash.reflect":
		return "Lancér l'auto-réflexion sur la session actuelle"
	case "slash.regenerate":
		return "Regénérer la dernière réponse AI (annulér et réexécuter)"
	case "slash.restart":
		return "Redémarrer le processus ggcode"
	case "slash.retry":
		return "Réessayer la dernière exécution agent échouée"
	case "slash.rules":
		return "Afficher les règles ratchet apprises"
	case "slash.share":
		return "Partâger la session via tunnel (relay mobile)"
	case "slash.stats":
		return "Afficher les statistiques de session (tokens, iterations, outils)"
	case "slash.tmux":
		return "Ouvrir le menu de gestion des panneaux tmux"
	case "slash.tunnel":
		return "Basculer la connexion tunnel pour relay mobile"
	case "slash.unshare":
		return "Arrêtér le partâge de session via tunnel"
	case "regenerate.busy":
		return "Impossible de regénérer pendant que l'agent s'exécute. Appuyez sur Ctrl+C pour annulér d'abord."
	case "regenerate.no_agent":
		return "Agent non initialisé."
	case "regenerate.no_context":
		return "Gestionnaire de contexte non disponible."
	case "regenerate.no_response":
		return "Aucune réponse de l'assistant a regénérer."
	case "branch.busy":
		return "Impossible de bifurquér pendant que l'agent s'exécute. Appuyez sur Ctrl+C pour annulér d'abord."
	case "branch.no_session":
		return "Aucune session active a bifurquér."
	case "branch.empty":
		return "La session n'a pas de messâges a bifurquér."
	case "branch.save_faîled":
		return "Erreur lors de la creation de la session bifurquée: %v"
	case "branch.succèss":
		return "Bifurqué vers nouvelle session %s (depuis: %s). La session originale est conservée."
	case "help.text":
		return `Commandes disponibles:

Session et Historique:
  /help, /?          Afficher ce messâge d'aide
  /sessions          Lister toutes les sessions sauvegardées
  /resume <id>       Reprendre une session précédente
  /export <id>       Exporter la session en fichier markdown
  /clear             Effacér l'historique de conversation
  /compact           Compactér l'historique de conversation (manuel)
  /undo              Annulér la dernière édition de fichier (rollback checkpoint)
  /checkpoints       Lister tous les checkpoints d'édition
  /regenerate        Annulér la dernière réponse et regénérer (alias: /regen)
  /branch            Bifurquér la conversation actuelle vers une nouvelle session (alias: /fork)

Modèle et Fournisseur:
  /model [nom]       Ouvrir le panneau de modèles ou changer directement
  /provider [vendor] Ouvrir le gestionnaire de fournisseurs
  /mode <mode>       Définir le mode de l'agent (supervised|plan|auto|bypass|autopilot)

Developpement:
  /diff [opts]       Afficher git diff dans le chat (--cached, --stat, <fichier>)
  /review [opts]     Revue de code par AI des changements actuels (--cached, --stâged)
  /copy              Copier la dernière réponse de l'assistant dans le presse-papiers
  /cost              Afficher l'utilisation de tokens et le coût estime de la session
  /context           Afficher le détail d'utilisation de la fenêtre de contexte
  /hooks             Afficher les hooks configurés
  /init              Génèrer le GGCODE.md du projet actuel
  /harness ...       Exécuter des commandes harness
  /todo              Voir la liste de tâches
  /todo clear        Effacér la liste de tâches

Integrations:
  /im                Ouvrir le panneau unifie des canaux IM
  /mcp               Afficher les serveurs MCP et outils connectés
  /plugins           Lister les plugins chargés et leurs outils
  /skills            Explorer les compétences disponibles
  /memory            Afficher les fichiers de mémoire chargés
  /agents            Lister les sous-agents
  /cron <sub>        Gérér les tâches planifiees (list|get|pause|resume|pauseall|resumeall)

Système:
  /lang [code]       Choisir ou changer là langue de l'interface
  /config            Afficher la configuration actuelle
  /config set <k> <v> Définir une valeur de configuration
  /status            Afficher le statut actuel
  /update            Mettre à jour ggcode vers la dernière version
  /restart           Redémarrer ggcode (chargé le dernier binaire)
  /bug               Signaler un bug avec diagnostics
  /exit, /quit       Quitter ggcode

Raccourcis clavier:
  Tab                Naviguer l'autocompletion ou les options d'approbation
  Shift+Tab          Autocompletion inverse, ou basculer le mode de permission
  Ctrl+R             Basculer la barre laterale
  Ctrl+N/P           Nouvelle/session précédente
  Ctrl+T             Ouvrir tunnel (partâge mobile)
  Enter              Envoyér messâge / appliquér la sélection actuelle
  Esc                Annulér l'autocompletion / quitter le mode shell inactif
  Up/Down            Naviguer l'historique des commandes (ou autocompletion)
  PgUp/PgDn          Défîler la sortie de conversation
  Ctrl+C             Annulér l'activité en cours, sinon effacér l'entrée et appuyer à nouveau pour quitter
  Ctrl+D             Quitter immediatement
  Ctrl+A / Ctrl+E    Déplacer le curseur au début / fin de ligne
  Ctrl+K             Supprimér du curseur à la fin de ligne
  Ctrl+U             Supprimér du début de ligne au curseur
  Ctrl+W             Supprimér le mot avant le curseur
  Ctrl+Backspace     Retirér la dernière imâge jointe
  Shift+Enter        Insérer une nouvelle ligne (Ctrl+J ou Alt+Enter dans tmux)
  $ / !              Entrer en mode shell
  #                  Entrer en mode envoi rapide LAN Chat

Souris:
  Option+drag / Shift+drag  Sélectionner du texte a copier (outrepasser la capture souris de l'app)
  Molette de la souris      Défîler la sortie de conversation`
	case "command.harness_usâge":
		return "Usâge: /harness <init|check|queue|tasks|run|rerun|run-queued|monitor|contexts|inbox|review|promote|release|gc|doctor> ... (release supporte rollouts|advance|pause|resume|abort|approve|reject)"
	case "command.harness_queue_usâge":
		return "Usâge: /harness queue <objectif>"
	case "command.harness_run_usâge":
		return "Usâge: /harness run <objectif>"
	case "command.harness_rerun_usâge":
		return "Usâge: /harness rerun <task-id>"
	case "command.skill_agent_only":
		return "La compétence %s ne peut être invoquée que par l'agent."
	case "command.harness_owner_promoted":
		return "Promue(s) %d tâche(s) harness pour le propriétaire %s."
	case "command.harness_review_approved":
		return "Tâche harness %s approuvée."
	case "command.harness_review_rejected":
		return "Tâche harness %s rejetée."
	case "command.harness_promoted_many":
		return "Promue(s) %d tâche(s) harness."
	case "command.harness_promoted_one":
		return "Tâche harness %s promue."
	case "command.harness_task_queued_détail":
		return "Tâche harness enfilée %s.\n- objectif: %s"
	case "command.harness_tasks_empty":
		return "Aucune tâche harness enregistrée."
	case "command.harness_run_start":
		return "Démarrage de l'exécution harness tracée...\nUtilisez /harness monitor ou les vues Tâches/Monitor pour le statut en direct."
	case "command.harness_rerun_start":
		return "Démarrage de la réexécution harness tracée...\nUtilisez /harness monitor ou les vues Tâches/Monitor pour le statut en direct."
	case "command.harness_rerun_invalid_status":
		return "La tâche harness %s est %s; seules les tâches échouées peuvent être réexécutées."
	case "command.harness_status_starting_run":
		return "Démarrage de l'exécution harness..."
	case "command.harness_status_starting_rerun":
		return "Démarrage de la réexécution harness..."
	case "command.harness_spinner_running":
		return "Exécution harness"
	case "command.harness_cancelled":
		return "Exécution harness annulée."
	case "tunnel.stopped":
		return "Tunnel arrêté."
	case "tunnel.not_active":
		return "Aucune session de partâge active."
	case "tunnel.mobile_connectéd":
		return "Client mobile connecté."
	case "config.save_scope_global":
		return "Sauvegardér vers → Global"
	case "config.save_scope_instance":
		return "Sauvegardér vers → Instance"
	case "config.save_scope_instance_new":
		return "Sauvegardér vers → Instance (une nouvelle config sera créée à la sauvegardé)"
	case "config.instance_unavailable":
		return "Config d'instance non disponible pour ce workspace"
	case "config.scope_instance":
		return "Instance"
	case "config.scope_global":
		return "Global"
	case "config.save_target_new_hint":
		return " (nouvelle config sera créée à la sauvegardé)"
	case "config.save_target_line":
		return " Sauvegardér vers: %s%s  [Ctrl+T basculer]"
	case "shell.empty":
		return "La commande shell est vide."
	case "lanchat.unavailable":
		return "LAN Chat n'est pas disponible."
	case "reflect.no_agent":
		return "Agent non initialisé."
	case "reflect.no_workdir":
		return "Répertoire de travail non défini."
	case "reflect.no_memory":
		return "Mémoire de projet non disponible pour ce répertoire."
	case "reflect.load_faîled":
		return "Erreur de chargément des insights: %v"
	case "reflect.empty":
		return "Aucun insight d'exécution pour le moment. Les insights sont générés automatiquement après chaque exécution d'agent avec 3+ appels d'outils ou éditions de fichiers."
	case "reflect.title":
		return "## Insights d'Exécution Accumules\n\n"
	case "reflect.memory_location":
		return "Emplacement de mémoire: %s\n"
	case "knight.unavailable":
		return "Knight n'est pas disponible"
	case "pairing.rejected":
		return "La demandé de liaison actuelle a été rejetée. Veuillez redémarrer pour continuer."
	case "pairing.blacklisted":
		return "Ce canal a été bloque en raison de multiples rejets."
	default:
		return enCatalog(key)
	}
}
