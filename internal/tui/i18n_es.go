package tui

// esCatalog returns the Spanish translation for the given key.
// Keys not yet translated fall through to enCatalog.
func esCatalog(key string) string {
	switch key {
	case "workspace.tagline":
		return "espacio de trabajo geek AI"
	case "header.terminal_native":
		return "programación con AI nativa de terminal"
	case "session.ephemeral":
		return "efímera"
	case "agents.idle":
		return "inactivo"
	case "agents.running":
		return "%d en ejecución"
	case "cron.firing":
		return "⏰ tarea cron activada"
	case "activity.idle":
		return "inactivo"
	case "panel.conversation":
		return "Conversación"
	case "panel.composer":
		return "Editor"
	case "panel.composer_locked":
		return "Editor bloqueado"
	case "panel.commands":
		return "Comandos:"
	case "panel.files":
		return "Archivos:"
	case "panel.agent_status":
		return "Estado del agente"
	case "panel.mode_policy":
		return "Política de modo"
	case "panel.session_usage":
		return "Uso de sesión"
	case "panel.metrics":
		return "Métricas"
	case "panel.context":
		return "Contexto"
	case "panel.im":
		return "IM"
	case "panel.mcp":
		return "MCP"
	case "panel.mcp.install_spec_required":
		return "Ingrese una especificacion de instalación primero."
	case "panel.mcp.installing_server":
		return "Instalando servidor MCP..."
	case "panel.mcp.reconnect_unavailable":
		return "Reconexión no disponible en esta sesión."
	case "panel.mcp.reconnecting":
		return "Reconectando %s..."
	case "panel.mcp.reconnect_failed":
		return "No se pudo reconectar %s."
	case "panel.mcp.uninstalling":
		return "Desinstalando %s..."
	case "panel.startup":
		return "Inicializando"
	case "panel.approval_required":
		return "Aprobación requerida"
	case "panel.bypass_approval":
		return "Aprobación en modo bypass"
	case "panel.review_file_change":
		return "Revisar cambio de archivo"
	case "label.vendor":
		return "proveedor"
	case "label.endpoint":
		return "endpoint"
	case "label.model":
		return "modelo"
	case "label.mode":
		return "modo"
	case "label.session":
		return "sesión"
	case "label.agents":
		return "agentes"
	case "label.cwd":
		return "dir. actual"
	case "label.branch":
		return "rama"
	case "label.context":
		return "contexto"
	case "label.skills":
		return "habilidades"
	case "label.activity":
		return "actividad"
	case "label.window":
		return "ventana"
	case "label.usage":
		return "uso"
	case "label.compact":
		return "compacto"
	case "label.total":
		return "total"
	case "label.cost":
		return "costo est."
	case "label.approval_policy":
		return "aprobación"
	case "label.tool_policy":
		return "herramientas"
	case "label.agent_policy":
		return "agente"
	case "label.tool":
		return "herramienta"
	case "label.input":
		return "entrada"
	case "label.output":
		return "salida"
	case "label.cache_read":
		return "lectura cache"
	case "label.cache_write":
		return "escritura cache"
	case "label.cache_hit":
		return "aciertos cache"
	case "label.turns":
		return "turnos"
	case "label.avg_ttft":
		return "ttft medio"
	case "label.p95_ttft":
		return "ttft p95"
	case "label.avg_duration":
		return "dur. media"
	case "label.p95_duration":
		return "dur. p95"
	case "label.avg_think":
		return "pens. medio"
	case "label.fail_rate":
		return "tasa fallos"
	case "label.slow_tools":
		return "herr. lentas"
	case "label.recent_turns":
		return "turnos recientes"
	case "label.file":
		return "archivo"
	case "label.directory":
		return "directorio"
	case "context.unavailable":
		return "Sin datos de contexto aún"
	case "metrics.empty":
		return "Sin métricas aún"
	case "im.none":
		return "Sin adaptadores configurados"
	case "im.summary":
		return "%d adaptadores • %d sanos"
	case "im.more":
		return "+%d más (/qq)"
	case "im.runtime.available":
		return "runtime disponible"
	case "im.runtime.disabled":
		return "deshabilitado"
	case "im.runtime.not_started":
		return "habilitado • reinicie para inicializar"
	case "im.status.not_started":
		return "no iniciado"
	case "context.until_compact":
		return "restánte"
	case "empty.ask":
		return "Pida una refactorización, corrección de errores, explicación o pruebas."
	case "empty.tips":
		return "Consejos: use @ruta para incluir archivos, /? para ayuda, y Shift+Tab para cambiar de modo."
	case "startup.banner":
		return "Preparando la interfaz de terminal y filtrando el ruido de inicio. Puede escribir de inmediato; este banner desaparece una vez que el inicio se complete."
	case "harness.views":
		return "Vistas"
	case "harness.items":
		return "Elementos"
	case "harness.action":
		return "Acción"
	case "harness.details":
		return "Detalles"
	case "harness.none":
		return "(ninguno)"
	case "harness.unknown":
		return "desconocido"
	case "harness.unscoped":
		return "sin alcance"
	case "harness.unavailable":
		return "Harness no disponible"
	case "harness.unavailable_intro":
		return "Comience aquí en un proyecto existente:"
	case "harness.unavailable_step_init":
		return "  1. Presione Enter o i para inicializar harness"
	case "harness.unavailable_step_refresh":
		return "  2. Presione r para actualizar una vez que termine la inicialización"
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
		return "Contextos"
	case "harness.section.tasks":
		return "tareas"
	case "harness.section.queue":
		return "Cola"
	case "harness.section.run":
		return "Ejecutar"
	case "harness.section.run_queued":
		return "Ejecutar encoladas"
	case "harness.section.inbox":
		return "Bandeja"
	case "harness.section.review":
		return "Revisión"
	case "harness.section.promote":
		return "Promover"
	case "harness.section.release":
		return "Release"
	case "harness.section.rollouts":
		return "Rollouts"
	case "harness.hints.unavailable":
		return "Enter/i init harness • r actualizar • Esc cerrar"
	case "harness.hints.move":
		return "j/k mover"
	case "harness.hints.tab":
		return "Tab cambiar"
	case "harness.hints.refresh":
		return "r actualizar"
	case "harness.hints.close":
		return "Esc cerrar"
	case "harness.hints.check":
		return "Enter ejecutar checks"
	case "harness.hints.monitor":
		return "Enter actualizar snapshot"
	case "harness.hints.gc":
		return "Enter ejecutar gc"
	case "harness.hints.type_goal":
		return "escribir objetivo"
	case "harness.hints.queue":
		return "Enter encolar"
	case "harness.hints.run":
		return "Enter ejecutar"
	case "harness.hints.focus_input":
		return "Tab enfocar entrada"
	case "harness.hints.rerun":
		return "Enter reejecutar fallidos"
	case "harness.hints.next":
		return "Enter siguiente"
	case "harness.hints.all":
		return "a todos"
	case "harness.hints.retry_failed":
		return "f reintentar-fallidos"
	case "harness.hints.resume":
		return "s reanudar"
	case "harness.hints.promote_owner":
		return "p promover propietario"
	case "harness.hints.retry_owner":
		return "f reintentar propietario"
	case "harness.hints.approve":
		return "Enter aprobar"
	case "harness.hints.reject":
		return "x rechazar"
	case "harness.hints.promote":
		return "Enter promover"
	case "harness.hints.apply_batch":
		return "Enter aplicar lote"
	case "harness.hints.advance":
		return "Enter avanzar"
	case "harness.hints.approve_gate":
		return "g aprobar gate"
	case "harness.hints.pause_resume":
		return "p pausar/reanudar"
	case "harness.hints.abort":
		return "x abortar"
	case "harness.hint.primary.check":
		return "Presione Enter para ejecutar checks."
	case "harness.hint.primary.monitor":
		return "Presione Enter para actualizar el snapshot del monitor."
	case "harness.hint.primary.gc":
		return "Presione Enter para ejecutar la recolección de basura."
	case "harness.hint.primary.queue":
		return "Escriba un objetivo, luego presione Enter para encolarlo."
	case "harness.hint.primary.run":
		return "Escriba un objetivo, luego presione Enter para iniciar la ejecución."
	case "harness.hint.primary.tasks":
		return "Presione Enter para reejecutar la tárea fallida seleccionada."
	case "harness.hint.primary.run_queued":
		return "Presione Enter para la siguiente; a ejecuta todas; f reintenta las fallidas; s reanuda las interrumpidas."
	case "harness.hint.primary.inbox":
		return "Presione p para promover este propietario o f para reintentarlo."
	case "harness.hint.primary.review":
		return "Presione Enter para aprobar o x para rechazar."
	case "harness.hint.primary.promote":
		return "Presione Enter para promover la tárea seleccionada."
	case "harness.hint.primary.release":
		return "Presione Enter para aplicar el lote de release actual."
	case "harness.hint.primary.rollouts":
		return "Presione Enter para avanzar; g aprueba gate; p pausa/reanuda; x aborta."
	case "harness.hint.primary.none":
		return "No se necesita entrada para esta sección."
	case "harness.message.read_only":
		return "El panel de harness es de solo lectura mientras otra ejecución está activa."
	case "harness.message.monitor_refreshed":
		return "Monitor de harness actualizado."
	case "harness.message.rerun_failed_only":
		return "La tárea de harness %s está %s; solo las táreas fallidas pueden reejecutarse."
	case "harness.message.review_approved":
		return "Revisión aprobada para %s"
	case "harness.message.review_rejected":
		return "Revisión rechazada para %s"
	case "harness.message.promoted":
		return "Promovido %s"
	case "harness.message.no_release_tasks":
		return "No hay táreas de harness listas para release."
	case "harness.message.release_applied":
		return "Lote de release aplicado %s"
	case "harness.message.no_rollouts":
		return "No se encontraron rollouts persistentes."
	case "harness.message.rollout_advanced":
		return "Rollout avanzado %s"
	case "harness.message.owner_promoted":
		return "Promovida(s) %d tárea(s) para %s"
	case "harness.message.owner_retried":
		return "Reintentadas táreas fallidas para %s"
	case "harness.message.gate_approved":
		return "Aprobado el siguiente gate para %s"
	case "harness.message.rollout_resumed":
		return "Rollout reanudado %s"
	case "harness.message.rollout_paused":
		return "Rollout pausado %s"
	case "harness.message.rollout_aborted":
		return "Rollout abortado %s"
	case "harness.message.check_passed":
		return "Check de harness superado."
	case "harness.message.check_failed":
		return "El check de harness encontró problemas."
	case "harness.message.gc_complete":
		return "GC de harness completado."
	case "harness.message.queue_goal_required":
		return "Primero escriba un objetivo de cola en la entrada del panel."
	case "harness.message.queued":
		return "tarea de harness encolada %s"
	case "harness.activity.status":
		return "Harness %s"
	case "harness.log.phase":
		return "Fase"
	case "harness.log.worker":
		return "Worker"
	case "harness.tool.read_file":
		return "Leer archivo"
	case "harness.tool.write_file":
		return "Escribir archivo"
	case "harness.tool.browse_files":
		return "Explorar archivos"
	case "harness.tool.search_code":
		return "Buscar código"
	case "harness.tool.run_command":
		return "Ejecutar comando"
	case "harness.tool.fetch_web_page":
		return "Obtener página web"
	case "harness.tool.run_subagent":
		return "Ejecutar sub-agente"
	case "harness.tool.update_task_state":
		return "Actualizar estado de tárea"
	case "harness.message.run_goal_required":
		return "Primero escriba un objetivo de ejecución en la entrada del panel."
	case "harness.message.no_queued_executed":
		return "No se ejecutaron táreas encoladas de harness."
	case "harness.message.queue_retried":
		return "Reintentada(s) %d tárea(s) encolada(s) fallida(s)."
	case "harness.message.queue_resumed":
		return "Reanudada(s) %d tárea(s) encolada(s) interrumpida(s)."
	case "harness.message.queue_ran":
		return "Ejecutada(s) %d tárea(s) encolada(s)."
	case "harness.preview.not_initialized":
		return "Harness no está inicializado en este proyecto aún.\n\nPresione Enter o i para ejecutar harness init en el repositorio actual."
	case "harness.preview.check":
		return "Ejecutar checks de harness contra el proyecto actual.\n\nEnter: ejecuta checks de archivo/contenido/contexto requeridos más comandos de validación configurados."
	case "harness.preview.gc":
		return "Ejecutar recolección de basura de harness.\n\nEnter: archiva táreas obsoletas, abandona trabajos bloqueados/en ejecución obsoletos, poda logs antiguos y elimina worktrees huerfanos."
	case "harness.preview.queue_help":
		return "Escriba el objetivo de harness aquí, luego presione Enter para encolarlo."
	case "harness.preview.run_help":
		return "Escriba el objetivo de harness aquí, luego presione Enter para iniciar la ejecución."
	case "harness.preview.run_queued":
		return "Estado de cola:\ncol=%d ejecut=%d bloquead=%d fallid=%d\n\nEnter ejecuta la siguiente tárea.\na ejecuta todas las táreas.\nf reintenta las táreas fallidas.\ns reanuda las táreas interrumpidas."
	case "harness.preview.no_owner":
		return "Sin propietario de harness seleccionado."
	case "harness.preview.no_context":
		return "Sin contexto de harness seleccionado."
	case "harness.preview.no_task":
		return "Sin tárea de harness seleccionada."
	case "harness.preview.project_not_initialized":
		return "Harness no está inicializado en este proyecto aún."
	case "harness.preview.project_initialized":
		return "Harness está inicializado."
	case "harness.preview.project_help":
		return "Use /harness para explorar y operar el plano de control."
	case "harness.preview.no_doctor":
		return "Sin reporte de doctor de harness."
	case "harness.preview.monitor_unavailable":
		return "Monitor de harness no disponible."
	case "harness.label.context_title":
		return "Contexto"
	case "harness.label.owner_title":
		return "Propietario"
	case "harness.label.id":
		return "id"
	case "harness.label.status":
		return "estado"
	case "harness.label.goal":
		return "objetivo"
	case "harness.label.attempts":
		return "intentos"
	case "harness.label.depends_on":
		return "depends_on"
	case "harness.label.context":
		return "contexto"
	case "harness.label.workspace":
		return "workspace"
	case "harness.label.branch":
		return "rama"
	case "harness.label.worker":
		return "worker"
	case "harness.label.progress":
		return "progreso"
	case "harness.label.verification":
		return "verificación"
	case "harness.label.changed_files":
		return "changed_files"
	case "harness.label.delivery_report":
		return "delivery_report"
	case "harness.label.delivery_report_human":
		return "reporte de entrega"
	case "harness.label.log":
		return "log"
	case "harness.label.review":
		return "revisión"
	case "harness.label.review_notes":
		return "review_notes"
	case "harness.label.promotion":
		return "promotion"
	case "harness.label.promotion_notes":
		return "promotion_notes"
	case "harness.label.release_batch":
		return "release_batch"
	case "harness.label.release_batch_human":
		return "lote de release"
	case "harness.label.release_notes":
		return "release_notes"
	case "harness.label.error":
		return "error"
	case "harness.label.name":
		return "nombre"
	case "harness.label.description":
		return "descripción"
	case "harness.label.owner":
		return "propietario"
	case "harness.label.commands":
		return "comandos"
	case "harness.label.tasks":
		return "táreas"
	case "harness.label.rollouts":
		return "rollouts"
	case "harness.label.gates":
		return "gates"
	case "harness.label.latest":
		return "último"
	case "harness.label.repo":
		return "repo"
	case "harness.label.config":
		return "config"
	case "harness.label.project":
		return "proyecto"
	case "harness.label.structure":
		return "estructura"
	case "harness.label.contexts":
		return "contextos"
	case "harness.label.workers":
		return "workers"
	case "harness.label.workflow":
		return "workflow"
	case "harness.label.quality":
		return "calidad"
	case "harness.label.worktrees":
		return "worktrees"
	case "harness.label.snapshot":
		return "snapshot"
	case "harness.label.events":
		return "eventos"
	case "harness.label.target":
		return "objetivo"
	case "harness.label.review_ready":
		return "review_ready"
	case "harness.label.promotion_ready":
		return "promotion_ready"
	case "harness.label.retryable":
		return "retryable"
	case "harness.task_title":
		return "tarea de harness"
	case "harness.doctor_title":
		return "Doctor de harness"
	case "harness.monitor_title":
		return "Monitor de harness"
	case "harness.latest_task":
		return "Última tárea"
	case "harness.latest_event":
		return "Último evento"
	case "harness.focus":
		return "Foco"
	case "harness.status.ok":
		return "ok"
	case "harness.status.needs_attention":
		return "requiere atención"
	case "harness.group.review":
		return "revisión"
	case "harness.group.promotion":
		return "promoción"
	case "harness.group.retry":
		return "reintento"
	case "harness.review_ready_short":
		return "revisión"
	case "harness.promote_ready_short":
		return "promover"
	case "harness.tasks_count":
		return "táreas"
	case "harness.input_empty":
		return "(la entrada está vacía)"
	case "harness.no_wavess":
		return "sin oleadas"
	case "harness.mixed":
		return "mixto"
	case "hint.autocomplete":
		return "Tab/Shift+Tab ciclo • Enter aplicar • Esc cerrar"
	case "hint.mention":
		return "@ adjunta archivos/carpetas • Tab/Shift+Tab ciclo • Enter aplicar"
	case "hint.mode":
		return "modo"
	case "mode.approval.ask":
		return "preguntar según sea necesario"
	case "mode.approval.none":
		return "sin paradas de aprobación"
	case "mode.approval.crítical":
		return "solo críticas"
	case "mode.tools.rules":
		return "seguir reglas de herramientas"
	case "mode.tools.readonly":
		return "solo lectura"
	case "mode.tools.safe":
		return "solo operaciónes seguras"
	case "mode.tools.open":
		return "casí todas las herramientas"
	case "mode.agent.waits":
		return "espera por usted"
	case "mode.agent.autocontinúe":
		return "continúa solo"
	case "hint.enter_send":
		return "Enter enviar"
	case "hint.ctrlv_image":
		return "Ctrl+V / Ctrl+Shift+V pegar imagen"
	case "hint.ctrlr_sidebar":
		return "Ctrl+R barra lateral"
	case "hint.help":
		return "/? ayuda"
	case "hint.add_context":
		return "@ agregar contexto"
	case "hint.scroll":
		return "PgUp/PgDn desplazar"
	case "hint.shift_tab_mode":
		return "Shift+Tab modo"
	case "hint.ctrlc_cancel":
		return "Ctrl+C cancelar"
	case "hint.ctrlc_exit":
		return "Ctrl+C limpiar/salir"
	case "hint.image_attached":
		return "imagen adjunta"
	case "hint.image_attached_count":
		return "%d imagen(es) adjunta(s)"
	case "hint.follow_panel":
		return "Ctrl+N seguir"
	case "hint.unfollow_panel":
		return "Ctrl+N dejar seguir"
	case "queued.count":
		return "%d en cola"
	case "queued.output":
		return "[en cola %d pendientes]\n\n"
	case "interrupt.delivered":
		return "[entregado a la ejecución activa; revisando plan]\n"
	case "status.thinking":
		return "Pensando..."
	case "status.writing":
		return "Escribiendo..."
	case "status.cancelling":
		return "Cancelando..."
	case "status.compacting":
		return "Comprimiendo contexto..."
	case "status.compacted":
		return "[conversación compactada]"
	case "reasoning.effort.status":
		return "Esfuerzo de razónamiento: %s"
	case "reasoning.effort.set":
		return "Esfuerzo de razónamiento estáblecido en %s para esta sesión"
	case "reasoning.effort.unsupported.status":
		return "Esfuerzo de razónamiento no soportado por el proveedor actual"
	case "reasoning.effort.unsupported":
		return "El esfuerzo de razónamiento no es soportado por el proveedor actual"
	case "follow.loading":
		return "  Cargando vista de seguimiento..."
	case "follow.active_agent":
		return "Siguiendo agente %s — entrada pausada. Presione Esc para volver."
	case "follow.active_teammate":
		return "Siguiendo compañero %s — entrada pausada. Presione Esc para volver."
	case "follow.status_running":
		return "ejecutando"
	case "follow.status_done":
		return "completado"
	case "follow.more":
		return "  +%d más"
	case "follow.hint":
		return "  ↑↓←→ cambiar  Esc cerrar"
	case "status.tools_used":
		return "%d herramientas usadas"
	case "tool.done":
		return "completado"
	case "tool.failed":
		return "fallido"
	case "tool.no_output":
		return "sin salida"
	case "tool.output":
		return "salida"
	case "tool.content":
		return "contenido"
	case "tool.match":
		return "coincidencia"
	case "tool.matches":
		return "coincidencias"
	case "tool.entry":
		return "entrada"
	case "tool.result":
		return "resultado"
	case "approval.rejected":
		return "  Rechazado.\n"
	case "approval.allow":
		return "Permitir"
	case "approval.allow_always":
		return "Permitir siempre"
	case "approval.deny":
		return "Denegar"
	case "approval.accept":
		return "Aceptar"
	case "approval.reject":
		return "Rechazar"
	case "exit.confirm":
		return "Presione Ctrl-C de nuevo para salir.\n\n"
	case "cancel.confirm":
		return "Presione Ctrl-C o Esc de nuevo para cancelar el agente en ejecución.\n\n"
	case "interrupted":
		return "[interrumpido]\n\n"
	case "lang.current":
		return "Idioma actual: %s\nUse /lang para elegir interactivamente, o /lang <en|zh-CN> para cambiar directamente.\n%s\n\n"
	case "lang.invalid":
		return "Idioma no soportado: %s\n%s\n\n"
	case "lang.switch":
		return "Idioma cambiado a: %s\n\n"
	case "lang.selection.current":
		return " Actual: %s"
	case "lang.selection.hint":
		return " Tab/j/k mover • Enter confirmar • e/z atajos • Esc cancelar"
	case "lang.first_use.title":
		return "Elija su idioma preferido"
	case "lang.first_use.body":
		return " Seleccione el idioma que desea que ggcode use de ahora en adelante."
	case "lang.first_use.hint":
		return " Tab/j/k mover • Enter confirmar • e/z atajos"
	case "mode.current":
		return "Modo actual: %s\nUso: /mode <supervised|plan|auto|bypass|autopilot>\n  supervised  Preguntar cuándo una herramienta no tiene regla explicita\n  plan        Exploracion de solo lectura; denegar escrituras y comandos\n  auto        Permitir operaciónes seguras, denegar peligrosas\n  bypass      Permitir casí todo; solo parar en acciones críticas\n  autopilot   bypass + continúar cuándo el modelo pregunte\n\n"
	case "mode.persist_failed":
		return "Error al persistir la preferencia de modo: %v"
	case "input.placeholder":
		return "Escriba un mensaje... ($ shell, # chat)"
	case "panel.model_filter.prompt":
		return "Filtro> "
	case "panel.model_filter.placeholder":
		return "escriba para filtrar modelos"
	case "panel.model_list.none":
		return "(ninguno)"
	case "panel.model_list.no_matches":
		return "(sin coincidencias)"
	case "panel.model_list.showing":
		return "mostrando %d/%d modelos"
	case "panel.model_list.hidden_above":
		return "%d arriba"
	case "panel.model_list.hidden_more":
		return "%d más"
	case "panel.provider.vendors":
		return "Proveedores"
	case "panel.provider.endpoints":
		return "Endpoints"
	case "panel.provider.models":
		return "Modelos"
	case "panel.provider.active_draft":
		return "Borrador activo"
	case "panel.provider.protocol":
		return "Protocolo"
	case "panel.provider.protocol.unknown":
		return "(desconocido)"
	case "panel.provider.auth":
		return "Auth"
	case "panel.provider.env_var":
		return "Variable de entorno"
	case "panel.provider.api_key":
		return "API key"
	case "panel.provider.api_key.missing":
		return "faltante"
	case "panel.provider.api_key.configured":
		return "configurado"
	case "panel.provider.auth.connected":
		return "conectado"
	case "panel.provider.auth.not_connected":
		return "no conectado"
	case "panel.provider.base_url":
		return "URL base"
	case "panel.provider.base_url.not_set":
		return "(no estáblecido)"
	case "panel.provider.enterprise_url":
		return "URL empresarial"
	case "panel.provider.tags":
		return "Etiquetas"
	case "panel.provider.model.set_with_m":
		return "(estáblecido con m)"
	case "panel.provider.edit":
		return "Editar"
	case "panel.provider.edit.vendor_api_key":
		return "api key del proveedor"
	case "panel.provider.edit.endpoint_api_key":
		return "api key del endpoint"
	case "panel.provider.edit.endpoint_base_url":
		return "url base del endpoint"
	case "panel.provider.edit.custom_model":
		return "modelo personalizado"
	case "panel.provider.edit.new_endpoint_name":
		return "nombre del nuevo endpoint"
	case "panel.provider.hint.edit":
		return "Enter guardar • Esc cancelar"
	case "panel.provider.hint.main":
		return "Tab/Shift+Tab cambiar foco • j/k mover • / foco filtro • Enter o s aplicar • a vendor key • u endpoint key • b URL base • m modelo personalizado • e agregar endpoint • Esc cerrar"
	case "panel.provider.hint.copilot":
		return "GitHub Copilot: l iniciar sesión • x cerrar sesión • b editar dominio empresarial"
	case "panel.provider.saved":
		return "Guardado."
	case "panel.provider.saved_activated":
		return "Guardado y activado."
	case "panel.provider.login.starting":
		return "Iniciando sesión de GitHub Copilot..."
	case "panel.provider.login.instructions":
		return "Abra %s e ingrese el código %s. Esperando autorización..."
	case "panel.provider.login.copied":
		return "Código de dispositivo copiado al portapapeles."
	case "panel.provider.login.copy_failed":
		return "Error al copiar el código de dispositivo: %s"
	case "panel.provider.login.browser_opened":
		return "Página de verificación abierta en su navegador."
	case "panel.provider.login.browser_failed":
		return "Error al abrir la página de verificación: %s"
	case "panel.provider.login.success":
		return "GitHub Copilot conectado."
	case "panel.provider.login.failed":
		return "Error de inicio de sesión de GitHub Copilot: %s"
	case "panel.provider.logout.success":
		return "GitHub Copilot desconectado."
	case "panel.provider.refreshing_vendor":
		return "Actualizando modelos para %s..."
	case "panel.provider.refresh.save_failed":
		return "Modelos actualizados, pero error al guardar la configuración: %s"
	case "panel.provider.refresh.partial":
		return "Actualizado(s) %d endpoint(s), descubierto(s) %d modelo(s). Algunos endpoints fallaron: %v"
	case "panel.provider.refresh.success":
		return "Actualizado(s) %d endpoint(s), descubierto(s) %d modelo(s)."
	case "panel.provider.refresh.failed":
		return "Error de actualización de modelos: %s"
	case "panel.provider.refresh.none":
		return "Sin endpoints actualizables para este proveedor."
	case "panel.model.models":
		return "Modelos"
	case "panel.model.vendor":
		return "Proveedor"
	case "panel.model.endpoint":
		return "Endpoint"
	case "panel.model.protocol":
		return "Protocolo"
	case "panel.model.source":
		return "Origen"
	case "panel.model.source.builtin":
		return "integrado"
	case "panel.model.source.remote":
		return "remoto"
	case "panel.model.refreshing":
		return "Actualizando últimos modelos..."
	case "panel.model.hint.main":
		return "j/k mover • Enter o s aplicar • w ventana de contexto • o max tokens • r actualizar • / filtrar • Esc cerrar"
	case "panel.model.hint.edit":
		return "Enter guardar • Esc cancelar (0 o vacio = auto, sufijo K/M/G permitido ej. 256k)"
	case "panel.model.context_window":
		return "Ventana de Contexto"
	case "panel.model.max_tokens":
		return "Max Tokens de Salida"
	case "panel.model.edit":
		return "Editar"
	case "panel.model.saved_runtime_inactive":
		return "Configuración guardada, pero el runtime actual sigue inactivo: %s"
	case "panel.model.context_applied":
		return "Aplicado context_window=%d, max_tokens=%d (guardado)"
	case "panel.model.context_cleared":
		return "Restáblecido a autodetección (guardado)"
	case "panel.model.switched":
		return "Modelo cambiado a %s."
	case "panel.model.refresh.save_failed":
		return "Modelos actualizados, pero error al guardar la configuración: %s"
	case "panel.model.refresh.builtin_reason":
		return "Usando modelos integrados: %s"
	case "panel.model.refresh.remote_loaded":
		return "Cargado(s) %d modelo(s) remoto(s)."
	case "panel.model.refresh.builtin_loaded":
		return "Modelos integrados cargados."
	case "command.unknown":
		return "Comando desconocido: %s\n"
	case "command.retry_empty":
		return "No hay envio previo para reintentar."
	case "command.retry_busy":
		return "El agente está ocupado. Espere a que termine la ejecución actual antes de reintentar."
	case "command.edit_empty":
		return "No hay envio previo para editar."
	case "command.edit_busy":
		return "El agente está ocupado. Espere a que termine la ejecución actual antes de editar."
	case "command.edit_ready":
		return "Último envio cargado — edite y presione Enter para enviar."
	case "command.help_hint":
		return "Escriba /help para ver los comandos disponibles\n\n"
	case "command.usage.allow":
		return "Uso: /allow <nombre-herramienta>\n\n"
	case "command.usage.resume":
		return "Uso: /resume <id-sesión>\n\n"
	case "command.usage.export":
		return "Uso: /export <id-sesión>\n\n"
	case "init.resolve_failed":
		return "Error al resolver el objetivo de init: %v\n\n"
	case "init.generate_failed":
		return "Error al generar contenido de GGCODE.md: %v\n\n"
	case "init.collecting":
		return "Recopilando conocimiento del proyecto..."
	case "init.prompt.title":
		return "Inicializar proyecto"
	case "init.prompt.body":
		return "No se encontro GGCODE.md en este proyecto. Crear uno para ayudar al agente a entender las convenciones de su código?"
	case "init.prompt.yes":
		return "Crear"
	case "init.prompt.no":
		return "Omitir"
	case "init.prompt.hint":
		return " y = crear GGCODE.md • n/Esc = omitir"
	case "command.model_switched":
		return "Modelo cambiado a: %s (proveedor: %s)\n\n"
	case "command.model_failed":
		return "Error al cambiar de modelo: %v\n\n"
	case "command.model_current":
		return "Modelo actual: %s (proveedor: %s)\nModelos disponibles: %s\nUse /model para abrir el panel de modelos o /model <nombre-modelo> para cambiar directamente.\n\n"
	case "command.provider_unknown":
		return "Proveedor desconocido: %s (disponibles: %v)\n\n"
	case "command.provider_switched":
		return "Proveedor cambiado a: %s (modelo: %s)\n\n"
	case "command.provider_failed":
		return "Error al actualizar la selección de proveedor: %v\n\n"
	case "command.provider_current":
		return "Proveedor actual: %s (endpoint: %s, modelo: %s)\nProveedores disponibles: %s\nEndpoints disponibles: %s\nUso: /provider [proveedor] [endpoint]\n\n"
	case "command.allow_set":
		return "✓ %s ahora siempre permitido\n\n"
	case "command.custom":
		return "Comando personalizado /%s:\n"
	case "command.mention_error":
		return "Error de mención: %v"
	case "session.list_failed":
		return "Error al listar sesiónes: %v\n\n"
	case "session.untitled":
		return "sin titulo"
	case "session.store_missing":
		return "Almacen de sesiónes no configurado.\n\n"
	case "session.none":
		return "No se encontraron sesiónes.\n\n"
	case "session.list.title":
		return "Sesiónes:\n\n"
	case "session.list.item":
		return "  %d. %s  %s  (%s)\n"
	case "session.list.hint":
		return "\nUse /resume <id> para continúar una sesión\n\n"
	case "session.new":
		return "Nueva sesión: %s\n\n"
	case "session.resume":
		return "Sesión reanudada: %s — %s (%d mensajes)\n\n"
	case "session.resume_failed":
		return "Error al reanudar la sesión %s: %v\n\n"
	case "session.resume_fallback":
		return "Iniciando nueva sesión en su lugar.\n\n"
	case "session.export_failed":
		return "Error al exportar sesión: %v\n\n"
	case "session.write_failed":
		return "Error al escribir archivo: %v\n\n"
	case "session.exported":
		return "Sesión exportada %s a %s\n\n"
	case "checkpoint.disabled":
		return "Puntos de control no habilitados.\n\n"
	case "checkpoint.undo_failed":
		return "Error al deshacer: %v\n\n"
	case "checkpoint.undid":
		return "Deshecho %s en %s (checkpoint %s)\n"
	case "checkpoint.none":
		return "Sin checkpoints.\n\n"
	case "files.disabled":
		return "Puntos de control no habilitados.\n\n"
	case "files.none":
		return "Sin archivos modificados por el agente en esta sesión.\n\n"
	case "files.title":
		return "Archivos modificados por el agente (%d archivos, %d ediciónes):\n\n"
	case "files.item":
		return "  %s  %d ediciónes  último: %s%s\n"
	case "files.hint":
		return "\nUse /undo para revertir la edición más reciente, /checkpoints para detalles.\n\n"
	case "checkpoint.list.title":
		return "Checkpoints (%d):\n\n"
	case "checkpoint.list.item":
		return "  %d. %s  %s  %s  %s\n"
	case "checkpoint.list.hint":
		return "\nUse /undo para revertir el más reciente.\n\n"
	case "memory.auto_unavailable":
		return "Memoria automatica no inicializada.\n\n"
	case "memory.list_failed":
		return "Error al listar memorias: %v\n\n"
	case "memory.none":
		return "Sin memorias automaticas guardadas.\n\n"
	case "memory.auto_title":
		return "Memorias Automaticas:\n"
	case "memory.clear_failed":
		return "Error al limpiar memorias: %v\n\n"
	case "memory.cleared":
		return "Todas las memorias automaticas eliminadas.\n\n"
	case "memory.title":
		return "Memoria:\n"
	case "memory.project":
		return "Memoria del Proyecto:\n"
	case "memory.project_none":
		return "  Sin archivos de memoria de proyecto cargados.\n"
	case "memory.auto":
		return "Memoria Automatica:\n"
	case "memory.auto_none":
		return "  Sin memorias automaticas cargadas.\n"
	case "memory.usage":
		return "\nUso: /memory [list|clear]\n\n"
	case "compact.unavailable":
		return "Gestor de contexto no disponible.\n\n"
	case "compact.failed":
		return "Error de compactación: %v\n\n"
	case "compact.done":
		return "Historial de conversación compactado.\n\n"
	case "compact.done_with_stats":
		return "Historial de conversación compactado (%d → %d tokens).\n\n"
	case "todo.cleared":
		return "Lista de táreas eliminada.\n\n"
	case "todo.clear_failed":
		return "Error al limpiar táreas: %v\n\n"
	case "todo.none":
		return "No se encontro lista de táreas. Use la herramienta todo_write para crear una.\n\n"
	case "todo.read_failed":
		return "Error al leer táreas: %v\n\n"
	case "todo.parse_failed":
		return "Error al analizar táreas: %v\n\n"
	case "todo.title":
		return "Lista de táreas:\n%s\n\n"
	case "bug.title":
		return "=== Díagnósticos de Reporte de Errores ===\n\n"
	case "bug.versión":
		return "Versión: %s\n"
	case "bug.os":
		return "SO: %s %s\n"
	case "bug.go":
		return "Go: %s\n"
	case "bug.provider":
		return "Proveedor: %s\n"
	case "bug.model":
		return "Modelo: %s\n"
	case "bug.session":
		return "Sesión: %s (%d mensajes)\n"
	case "bug.mcp":
		return "Servidores MCP: %d\n"
	case "bug.last_error":
		return "Último error: %s\n"
	case "bug.hint":
		return "\nIncluya esta información al reportar un error.\n\n"
	case "config.usage":
		return "Uso: /config set <clave> <valor>\n\nClavess: model, vendor, endpoint, language, apikey [--vendor]\n\nEndpoints: /config add-endpoint <nombre> <url_base> [--protocol openai] [--apikey sk-xxx]\n          /config remove-endpoint <nombre>\n\n"
	case "config.not_loaded":
		return "Configuración no cargada.\n\n"
	case "config.model_set":
		return "Config: modelo = %s\n\n"
	case "config.provider_set":
		return "Config: proveedor = %s\n\n"
	case "config.language_set":
		return "Config: idioma = %s\n\n"
	case "config.unknown_key":
		return "Clave de config desconocida: %s\nSoportadas: model, provider, language\n\n"
	case "config.title":
		return "Configuración Actual:\n"
	case "status.title":
		return "Estado:\n"
	case "panel.update":
		return "Actualización"
	case "label.versión":
		return "Versión"
	case "label.latest":
		return "Última"
	case "update.sidebar_hint":
		return "Nueva versión disponible. Ejecute /update."
	case "update.up_to_date":
		return "Esta actualizado."
	case "update.available":
		return "actualización disponible: %s"
	case "update.current":
		return "actual: %s (última: %s)"
	case "update.unknown":
		return "sin verificar aún"
	case "update.check_failed":
		return "verificación fallida: %s"
	case "update.unavailable":
		return "Actualización no disponible en esta sesión.\n\n"
	case "update.preparing":
		return "Preparando actualización"
	case "update.failed":
		return "Actualización fallida: %v\n\n"
	case "update.restárt_failed":
		return "Actualización preparada, pero error al reiniciar: %v\n\n"
	case "update.pm_hint.brew":
		return "Actualización instalada. Nota: ggcode fue instalado via Homebrew.\nEjecute `brew upgrade ggcode` para mantener Homebrew sincronizado.\n\n"
	case "update.pm_hint.scoop":
		return "Actualización instalada. Nota: ggcode fue instalado via Scoop.\nEjecute `scoop update ggcode` para mantener Scoop sincronizado.\n\n"
	case "update.pm_hint.winget":
		return "Actualización instalada. Nota: ggcode fue instalado via winget.\nEjecute `winget upgrade ggcode` para mantener winget sincronizado.\n\n"
	case "update.pm_hint.snap":
		return "Actualización instalada. Nota: ggcode fue instalado via Snap.\nEjecute `sudo snap refresh ggcode` para mantener Snap sincronizado.\n\n"
	case "update.other_installs":
		return "Otras instalaciones de ggcode detectadas en este sistema:\n%s\nSi un ggcode diferente aparece primero en PATH, considere actualizarlo también o ajustar el orden de PATH.\n\n"
	case "update.dual_scope":
		return "Advertencia: Se encontraron instalaciones de ggcode de usuario y de sistema:\n  Usuario: %s\n  Sistema: %s\nEsto puede causar conflictos de PATH. Considere desinstalar una desde Configuración > Aplicaciónes.\n\n"
	case "plugins.unavailable":
		return "Gestor de plugins no disponible.\n\n"
	case "plugins.none":
		return "Sin plugins cargados.\n\n"
	case "plugins.title":
		return "Plugins:\n"
	case "mcp.none":
		return "Sin servidores MCP configurados.\n\n"
	case "mcp.title":
		return "Servidores MCP:\n"
	case "mcp.active_tools":
		return "Herramientas activas"
	case "mcp.more":
		return "… %d más • /mcp"
	case "image.usage":
		return "Uso: /image <ruta/al/archivo.png> o /image paste\n"
	case "image.formats":
		return "Formatos soportados: PNG, JPEG, GIF, WebP (max 20MB)\n\n"
	case "image.attached":
		return "Imagen adjunta: %s\n"
	case "image.attached_hint":
		return "Envie un mensaje para incluir la imagen, o /image para adjuntar otra.\n\n"
	case "image.clipboard_failed":
		return "No se pudo pegar una imagen del portapapeles: %v"
	case "image.clipboard_no_image_windows":
		return "Sin imagen en el portapapeles. En Windows, Ctrl+V a menudo es interceptado por la terminal. Pruebe Ctrl+Shift+V o /image paste."
	case "agents.unavailable":
		return "Gestor de sub-agentes no configurado.\n\n"
	case "agents.none":
		return "Sin sub-agentes creados aún.\nUso: el LLM puede usar la herramienta spawn_agent para crear sub-agentes.\n\n"
	case "agents.title":
		return "%d sub-agente(s):\n"
	case "agents.item":
		return "  %s [%s]%s - %s\n"
	case "agents.hint":
		return "\nUse /agent <id> para detalles, /agent cancel <id> para cancelar.\n\n"
	case "agent.usage":
		return "Uso: /agent <id> o /agent cancel <id>\n\n"
	case "agent.cancelled":
		return "Sub-agente %s cancelado\n\n"
	case "agent.cancel_failed":
		return "No se pudo cancelar %s (no encontrado o no en ejecución)\n\n"
	case "agent.not_found":
		return "Sub-agente %s no encontrado\n\n"
	case "agent.title":
		return "Agente: %s\nEstado: %s\ntarea: %s\n"
	case "agent.result":
		return "Resultado: %s\n"
	case "agent.error":
		return "Error: %v\n"
	case "slash.help":
		return "Mostrar mensaje de ayuda"
	case "slash.sessions":
		return "Listar sesiónes guardadas"
	case "slash.resume":
		return "Reanudar una sesión previa"
	case "slash.export":
		return "Exportar sesión a markdown"
	case "slash.model":
		return "Cambiar modelo"
	case "slash.provider":
		return "Abrir gestor de proveedores"
	case "slash.clear":
		return "Limpiar conversación"
	case "slash.mcp":
		return "Mostrar servidores MCP"
	case "slash.memory":
		return "Gestionar memoria"
	case "slash.undo":
		return "Deshacer última edición de archivo"
	case "slash.files":
		return "Mostrar archivos modificados por el agente"
	case "slash.checkpoints":
		return "Listar checkpoints"
	case "slash.allow":
		return "Permitir siempre una herramienta"
	case "slash.plugins":
		return "Listar plugins cargados"
	case "slash.image":
		return "Adjuntar una imagen"
	case "slash.init":
		return "Generar GGCODE.md del proyecto"
	case "slash.harness":
		return "Ejecutar comandos de workflow harness"
	case "slash.lang":
		return "Cambiar idioma de interfaz"
	case "slash.skills":
		return "Explorar habilidades disponibles"
	case "slash.exit":
		return "Salir de ggcode"
	case "slash.compact":
		return "Comprimir historial de conversación"
	case "slash.todo":
		return "Ver/gestionar lista de táreas"
	case "slash.bug":
		return "Reportar un error"
	case "slash.config":
		return "Ver/modificar configuración"
	case "slash.qq":
		return "Gestionar vinculación de canal QQ"
	case "slash.telegram":
		return "Gestionar vinculación de canal Telegram"
	case "slash.pc":
		return "Gestionar vinculación de canal PC"
	case "slash.discord":
		return "Gestionar vinculación de canal Discord"
	case "slash.feishu":
		return "Gestionar vinculación de canal Feishu"
	case "slash.slack":
		return "Gestionar vinculación de canal Slack"
	case "slash.dingtalk":
		return "Gestionar vinculación de canal DingTalk"
	case "slash.wechat":
		return "Gestionar vinculación de canal WeChat"
	case "slash.wecom":
		return "Gestionar vinculación de canal WeCom (WeChat Empresarial)"
	case "slash.mattermost":
		return "Gestionar vinculación de canal Mattermost"
	case "slash.matrix":
		return "Gestionar vinculación de canal Matrix"
	case "slash.signal":
		return "Gestionar vinculación de canal Signal"
	case "slash.irc":
		return "Gestionar vinculación de canal IRC"
	case "slash.nostr":
		return "Gestionar vinculación de canal Nostr"
	case "slash.twitch":
		return "Gestionar vinculación de canal Twitch"
	case "slash.whatsapp":
		return "Gestionar vinculación de canal WhatsApp"
	case "slash.impersonate":
		return "Suplantar una herramienta CLI para la visualización del prompt de shell"
	case "slash.knight":
		return "Gestionar agente autonomo en segúndo plano"
	case "slash.stream":
		return "Configurar modo de salida en streaming"
	case "slash.diff":
		return "Mostrar git diff en el chat (soporta --cached, <archivo>, --stat)"
	case "slash.hooks":
		return "Mostrar hooks configurados (todos los eventos, tipos, patrones)"
	case "slash.cost":
		return "Mostrar uso de tokens y costo estimado de la sesión"
	case "slash.review":
		return "Revisión de código con AI de cambios actuales (bugs, seguridad, concurrencia)"
	case "slash.copy":
		return "Copiar última respuesta del asistente al portapapeles"
	case "slash.context":
		return "Mostrar desglose de uso de ventana de contexto (tokens, mensajes, capacidad)"
	case "slash.im":
		return "Abrir panel unificado de canales IM"
	case "panel.qq.directory":
		return "Directorio"
	case "panel.qq.runtime":
		return "Runtime"
	case "panel.qq.bots":
		return "Bots QQ"
	case "panel.qq.created":
		return "Creados: %d"
	case "panel.qq.bound":
		return "Vinculados: %d"
	case "panel.qq.available":
		return "Disponibles: %d"
	case "panel.qq.current_binding":
		return "Vinculación Actual"
	case "panel.qq.none":
		return "(ninguno)"
	case "panel.qq.default":
		return "(predeterminado)"
	case "panel.qq.adapter":
		return "Adaptador: %s"
	case "panel.qq.target":
		return "Objetivo: %s"
	case "panel.qq.channel":
		return "Canal: %s"
	case "panel.qq.bot_list":
		return "Lista de Bots QQ"
	case "panel.qq.no_bots":
		return "Sin bots QQ configurados."
	case "panel.qq.entry.available":
		return "Disponible"
	case "panel.qq.entry.bound":
		return "Vinculado"
	case "panel.qq.entry.active":
		return "Activo"
	case "panel.qq.entry.bound_other":
		return "Vinculado: %s"
	case "panel.qq.entry.muted":
		return "Silenciado"
	case "panel.qq.details":
		return "Detalles"
	case "panel.qq.status":
		return "Estado: %s"
	case "panel.qq.transport":
		return "Transporte: %s"
	case "panel.qq.bound_directory":
		return "Directorio Vinculado: %s"
	case "panel.qq.current_directory_target":
		return "Objetivo de Directorio Actual: %s"
	case "panel.qq.current_directory_channel":
		return "Canal de Directorio Actual: %s"
	case "panel.qq.waiting_for_pairing":
		return "(esperando vinculación)"
	case "panel.qq.last_error":
		return "Último Error: %s"
	case "panel.qq.occupied_by":
		return "Ocupado por: %s"
	case "panel.qq.create":
		return "Crear"
	case "panel.qq.bot_input":
		return "Bot QQ: %s"
	case "panel.qq.create_format":
		return "Formato: <bot-id> <appid> <appsecret>"
	case "panel.qq.create_example":
		return "Ejemplo: qq-main 123456 valor-secreto"
	case "panel.qq.create_hint":
		return "Enter crear bot • Esc cancelar"
	case "panel.qq.actions_hint":
		return "j/k mover • Enter o b vincular bot • c vincular canal • x desvincular canal • u desvincular bot • i crear bot • Esc cerrar"
	case "panel.qq.bind_channel":
		return "Vincular Canal"
	case "panel.qq.scan_hint":
		return "Escanee el código QR, agregue el bot, luego envie un mensaje para iniciar la vinculación."
	case "panel.qq.qr_code":
		return "Código QR:"
	case "panel.qq.share_link":
		return "Enlace de Compartir:"
	case "panel.qq.message.no_bot":
		return "Sin bot QQ disponible."
	case "panel.qq.message.bound_success":
		return "Bot QQ vinculado al workspace actual. Use c para generar el código QR de vinculación de canal."
	case "panel.qq.message.share_generated":
		return "Enlace de compartir QQ generado. Escanee el código QR, agregue el bot, luego envie un mensaje para iniciar la vinculación."
	case "panel.qq.message.unbound":
		return "Canal QQ desvinculado."
	case "panel.qq.message.cleared":
		return "Autorización de canal QQ eliminada para el workspace actual."
	case "panel.qq.message.added_bot":
		return "Bot QQ agregado %s."
	case "panel.qq.error.config_unavailable":
		return "configuración no disponible"
	case "panel.qq.error.config_format":
		return "La config del bot QQ debe ser: <bot-id> <appid> <appsecret>"
	case "panel.qq.error.adapter_required":
		return "Se requiere el nombre del adaptador QQ"
	case "panel.qq.error.not_configured":
		return "El bot QQ %q no está configurado"
	case "panel.qq.error.disabled":
		return "El bot QQ %q está deshabilitado"
	case "panel.qq.error.not_qq_adapter":
		return "el adaptador %q no es un bot QQ"
	case "panel.qq.error.not_online":
		return "El bot QQ %q no está en línea"
	case "panel.qq.error.not_online_detail":
		return "El bot QQ %q no está en línea: %s"
	case "panel.qq.runtime.available":
		return "disponible"
	case "panel.qq.runtime.disabled":
		return "deshabilitado (estáblezca im.enabled: true y reinicie ggcode)"
	case "panel.qq.runtime.not_started":
		return "no iniciado (reinicie ggcode para inicializar el runtime de IM)"
	case "panel.qq.status.not_started":
		return "no iniciado"
	case "panel.qq.status.unknown":
		return "desconocido"
	case "slash.status":
		return "Mostrar estado actual"
	case "slash.update":
		return "Actualizar ggcode"
	case "slash.cron":
		return "Gestionar táreas cron programadas (listar, pausar, reanudar, crear)"
	case "slash.branch":
		return "Bifurcar sesión actual a una nueva sesión (fork de conversación)"
	case "slash.chat":
		return "Abrir panel de LAN Chat"
	case "slash.edit":
		return "Editar y reenviar su último mensaje"
	case "slash.inspector":
		return "Alternar panel del inspector"
	case "slash.mode":
		return "Mostrar o cambiar modo de permiso"
	case "slash.nick":
		return "Establecer su apodo de LAN Chat"
	case "slash.reflect":
		return "Ejecutar autorreflexión en la sesión actual"
	case "slash.regenerate":
		return "Regenerar última respuesta de AI (descartar y reejecutar)"
	case "slash.restárt":
		return "Reiniciar proceso de ggcode"
	case "slash.retry":
		return "Reintentar la última ejecución fallida del agente"
	case "slash.rules":
		return "Mostrar reglas ratchet aprendidas"
	case "slash.share":
		return "Compartir sesión via tunnel (relay móvil)"
	case "slash.stats":
		return "Mostrar estádísticas de sesión (tokens, iteraciones, herramientas)"
	case "slash.tmux":
		return "Abrir menu de gestion de paneles tmux"
	case "slash.tunnel":
		return "Alternar conexión tunnel para relay móvil"
	case "slash.unshare":
		return "Dejar de compartir sesión via tunnel"
	case "regenerate.busy":
		return "No se puede regenerar mientras el agente está en ejecución. Presione Ctrl+C para cancelar primero."
	case "regenerate.no_agent":
		return "Agente no inicializado."
	case "regenerate.no_context":
		return "Gestor de contexto no disponible."
	case "regenerate.no_response":
		return "Sin respuesta del asistente para regenerar."
	case "branch.busy":
		return "No se puede bifurcar mientras el agente está en ejecución. Presione Ctrl+C para cancelar primero."
	case "branch.no_session":
		return "Sin sesión activa para bifurcar."
	case "branch.empty":
		return "La sesión no tiene mensajes para bifurcar."
	case "branch.save_failed":
		return "Error al crear sesión bifurcada: %v"
	case "branch.success":
		return "Bifurcado a nueva sesión %s (desde: %s). La sesión original se conserva."
	case "help.text":
		return `Comandos disponibles:

Sesión e Historial:
  /help, /?          Mostrar este mensaje de ayuda
  /sessions          Listar todas las sesiónes guardadas
  /resume <id>       Reanudar una sesión previa
  /export <id>       Exportar sesión a archivo markdown
  /clear             Limpiar historial de conversación
  /compact           Comprimir historial de conversación (manual)
  /undo              Deshacer la última edición de archivo (rollback de checkpoint)
  /checkpoints       Listar todos los checkpoints de edición
  /regenerate        Descartar última respuesta y regenerar (alias: /regen)
  /branch            Bifurcar conversación actual a nueva sesión (alias: /fork)

Modelo y Proveedor:
  /model [nombre]    Abrir panel de modelos o cambiar directamente
  /provider [vendor] Abrir gestor de proveedores
  /mode <modo>       Establecer modo del agente (supervised|plan|auto|bypass|autopilot)

Desarrollo:
  /diff [opts]       Mostrar git diff en el chat (--cached, --stat, <archivo>)
  /review [opts]     Revisión de código con AI de cambios actuales (--cached, --staged)
  /copy              Copiar última respuesta del asistente al portapapeles
  /cost              Mostrar uso de tokens y costo estimado de la sesión
  /context           Mostrar desglose de uso de ventana de contexto
  /hooks             Mostrar hooks configurados
  /init              Generar GGCODE.md del proyecto actual
  /harness ...       Ejecutar comandos de harness
  /todo              Ver lista de táreas
  /todo clear        Limpiar lista de táreas

Integraciones:
  /im                Abrir panel unificado de canales IM
  /mcp               Mostrar servidores MCP y herramientas conectados
  /plugins           Listar plugins cargados y sus herramientas
  /skills            Explorar habilidades disponibles
  /memory            Mostrar archivos de memoria cargados
  /agents            Listar sub-agentes
  /cron <sub>        Gestionar táreas programadas (list|get|pause|resume|pauseall|resumeall)

Sistema:
  /lang [código]     Elegir o cambiar idioma de interfaz
  /config            Mostrar configuración actual
  /config set <k> <v> Establecer un valor de configuración
  /status            Mostrar estado actual
  /update            Actualizar ggcode a la última versión
  /restárt           Reiniciar ggcode (carga el último binario)
  /bug               Reportar un error con díagnósticos
  /exit, /quit       Salir de ggcode

Atajos de teclado:
  Tab                Ciclar autocompletar o opciones de aprobación
  Shift+Tab          Ciclar autocompletar inverso, o alternar modo de permiso
  Ctrl+R             Alternar barra lateral
  Ctrl+N/P           Nueva/sesión anterior
  Ctrl+T             Abrir tunnel (compartir móvil)
  Enter              Enviar mensaje / aplicar selección actual
  Esc                Cancelar autocompletar / salir del modo shell inactivo
  Up/Down            Navegar historial de comandos (o autocompletar)
  PgUp/PgDn          Desplazar salida de conversación
  Ctrl+C             Cancelar actividad actual, sino limpiar entrada y presionar de nuevo para salir
  Ctrl+D             Salir inmedíatamente
  Ctrl+A / Ctrl+E    Mover cursor al inicio / fin de línea
  Ctrl+K             Eliminar del cursor al final de línea
  Ctrl+U             Eliminar del inicio de línea al cursor
  Ctrl+W             Eliminar palabra antes del cursor
  Ctrl+Backspace     Quitar última imagen adjunta
  Shift+Enter        Insertar nueva línea (Ctrl+J o Alt+Enter en tmux)
  $ / !              Entrar al modo shell
  #                  Entrar al modo envio rápido de LAN Chat

Raton:
  Option+drag / Shift+drag  Selecciónar texto para copiar (omite captura de raton de la app)
  Rueda del raton           Desplazar salida de conversación`
	case "command.harness_usage":
		return "Uso: /harness <init|check|queue|tasks|run|rerun|run-queued|monitor|contexts|inbox|review|promote|release|gc|doctor> ... (release soporta rollouts|advance|pause|resume|abort|approve|reject)"
	case "command.harness_queue_usage":
		return "Uso: /harness queue <objetivo>"
	case "command.harness_run_usage":
		return "Uso: /harness run <objetivo>"
	case "command.harness_rerun_usage":
		return "Uso: /harness rerun <task-id>"
	case "command.skill_agent_only":
		return "La habilidad %s solo puede ser invocada por el agente."
	case "command.harness_owner_promoted":
		return "Promovida(s) %d tárea(s) de harness para el propietario %s."
	case "command.harness_review_approved":
		return "Aprobada tárea de harness %s."
	case "command.harness_review_rejected":
		return "Rechazada tárea de harness %s."
	case "command.harness_promoted_many":
		return "Promovida(s) %d tárea(s) de harness."
	case "command.harness_promoted_one":
		return "Promovida tárea de harness %s."
	case "command.harness_task_queued_detail":
		return "Encolada tárea de harness %s.\n- objetivo: %s"
	case "command.harness_tasks_empty":
		return "Sin táreas de harness registradas."
	case "command.harness_run_start":
		return "Iniciando ejecución de harness rastreada...\nUse /harness monitor o las vistas de tareas/Monitor para estado en vivo."
	case "command.harness_rerun_start":
		return "Iniciando reejecución de harness rastreada...\nUse /harness monitor o las vistas de tareas/Monitor para estado en vivo."
	case "command.harness_rerun_invalid_status":
		return "La tárea de harness %s está %s; solo las táreas fallidas pueden reejecutarse."
	case "command.harness_status_starting_run":
		return "Iniciando ejecución de harness..."
	case "command.harness_status_starting_rerun":
		return "Iniciando reejecución de harness..."
	case "command.harness_spinner_running":
		return "Ejecutando harness"
	case "command.harness_cancelled":
		return "Ejecución de harness cancelada."
	case "tunnel.stopped":
		return "Tunnel detenido."
	case "tunnel.not_active":
		return "Sin sesión de compartir activa."
	case "tunnel.mobile_connected":
		return "Cliente móvil conectado."
	case "config.save_scope_global":
		return "Guardar en → Global"
	case "config.save_scope_instance":
		return "Guardar en → Instancia"
	case "config.save_scope_instance_new":
		return "Guardar en → Instancia (se creara una nueva config al guardar)"
	case "config.instance_unavailable":
		return "Config de instancia no disponible para este workspace"
	case "config.scope_instance":
		return "Instancia"
	case "config.scope_global":
		return "Global"
	case "config.save_target_new_hint":
		return " (se creara nueva config al guardar)"
	case "config.save_target_line":
		return " Guardar en: %s%s  [Ctrl+T alternar]"
	case "shell.empty":
		return "El comando de shell está vacío."
	case "lanchat.unavailable":
		return "LAN Chat no está disponible."
	case "reflect.no_agent":
		return "Agente no inicializado."
	case "reflect.no_workdir":
		return "Directorio de trabajo no estáblecido."
	case "reflect.no_memory":
		return "Memoria de proyecto no disponible para este directorio."
	case "reflect.load_failed":
		return "Error al cargar insights: %v"
	case "reflect.empty":
		return "Sin insights de ejecución aún. Los insights se generan automaticamente después de cada ejecución del agente con 3+ llamadas a herramientas o ediciónes de archivos."
	case "reflect.title":
		return "## Insights de Ejecución Acumulados\n\n"
	case "reflect.memory_location":
		return "Ubicación de memoria: %s\n"
	case "knight.unavailable":
		return "Knight no está disponible"
	case "pairing.rejected":
		return "La solicitud de vinculación actual ha sido rechazada. Por favor reinicie para continúar."
	case "pairing.blacklisted":
		return "Este canal ha sido bloqueado debido a multiples rechazos."
	default:
		return enCatalog(key)
	}
}
