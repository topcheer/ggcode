package tui

// ptCatalog returns the Portuguese translation for the given key.
// Keys not yet translated fall through to enCatalog.
func ptCatalog(key string) string {
	switch key {
	// ── Common UI labels ──────────────────────────────────────────
	case "label.mode":
		return "Modo"
	case "label.model":
		return "Modelo"
	case "label.vendor":
		return "Fornecedor"
	case "label.endpoint":
		return "Endpoint"
	case "label.session":
		return "Sessão"
	case "label.sessions":
		return "Sessões"
	case "label.workspace":
		return "Workspace"
	case "label.language":
		return "Idioma"
	case "label.tools":
		return "Ferramentas"
	case "label.context":
		return "Contexto"
	case "label.tokens":
		return "Tokens"
	case "label.cost":
		return "Custo"
	case "label.permissions":
		return "Permissões"
	case "label.search":
		return "Buscar"
	case "label.filter":
		return "Filtrar"
	case "label.confirm":
		return "Confirmar"
	case "label.cancel":
		return "Cancelar"
	case "label.close":
		return "Fechar"
	case "label.save":
		return "Salvar"
	case "label.delete":
		return "Excluir"
	case "label.rename":
		return "Renomear"
	case "label.new":
		return "Novo"
	case "label.open":
		return "Abrir"
	case "label.copy":
		return "Copiar"
	case "label.yes":
		return "Sim"
	case "label.no":
		return "Não"
	case "label.none":
		return "Nenhum"
	case "label.all":
		return "Todos"
	case "label.loading":
		return "Carregando..."
	case "label.ready":
		return "Pronto"
	case "label.idle":
		return "Inativo"
	case "label.busy":
		return "Ocupado"
	case "label.error":
		return "Erro"
	case "label.warning":
		return "Aviso"
	case "label.info":
		return "Informação"
	case "label.success":
		return "Sucesso"

	// ── Permission modes ──────────────────────────────────────────
	case "mode.supervised":
		return "Supervisionado"
	case "mode.plan":
		return "Plano"
	case "mode.auto":
		return "Auto"
	case "mode.bypass":
		return "Bypass"
	case "mode.autopilot":
		return "Autopilot"

	// ── Status bar ────────────────────────────────────────────────
	case "status.agent_idle":
		return "Inativo"
	case "status.agent_thinking":
		return "Pensando..."
	case "status.agent_running":
		return "Executando..."
	case "status.agent_working":
		return "Trabalhando..."
	case "status.context.fill":
		return "Contexto: %s/%s (%d%%)"
	case "status.tokens.used":
		return "Tokens: %s"

	// ── Chat input ────────────────────────────────────────────────
	case "input.placeholder":
		return "Digite sua mensagem... (Enter para enviar, Shift+Enter para nova linha)"
	case "input.hint":
		return "Enter para enviar • Shift+Enter para nova linha"
	case "input.empty_warning":
		return "A entrada está vazia"
	case "input.cancel":
		return "Cancelar"

	// ── Welcome screen ────────────────────────────────────────────
	case "welcome.title":
		return "Bem-vindo ao ggcode"
	case "welcome.subtitle":
		return "Seu assistente de IA para codificação"
	case "welcome.prompt.explain":
		return "Explicar o código selecionado"
	case "welcome.prompt.review":
		return "Revisar alterações recentes"
	case "welcome.prompt.test":
		return "Escrever testes para código"
	case "welcome.prompt.debug":
		return "Depurar um problema"

	// ── Slash command descriptions ────────────────────────────────
	case "slash.help":
		return "Mostrar esta mensagem de ajuda"
	case "slash.help_short":
		return "Ajuda"
	case "slash.sessions":
		return "Listar todas as sessões salvas"
	case "slash.resume":
		return "Retomar uma sessão anterior"
	case "slash.export":
		return "Exportar sessão para arquivo markdown"
	case "slash.clear":
		return "Limpar histórico da conversa"
	case "slash.compact":
		return "Compactar histórico da conversa (manual)"
	case "slash.undo":
		return "Desfazer a última edição de arquivo (reverter checkpoint)"
	case "slash.checkpoints":
		return "Listar todos os checkpoints de edição de arquivo"
	case "slash.model":
		return "Abrir painel de modelo ou alternar diretamente"
	case "slash.provider":
		return "Abrir gerenciador de provedor"
	case "slash.mode":
		return "Definir modo do agente (supervised|plan|auto|bypass|autopilot)"
	case "slash.lang":
		return "Escolher ou alternar idioma da interface"
	case "slash.diff":
		return "Mostrar git diff no chat"
	case "slash.review":
		return "Revisão de código IA das alterações atuais"
	case "slash.copy":
		return "Copiar última resposta do assistente para a área de transferência"
	case "slash.cost":
		return "Mostrar uso de tokens da sessão e custo estimado"
	case "slash.context":
		return "Mostrar detalhamento de uso da janela de contexto"
	case "slash.init":
		return "Gerar GGCODE.md a partir do projeto atual"
	case "slash.im":
		return "Abrir painel unificado de canais IM"
	case "slash.mcp":
		return "Mostrar servidores MCP conectados e ferramentas"
	case "slash.plugins":
		return "Listar plugins carregados e suas ferramentas"
	case "slash.skills":
		return "Navegar pelas skills disponíveis"
	case "slash.memory":
		return "Mostrar arquivos de memória carregados"
	case "slash.agents":
		return "Listar sub-agentes"
	case "slash.cron":
		return "Gerenciar tarefas agendadas"
	case "slash.hooks":
		return "Mostrar hooks configurados"
	case "slash.todo":
		return "Ver lista de tarefas"
	case "slash.stats":
		return "Mostrar estatísticas da sessão (tokens, iterações, ferramentas)"
	case "slash.perf":
		return "Mostrar estatísticas de otimização de desempenho"
	case "slash.doctor":
		return "Executar diagnósticos de saúde do sistema"
	case "slash.quit":
		return "Sair do ggcode"
	case "slash.exit":
		return "Sair do ggcode"
	case "slash.share":
		return "Compartilhar sessão via túnel (relay móvel)"
	case "slash.unshare":
		return "Parar de compartilhar sessão via túnel"
	case "slash.tunnel":
		return "Alternar conexão de túnel para relay móvel"
	case "slash.tmux":
		return "Abrir menu de gerenciamento de painéis tmux"
	case "slash.regenerate":
		return "Regenerar última resposta da IA (descartar e re-executar)"
	case "slash.restart":
		return "Reiniciar processo do ggcode"
	case "slash.retry":
		return "Repetir a última execução do agente que falhou"
	case "slash.rules":
		return "Mostrar regras ratchet aprendidas"

	// ── Session messages ──────────────────────────────────────────
	case "session.untitled":
		return "sem título"
	case "session.none":
		return "Nenhuma sessão encontrada."
	case "session.list.title":
		return "Sessões:"
	case "session.list.hint":
		return "\nUse /resume <id> para continuar uma sessão"
	case "session.new":
		return "Nova sessão: %s"
	case "session.resume":
		return "Sessão retomada: %s — %s (%d mensagens)"
	case "session.resume_failed":
		return "Falha ao retomar sessão %s: %v"
	case "session.exported":
		return "Sessão %s exportada para %s"

	// ── Common messages ───────────────────────────────────────────
	case "command.unknown":
		return "Comando desconhecido: %s"
	case "command.help_hint":
		return "Digite /help para comandos disponíveis"
	case "command.model_switched":
		return "Modelo alterado para: %s (fornecedor: %s)"
	case "command.model_current":
		return "Modelo atual: %s (fornecedor: %s)"
	case "command.allow_set":
		return "✓ %s agora é sempre permitido"

	// ── Mode cycling ──────────────────────────────────────────────
	case "mode.cycled":
		return "Modo: %s"
	case "mode.saved":
		return " (salvo)"

	// ── Doctor ────────────────────────────────────────────────────
	case "doctor.title":
		return "Diagnóstico de Saúde do ggcode"
	case "doctor.ok":
		return "✓"
	case "doctor.fail":
		return "✗"
	case "doctor.check.api_key":
		return "Chave de API"
	case "doctor.check.vendor":
		return "Fornecedor/Endpoint"
	case "doctor.check.model":
		return "Modelo"
	case "doctor.check.mcp":
		return "Servidores MCP"
	case "doctor.check.config":
		return "Arquivo de configuração"
	case "doctor.check.git":
		return "Repositório Git"

	// ── Error messages ────────────────────────────────────────────
	case "error.agent_not_initialized":
		return "Agente não inicializado."
	case "error.permission_denied":
		return "Permissão negada: %s"
	case "error.session_not_found":
		return "Sessão não encontrada: %s"
	case "error.workspace_not_git":
		return "O workspace atual não é um repositório Git."

	// ── Thinking/reasoning ────────────────────────────────────────
	case "reasoning.label":
		return "Raciocínio"
	case "reasoning.thinking":
		return "Pensando..."

	// ── Checkpoint ────────────────────────────────────────────────
	case "checkpoint.disabled":
		return "Checkpoint não habilitado."
	case "checkpoint.none":
		return "Nenhum checkpoint."
	case "checkpoint.undid":
		return "Desfeito %s em %s (checkpoint %s)"

	// ── Files ─────────────────────────────────────────────────────
	case "files.none":
		return "Nenhum arquivo modificado pelo agente nesta sessão."
	case "files.title":
		return "Arquivos modificados pelo agente (%d arquivos, %d edições):"

	// ── Memory ────────────────────────────────────────────────────
	case "memory.none":
		return "Nenhuma memória automática salva."
	case "memory.title":
		return "Memória:"
	case "memory.auto_title":
		return "Memórias Automáticas:"
	case "memory.cleared":
		return "Todas as memórias automáticas foram limpas."

	// ── Regenerate ────────────────────────────────────────────────
	case "regenerate.busy":
		return "Não é possível regenerar enquanto o agente está em execução. Pressione Ctrl+C para cancelar primeiro."
	case "regenerate.no_agent":
		return "Agente não inicializado."
	case "regenerate.no_response":
		return "Nenhuma resposta do assistente para regenerar."

	// ── Branch ────────────────────────────────────────────────────
	case "branch.busy":
		return "Não é possível ramificar enquanto o agente está em execução. Pressione Ctrl+C para cancelar primeiro."
	case "branch.no_session":
		return "Nenhuma sessão ativa para ramificar."
	case "branch.empty":
		return "A sessão não tem mensagens para ramificar."
	case "branch.success":
		return "Ramificada para nova sessão %s (de: %s). A sessão original foi preservada."

	// ── Init ──────────────────────────────────────────────────────
	case "init.collecting":
		return "Coletando conhecimento do projeto..."
	case "init.prompt.title":
		return "Inicializar projeto"
	case "init.prompt.body":
		return "Nenhum GGCODE.md encontrado neste projeto. Criar um para ajudar o agente a entender as convenções do seu código?"
	case "init.prompt.yes":
		return "Criar"
	case "init.prompt.no":
		return "Pular"
	case "init.prompt.hint":
		return " y = criar GGCODE.md • n/Esc = pular"

	// ── Activity ──────────────────────────────────────────────────
	case "activity.idle":
		return "inativo"

	// ── Agent ─────────────────────────────────────────────────────
	case "agent.cancel_failed":
		return "Não foi possível cancelar %s (não encontrado ou não em execução)\n\n"
	case "agent.cancelled":
		return "Sub-agente %s cancelado\n\n"
	case "agent.error":
		return "Erro: %v\n"
	case "agent.not_found":
		return "Sub-agente %s não encontrado\n\n"
	case "agent.result":
		return "Resultado: %s\n"
	case "agent.title":
		return "Agente: %s\nStatus: %s\nTarefa: %s\n"
	case "agent.usage":
		return "Uso: /agent <id> ou /agent cancel <id>\n\n"
	case "agents.hint":
		return "\nUse /agent <id> para detalhes, /agent cancel <id> para cancelar.\n\n"
	case "agents.idle":
		return "inativo"
	case "agents.item":
		return "  %s [%s]%s - %s\n"
	case "agents.none":
		return "Nenhum sub-agente criado ainda.\nUso: o LLM pode usar a ferramenta spawn_agent para criar sub-agentes.\n\n"
	case "agents.running":
		return "%d em execução"
	case "agents.title":
		return "%d sub-agente(s):\n"
	case "agents.unavailable":
		return "Gerenciador de sub-agentes não configurado.\n\n"

	// ── Approval ──────────────────────────────────────────────────
	case "approval.accept":
		return "Aceitar"
	case "approval.allow":
		return "Permitir"
	case "approval.allow_always":
		return "Permitir Sempre"
	case "approval.deny":
		return "Negar"
	case "approval.reject":
		return "Rejeitar"
	case "approval.rejected":
		return "  Rejeitado.\n"

	// ── Branch ────────────────────────────────────────────────────
	case "branch.save_failed":
		return "Falha ao criar sessão ramificada: %v"

	// ── Cancel ────────────────────────────────────────────────────
	case "cancel.confirm":
		return "Pressione Ctrl-C ou Esc novamente para cancelar o agente em execução.\n\n"

	// ── Checkpoint ────────────────────────────────────────────────
	case "checkpoint.list.hint":
		return "\nUse /undo para reverter o mais recente.\n\n"
	case "checkpoint.list.item":
		return "  %d. %s  %s  %s  %s\n"
	case "checkpoint.list.title":
		return "Checkpoints (%d):\n\n"
	case "checkpoint.undo_failed":
		return "Desfazer falhou: %v\n\n"

	// ── Context ───────────────────────────────────────────────────
	case "context.unavailable":
		return "Sem dados de contexto ainda"
	case "context.until_compact":
		return "restante"

	// ── Cron ──────────────────────────────────────────────────────
	case "cron.firing":
		return "⏰ Tarefa cron disparada"

	// ── Empty state ───────────────────────────────────────────────
	case "empty.ask":
		return "Peça um refactor, correção de bug, explicação ou testes."
	case "empty.tips":
		return "Dicas: use @path para incluir arquivos, /? para ajuda, e Shift+Tab para mudar de modo."

	// ── Exit ──────────────────────────────────────────────────────
	case "exit.confirm":
		return "Pressione Ctrl-C novamente para sair.\n\n"

	// ── Bug report ────────────────────────────────────────────────
	case "bug.go":
		return "Go: %s\n"
	case "bug.hint":
		return "\nInclua estas informações ao relatar um bug.\n\n"
	case "bug.last_error":
		return "Último erro: %s\n"
	case "bug.mcp":
		return "Servidores MCP: %d\n"
	case "bug.model":
		return "Modelo: %s\n"
	case "bug.os":
		return "SO: %s %s\n"
	case "bug.provider":
		return "Fornecedor: %s\n"
	case "bug.session":
		return "Sessão: %s (%d mensagens)\n"
	case "bug.title":
		return "=== Diagnóstico de Relatório de Bug ===\n\n"
	case "bug.version":
		return "Versão: %s\n"

	// ── Compact ───────────────────────────────────────────────────
	case "compact.done":
		return "Histórico de conversa compactado.\n\n"
	case "compact.done_with_stats":
		return "Histórico de conversa compactado (%d → %d tokens).\n\n"
	case "compact.failed":
		return "Compactação falhou: %v\n\n"
	case "compact.unavailable":
		return "Gerenciador de contexto não disponível.\n\n"

	// ── Config ────────────────────────────────────────────────────
	case "config.instance_unavailable":
		return "Configuração de instância não disponível para este workspace"
	case "config.language_set":
		return "Config: idioma = %s\n\n"
	case "config.model_set":
		return "Config: modelo = %s\n\n"
	case "config.not_loaded":
		return "Configuração não carregada.\n\n"
	case "config.provider_set":
		return "Config: fornecedor = %s\n\n"
	case "config.save_scope_global":
		return "Salvar em → Global"
	case "config.save_scope_instance":
		return "Salvar em → Instância"
	case "config.save_scope_instance_new":
		return "Salvar em → Instância (nova config será criada)"
	case "config.save_target_line":
		return " Salvar em: %s%s  [Ctrl+T alternar]"
	case "config.save_target_new_hint":
		return " (nova config será criada ao salvar)"
	case "config.scope_global":
		return "Global"
	case "config.scope_instance":
		return "Instância"
	case "config.title":
		return "Configuração Atual:\n"
	case "config.unknown_key":
		return "Chave de config desconhecida: %s\nSuportadas: model, provider, language\n\n"
	case "config.usage":
		return "Uso: /config set <chave> <valor>\n\nChaves: model, vendor, endpoint, language, apikey [--vendor]\n\nEndpoints: /config add-endpoint <nome> <url_base> [--protocol openai] [--apikey sk-xxx]\n          /config remove-endpoint <nome>\n\n"

	// ── Follow ────────────────────────────────────────────────────
	case "follow.active_agent":
		return "Seguindo agente %s — entrada pausada. Pressione Esc para retornar."
	case "follow.active_teammate":
		return "Seguindo companheiro %s — entrada pausada. Pressione Esc para retornar."
	case "follow.hint":
		return "  ↑↓←→ alternar  Esc fechar"
	case "follow.loading":
		return "  Carregando visualização de acompanhamento..."
	case "follow.more":
		return "  +%d mais"
	case "follow.status_done":
		return "concluído"
	case "follow.status_running":
		return "em execução"

	// ── Header ────────────────────────────────────────────────────
	case "header.terminal_native":
		return "codificação AI nativa em terminal"

	// ── Hints ─────────────────────────────────────────────────────
	case "hint.add_context":
		return "@ adicionar contexto"
	case "hint.autocomplete":
		return "Tab/Shift+Tab alternar • Enter aplicar • Esc fechar"
	case "hint.ctrlc_cancel":
		return "Ctrl+C cancelar"
	case "hint.ctrlc_exit":
		return "Ctrl+C limpar/sair"
	case "hint.ctrlr_sidebar":
		return "Ctrl+R barra lateral"
	case "hint.ctrlv_image":
		return "Ctrl+V / Ctrl+Shift+V colar imagem"
	case "hint.enter_send":
		return "Enter enviar"
	case "hint.follow_panel":
		return "Ctrl+N acompanhar"
	case "hint.help":
		return "/? ajuda"
	case "hint.image_attached":
		return "imagem anexada"
	case "hint.image_attached_count":
		return "%d imagem(ns) anexada(s)"
	case "hint.mention":
		return "@ anexa arquivos/pastas • Tab/Shift+Tab alternar • Enter aplicar"
	case "hint.mode":
		return "modo"
	case "hint.scroll":
		return "PgUp/PgDn rolar"
	case "hint.shift_tab_mode":
		return "Shift+Tab modo"
	case "hint.unfollow_panel":
		return "Ctrl+N parar de acompanhar"

	// ── Image ─────────────────────────────────────────────────────
	case "image.attached":
		return "Imagem anexada: %s\n"
	case "image.attached_hint":
		return "Envie uma mensagem para incluir a imagem, ou /image para anexar outra.\n\n"
	case "image.clipboard_failed":
		return "Não foi possível colar uma imagem da área de transferência: %v"
	case "image.clipboard_no_image_windows":
		return "Nenhuma imagem encontrada na área de transferência. No Windows, Ctrl+V é frequentemente interceptado pelo terminal. Tente Ctrl+Shift+V ou /image paste."
	case "image.formats":
		return "Formatos suportados: PNG, JPEG, GIF, WebP (máx 20MB)\n\n"
	case "image.usage":
		return "Uso: /image <caminho/arquivo.png> ou /image paste\n"

	// ── IM ────────────────────────────────────────────────────────
	case "im.more":
		return "+%d mais (/qq)"
	case "im.none":
		return "Nenhum adaptador configurado"
	case "im.runtime.available":
		return "runtime disponível"
	case "im.runtime.disabled":
		return "desativado"
	case "im.runtime.not_started":
		return "ativado • reinicie para inicializar"
	case "im.status.not_started":
		return "não iniciado"
	case "im.summary":
		return "%d adaptadores • %d saudáveis"

	// ── Interrupt ─────────────────────────────────────────────────
	case "interrupt.delivered":
		return "[entregue à execução ativa; revisando plano]\n"
	case "interrupted":
		return "[interrompido]\n\n"

	// ── Knight ────────────────────────────────────────────────────
	case "knight.unavailable":
		return "Knight não está disponível"

	// ── Labels ────────────────────────────────────────────────────
	case "label.activity":
		return "atividade"
	case "label.agent_policy":
		return "agente"
	case "label.agents":
		return "agentes"
	case "label.approval_policy":
		return "aprovação"
	case "label.avg_duration":
		return "duração média"
	case "label.avg_think":
		return "think médio"
	case "label.avg_tps":
		return "tps médio"
	case "label.p95_tps":
		return "p95 tps"
	case "label.avg_ttft":
		return "ttft médio"
	case "label.branch":
		return "branch"
	case "label.cache_hit":
		return "cache hit"
	case "label.cache_read":
		return "leitura cache"
	case "label.cache_write":
		return "escrita cache"
	case "label.compact":
		return "compactar"
	case "label.cwd":
		return "cwd"
	case "label.directory":
		return "diretório"
	case "label.fail_rate":
		return "taxa falha"
	case "label.file":
		return "arquivo"
	case "label.input":
		return "entrada"
	case "label.latest":
		return "Mais recente"
	case "label.output":
		return "saída"
	case "label.p95_duration":
		return "p95 duração"
	case "label.p95_ttft":
		return "p95 ttft"
	case "label.recent_turns":
		return "turnos recentes"
	case "label.skills":
		return "skills"
	case "label.slow_tools":
		return "ferr. lentas"
	case "label.tool":
		return "ferramenta"
	case "label.tool_policy":
		return "ferramentas"
	case "label.total":
		return "total"
	case "label.turns":
		return "turnos"
	case "label.usage":
		return "uso"
	case "label.version":
		return "Versão"
	case "label.window":
		return "janela"

	// ── LAN Chat ──────────────────────────────────────────────────
	case "lanchat.unavailable":
		return "LAN Chat não está disponível."

	// ── Language ──────────────────────────────────────────────────
	case "lang.current":
		return "Idioma atual: %s\nUse /lang para escolher interativamente, ou /lang <en|zh-CN> para alternar diretamente.\n%s\n\n"
	case "lang.first_use.body":
		return " Selecione o idioma que deseja que o ggcode use a partir de agora."
	case "lang.first_use.hint":
		return " Tab/j/k mover • Enter confirmar • atalhos e/z"
	case "lang.first_use.title":
		return "Escolha seu idioma preferido"
	case "lang.invalid":
		return "Idioma não suportado: %s\n%s\n\n"
	case "lang.selection.current":
		return " Atual: %s"
	case "lang.selection.hint":
		return " Tab/j/k mover • Enter confirmar • atalhos e/z • Esc cancelar"
	case "lang.switch":
		return "Idioma alterado para: %s\n\n"

	// ── MCP ───────────────────────────────────────────────────────
	case "mcp.active_tools":
		return "Ferramentas ativas"
	case "mcp.more":
		return "… %d mais • /mcp"
	case "mcp.none":
		return "Nenhum servidor MCP configurado.\n\n"
	case "mcp.title":
		return "Servidores MCP:\n"

	// ── Memory ────────────────────────────────────────────────────
	case "memory.auto":
		return "Memória Automática:\n"
	case "memory.auto_none":
		return "  Nenhuma memória automática carregada.\n"
	case "memory.auto_unavailable":
		return "Memória automática não inicializada.\n\n"
	case "memory.clear_failed":
		return "Erro ao limpar memórias: %v\n\n"
	case "memory.list_failed":
		return "Erro ao listar memórias: %v\n\n"
	case "memory.project":
		return "Memória do Projeto:\n"
	case "memory.project_none":
		return "  Nenhum arquivo de memória do projeto carregado.\n"
	case "memory.usage":
		return "\nUso: /memory [list|clear]\n\n"

	// ── Metrics ───────────────────────────────────────────────────
	case "metrics.empty":
		return "Sem métricas ainda"

	// ── Mode details ──────────────────────────────────────────────
	case "mode.agent.autocontinue":
		return "continua automaticamente"
	case "mode.agent.waits":
		return "espera por você"
	case "mode.approval.ask":
		return "perguntar conforme necessário"
	case "mode.approval.critical":
		return "apenas crítico"
	case "mode.approval.none":
		return "sem paradas de aprovação"
	case "mode.current":
		return "Modo atual: %s\nUso: /mode <supervised|plan|auto|bypass|autopilot>\n  supervised  Perguntar quando uma ferramenta não tem regra explícita\n  plan        Exploração somente leitura; nega escritas e comandos\n  auto        Permite operações seguras, nega perigosas\n  bypass      Permite quase tudo; para apenas em ações críticas\n  autopilot   bypass + continua quando o modelo pede resposta\n\n"
	case "mode.persist_failed":
		return "Falha ao persistir preferência de modo: %v"
	case "mode.tools.open":
		return "quase todas as ferramentas"
	case "mode.tools.readonly":
		return "apenas leitura"
	case "mode.tools.rules":
		return "seguir regras de ferramentas"
	case "mode.tools.safe":
		return "apenas operações seguras"

	// ── Pairing ───────────────────────────────────────────────────
	case "pairing.blacklisted":
		return "Este canal foi bloqueado devido a múltiplas rejeições."
	case "pairing.rejected":
		return "A solicitação de pareamento atual foi rejeitada. Inicie novamente para continuar."

	// ── Plugins ───────────────────────────────────────────────────
	case "plugins.none":
		return "Nenhum plugin carregado.\n\n"
	case "plugins.title":
		return "Plugins:\n"
	case "plugins.unavailable":
		return "Gerenciador de plugins não disponível.\n\n"

	// ── Queued ────────────────────────────────────────────────────
	case "queued.count":
		return "%d na fila"
	case "queued.output":
		return "[%d na fila pendentes]\n\n"

	// ── Reasoning ─────────────────────────────────────────────────
	case "reasoning.effort.set":
		return "Esforço de raciocínio definido para %s nesta sessão"
	case "reasoning.effort.status":
		return "Esforço de raciocínio: %s"
	case "reasoning.effort.unsupported":
		return "Esforço de raciocínio não suportado pelo provedor atual"
	case "reasoning.effort.unsupported.status":
		return "Esforço de raciocínio não suportado pelo provedor atual"

	// ── Reflect ───────────────────────────────────────────────────
	case "reflect.empty":
		return "Sem insights de execução ainda. Insights são gerados automaticamente após cada execução do agente com 3+ chamadas de ferramenta ou edições de arquivo."
	case "reflect.load_failed":
		return "Falha ao carregar insights: %v"
	case "reflect.memory_location":
		return "Local da memória: %s\n"
	case "reflect.no_agent":
		return "Agente não inicializado."
	case "reflect.no_memory":
		return "Memória do projeto não disponível para este diretório."
	case "reflect.no_workdir":
		return "Diretório de trabalho não definido."
	case "reflect.title":
		return "## Insights de Execução Acumulados\n\n"

	// ── Regenerate ────────────────────────────────────────────────
	case "regenerate.no_context":
		return "Gerenciador de contexto não disponível."

	// ── Session ───────────────────────────────────────────────────
	case "session.ephemeral":
		return "efêmera"
	case "session.export_failed":
		return "Erro ao exportar sessão: %v\n\n"
	case "session.list.item":
		return "  %d. %s  %s  (%s)\n"
	case "session.list_failed":
		return "Erro ao listar sessões: %v\n\n"
	case "session.resume_fallback":
		return "Iniciando nova sessão em vez disso.\n\n"
	case "session.store_missing":
		return "Armazenamento de sessões não configurado.\n\n"
	case "session.write_failed":
		return "Erro ao escrever arquivo: %v\n\n"

	// ── Shell ─────────────────────────────────────────────────────
	case "shell.empty":
		return "Comando shell está vazio."
	case "shell.already_running":
		return "Um comando shell ja esta em execucao - aguarde ou Esc para cancelar."

	// ── Slash command descriptions ────────────────────────────────
	case "slash.allow":
		return "Permitir sempre uma ferramenta"
	case "slash.branch":
		return "Ramificar sessão atual para nova sessão (bifurcar conversa)"
	case "slash.bug":
		return "Reportar um bug"
	case "slash.chat":
		return "Abrir painel do LAN Chat"
	case "slash.config":
		return "Ver/modificar configuração"
	case "slash.dingtalk":
		return "Gerenciar vinculação do canal DingTalk"
	case "slash.discord":
		return "Gerenciar vinculação do canal Discord"
	case "slash.edit":
		return "Editar e reenviar sua última mensagem"
	case "slash.feishu":
		return "Gerenciar vinculação do canal Feishu"
	case "slash.files":
		return "Mostrar arquivos modificados pelo agente"
	case "slash.image":
		return "Anexar uma imagem"
	case "slash.impersonate":
		return "Personificar uma ferramenta CLI para exibição no prompt do shell"
	case "slash.inspector":
		return "Alternar painel do inspetor"
	case "slash.irc":
		return "Gerenciar vinculação do canal IRC"
	case "slash.knight":
		return "Gerenciar agente autônomo em segundo plano"
	case "slash.matrix":
		return "Gerenciar vinculação do canal Matrix"
	case "slash.mattermost":
		return "Gerenciar vinculação do canal Mattermost"
	case "slash.nick":
		return "Definir seu apelido do LAN Chat"
	case "slash.nostr":
		return "Gerenciar vinculação do canal Nostr"
	case "slash.pc":
		return "Gerenciar vinculação do canal PC"
	case "slash.qq":
		return "Gerenciar vinculação do canal QQ"
	case "slash.reflect":
		return "Executar autorreflexão na sessão atual"
	case "slash.signal":
		return "Gerenciar vinculação do canal Signal"
	case "slash.slack":
		return "Gerenciar vinculação do canal Slack"
	case "slash.status":
		return "Mostrar status atual"
	case "slash.stream":
		return "Configurar modo de saída de streaming"
	case "slash.telegram":
		return "Gerenciar vinculação do canal Telegram"
	case "slash.twitch":
		return "Gerenciar vinculação do canal Twitch"
	case "slash.update":
		return "Atualizar ggcode"
	case "slash.wechat":
		return "Gerenciar vinculação do canal WeChat"
	case "slash.wecom":
		return "Gerenciar vinculação do canal WeCom (WeChat Enterprise)"
	case "slash.whatsapp":
		return "Gerenciar vinculação do canal WhatsApp"

	// ── Startup ───────────────────────────────────────────────────
	case "startup.banner":
		return "Preparando a interface do terminal e filtrando ruído de inicialização. Você pode digitar imediatamente; este banner desaparece quando a inicialização estabilizar."

	// ── Status ────────────────────────────────────────────────────
	case "status.cancelling":
		return "Cancelando..."
	case "status.compacted":
		return "[conversa compactada]"
	case "status.compacting":
		return "Comprimindo contexto..."
	case "status.thinking":
		return "Pensando..."
	case "status.title":
		return "Status:\n"
	case "status.tools_used":
		return "%d ferramentas usadas"
	case "status.writing":
		return "Escrevendo..."

	// ── Todo ──────────────────────────────────────────────────────
	case "todo.clear_failed":
		return "Erro ao limpar todos: %v\n\n"
	case "todo.cleared":
		return "Lista de todos limpa.\n\n"
	case "todo.none":
		return "Nenhuma lista de todos encontrada. Use a ferramenta todo_write para criar uma.\n\n"
	case "todo.parse_failed":
		return "Erro ao analisar todos: %v\n\n"
	case "todo.read_failed":
		return "Erro ao ler todos: %v\n\n"
	case "todo.title":
		return "Lista de todos:\n%s\n\n"

	// ── Tool ──────────────────────────────────────────────────────
	case "tool.content":
		return "conteúdo"
	case "tool.done":
		return "concluído"
	case "tool.entry":
		return "entrada"
	case "tool.failed":
		return "falhou"
	case "tool.match":
		return "correspondência"
	case "tool.matches":
		return "correspondências"
	case "tool.no_output":
		return "sem saída"
	case "tool.output":
		return "saída"
	case "tool.result":
		return "resultado"

	// ── Tunnel ────────────────────────────────────────────────────
	case "tunnel.mobile_connected":
		return "Cliente móvel conectado."
	case "tunnel.not_active":
		return "Nenhuma sessão de compartilhamento ativa."
	case "tunnel.stopped":
		return "Túnel interrompido."

	// ── Update ────────────────────────────────────────────────────
	case "update.available":
		return "atualização disponível: %s"
	case "update.check_failed":
		return "verificação falhou: %s"
	case "update.current":
		return "atual: %s (mais recente: %s)"
	case "update.dual_scope":
		return "Aviso: Instalações de ggcode de usuário e de sistema encontradas:\n  Usuário: %s\n  Sistema: %s\nIsso pode causar conflitos de PATH. Considere desinstalar uma em Configurações > Aplicativos.\n\n"
	case "update.failed":
		return "Atualização falhou: %v\n\n"
	case "update.other_installs":
		return "Outras instalações do ggcode detectadas neste sistema:\n%s\nSe um ggcode diferente aparecer primeiro no PATH, considere atualizá-lo também ou ajustar a ordem do PATH.\n\n"
	case "update.pm_hint.brew":
		return "Atualização instalada. Nota: ggcode foi instalado via Homebrew.\nExecute `brew upgrade ggcode` para manter o Homebrew sincronizado.\n\n"
	case "update.pm_hint.scoop":
		return "Atualização instalada. Nota: ggcode foi instalado via Scoop.\nExecute `scoop update ggcode` para manter o Scoop sincronizado.\n\n"
	case "update.pm_hint.snap":
		return "Atualização instalada. Nota: ggcode foi instalado via Snap.\nExecute `sudo snap refresh ggcode` para manter o Snap sincronizado.\n\n"
	case "update.pm_hint.winget":
		return "Atualização instalada. Nota: ggcode foi instalado via winget.\nExecute `winget upgrade ggcode` para manter o winget sincronizado.\n\n"
	case "update.preparing":
		return "Preparando atualização"
	case "update.restart_failed":
		return "Atualização preparada, mas reinício falhou: %v\n\n"
	case "update.sidebar_hint":
		return "Nova versão disponível. Execute /update."
	case "update.unavailable":
		return "Atualização indisponível nesta sessão.\n\n"
	case "update.unknown":
		return "ainda não verificado"
	case "update.up_to_date":
		return "Você está atualizado."

	// ── Workspace ─────────────────────────────────────────────────
	case "workspace.tagline":
		return "workspace geek de IA"

	// ── Command ───────────────────────────────────────────────────
	case "command.custom":
		return "Comando personalizado /%s:\n"
	case "command.edit_busy":
		return "Agente está ocupado. Aguarde a execução atual terminar antes de editar."
	case "command.edit_empty":
		return "Nenhum envio anterior para editar."
	case "command.edit_ready":
		return "Último envio carregado — edite e pressione Enter para enviar."
	case "command.mention_error":
		return "Erro de expansão de menção: %v"
	case "command.model_failed":
		return "Falha ao trocar modelo: %v\n\n"
	case "command.provider_current":
		return "Fornecedor atual: %s (endpoint: %s, modelo: %s)\nFornecedores disponíveis: %s\nEndpoints disponíveis: %s\nUso: /provider [fornecedor] [endpoint]\n\n"
	case "command.provider_failed":
		return "Falha ao atualizar seleção de fornecedor: %v\n\n"
	case "command.provider_switched":
		return "Fornecedor alterado para: %s (modelo: %s)\n\n"
	case "command.provider_unknown":
		return "Fornecedor desconhecido: %s (disponíveis: %v)\n\n"
	case "command.retry_busy":
		return "Agente está ocupado. Aguarde a execução atual terminar antes de tentar novamente."
	case "command.retry_empty":
		return "Nenhum envio anterior para tentar novamente."
	case "command.skill_agent_only":
		return "Skill %s só pode ser invocada pelo agente."
	case "command.usage.allow":
		return "Uso: /allow <nome-ferramenta>\n\n"
	case "command.usage.export":
		return "Uso: /export <id-sessão>\n\n"
	case "command.usage.resume":
		return "Uso: /resume <id-sessão>\n\n"

	// ── Files ─────────────────────────────────────────────────────
	case "files.disabled":
		return "Checkpointing não habilitado.\n\n"
	case "files.hint":
		return "\nUse /undo para reverter a edição mais recente, /checkpoints para detalhes.\n\n"
	case "files.item":
		return "  %s  %d edições  última: %s%s\n"

	// ── Init ──────────────────────────────────────────────────────
	case "init.generate_failed":
		return "Falha ao gerar conteúdo GGCODE.md: %v\n\n"
	case "init.resolve_failed":
		return "Falha ao resolver destino de inicialização: %v\n\n"

	// ── Panel ─────────────────────────────────────────────────────
	case "panel.agent_status":
		return "Status do agente"
	case "panel.approval_required":
		return "Aprovação necessária"
	case "panel.bypass_approval":
		return "Aprovação modo bypass"
	case "panel.commands":
		return "Comandos:"
	case "panel.composer":
		return "Compositor"
	case "panel.composer_locked":
		return "Compositor bloqueado"
	case "panel.context":
		return "Contexto"
	case "panel.conversation":
		return "Conversa"
	case "panel.files":
		return "Arquivos:"
	case "panel.im":
		return "IM"
	case "panel.mcp":
		return "MCP"
	case "panel.mcp.install_spec_required":
		return "Digite uma especificação de instalação primeiro."
	case "panel.mcp.installing_server":
		return "Instalando servidor MCP..."
	case "panel.mcp.reconnect_failed":
		return "Não foi possível reconectar %s."
	case "panel.mcp.reconnect_unavailable":
		return "Reconexão indisponível nesta sessão."
	case "panel.mcp.reconnecting":
		return "Reconectando %s..."
	case "panel.mcp.uninstalling":
		return "Desinstalando %s..."
	case "panel.metrics":
		return "Métricas"
	case "panel.mode_policy":
		return "Política de modo"
	case "panel.model.context_applied":
		return "Aplicado context_window=%d, max_tokens=%d (salvo)"
	case "panel.model.context_cleared":
		return "Redefinido para auto-detecção (salvo)"
	case "panel.model.context_window":
		return "Janela de Contexto"
	case "panel.model.edit":
		return "Editar"
	case "panel.model.endpoint":
		return "Endpoint"
	case "panel.model.hint.edit":
		return "Enter salvar • Esc cancelar (0 ou vazio = auto, sufixo K/M/G OK ex. 256k)"
	case "panel.model.hint.main":
		return "j/k mover • Enter ou s aplicar • w janela contexto • o máx tokens • r atualizar • / filtrar • Esc fechar"
	case "panel.model.max_tokens":
		return "Máx. Tokens de Saída"
	case "panel.model.models":
		return "Modelos"
	case "panel.model.protocol":
		return "Protocolo"
	case "panel.model.refresh.builtin_loaded":
		return "Modelos embutidos carregados."
	case "panel.model.refresh.builtin_reason":
		return "Usando modelos embutidos: %s"
	case "panel.model.refresh.remote_loaded":
		return "Carregados %d modelo(s) remoto(s)."
	case "panel.model.refresh.save_failed":
		return "Modelos atualizados, mas salvar config falhou: %s"
	case "panel.model.refreshing":
		return "Atualizando modelos mais recentes..."
	case "panel.model.saved_runtime_inactive":
		return "Config salva, mas runtime atual ainda inativo: %s"
	case "panel.model.source":
		return "Origem"
	case "panel.model.source.builtin":
		return "embutido"
	case "panel.model.source.remote":
		return "remoto"
	case "panel.model.switched":
		return "Modelo alterado para %s."
	case "panel.model.vendor":
		return "Fornecedor"
	case "panel.model_filter.placeholder":
		return "digite para filtrar modelos"
	case "panel.model_filter.prompt":
		return "Filtro> "
	case "panel.model_list.hidden_above":
		return "%d acima"
	case "panel.model_list.hidden_more":
		return "%d mais"
	case "panel.model_list.no_matches":
		return "(sem correspondências)"
	case "panel.model_list.none":
		return "(nenhum)"
	case "panel.model_list.showing":
		return "mostrando %d/%d modelos"
	case "panel.provider.active_draft":
		return "Rascunho ativo"
	case "panel.provider.api_key":
		return "Chave de API"
	case "panel.provider.api_key.configured":
		return "configurada"
	case "panel.provider.api_key.missing":
		return "ausente"
	case "panel.provider.auth":
		return "Autenticação"
	case "panel.provider.auth.connected":
		return "conectado"
	case "panel.provider.auth.not_connected":
		return "não conectado"
	case "panel.provider.base_url":
		return "URL Base"
	case "panel.provider.base_url.not_set":
		return "(não definido)"
	case "panel.provider.edit":
		return "Editar"
	case "panel.provider.edit.custom_model":
		return "modelo personalizado"
	case "panel.provider.edit.endpoint_api_key":
		return "chave de api do endpoint"
	case "panel.provider.edit.endpoint_base_url":
		return "url base do endpoint"
	case "panel.provider.edit.new_endpoint_name":
		return "nome do novo endpoint"
	case "panel.provider.edit.vendor_api_key":
		return "chave de api do fornecedor"
	case "panel.provider.endpoints":
		return "Endpoints"
	case "panel.provider.enterprise_url":
		return "URL Enterprise"
	case "panel.provider.env_var":
		return "Variável de ambiente"
	case "panel.provider.hint.copilot":
		return "GitHub Copilot: l login • x logout • b editar domínio enterprise"
	case "panel.provider.hint.edit":
		return "Enter salvar • Esc cancelar"
	case "panel.provider.hint.main":
		return "Tab/Shift+Tab mudar foco • j/k mover • / focar filtro • Enter ou s aplicar • a chave fornecedor • u chave endpoint • b URL base • m modelo personalizado • e adicionar endpoint • Esc fechar"
	case "panel.provider.login.browser_failed":
		return "Falha ao abrir a página de verificação: %s"
	case "panel.provider.login.browser_opened":
		return "Página de verificação aberta no seu navegador."
	case "panel.provider.login.copied":
		return "Código do dispositivo copiado para a área de transferência."
	case "panel.provider.login.copy_failed":
		return "Falha ao copiar código do dispositivo: %s"
	case "panel.provider.login.failed":
		return "Login do GitHub Copilot falhou: %s"
	case "panel.provider.login.instructions":
		return "Abra %s e digite o código %s. Aguardando autorização..."
	case "panel.provider.login.starting":
		return "Iniciando login do GitHub Copilot..."
	case "panel.provider.login.success":
		return "GitHub Copilot conectado."
	case "panel.provider.logout.success":
		return "GitHub Copilot desconectado."
	case "panel.provider.model.set_with_m":
		return "(definido com m)"
	case "panel.provider.models":
		return "Modelos"
	case "panel.provider.protocol":
		return "Protocolo"
	case "panel.provider.protocol.unknown":
		return "(desconhecido)"
	case "panel.provider.refresh.failed":
		return "Atualização de modelos falhou: %s"
	case "panel.provider.refresh.none":
		return "Nenhum endpoint atualizável para este fornecedor."
	case "panel.provider.refresh.partial":
		return "Atualizados %d endpoint(s), descobertos %d modelo(s). Alguns endpoints falharam: %v"
	case "panel.provider.refresh.save_failed":
		return "Modelos atualizados, mas salvar config falhou: %s"
	case "panel.provider.refresh.success":
		return "Atualizados %d endpoint(s), descobertos %d modelo(s)."
	case "panel.provider.refreshing_vendor":
		return "Atualizando modelos para %s..."
	case "panel.provider.saved":
		return "Salvo."
	case "panel.provider.saved_activated":
		return "Salvo e ativado."
	case "panel.provider.tags":
		return "Tags"
	case "panel.provider.vendors":
		return "Fornecedores"

	// ── Panel: QQ ────────────────────────────────────────────────
	case "panel.qq.actions_hint":
		return "j/k mover • d desativar/ativar • Enter ou b vincular bot • c vincular canal • x desvincular canal • u desvincular bot • i criar bot • e editar config • q mostrar QR • Esc fechar"
	case "panel.qq.adapter":
		return "Adaptador: %s"
	case "panel.qq.available":
		return "Disponíveis: %d"
	case "panel.qq.bind_channel":
		return "Vincular Canal"
	case "panel.qq.bot_input":
		return "Bot QQ: %s"
	case "panel.qq.bot_list":
		return "Lista de Bots QQ"
	case "panel.qq.bots":
		return "Bots QQ"
	case "panel.qq.bound":
		return "Vinculados: %d"
	case "panel.qq.bound_directory":
		return "Diretório Vinculado: %s"
	case "panel.qq.channel":
		return "Canal: %s"
	case "panel.qq.create":
		return "Criar"
	case "panel.qq.create_example":
		return "Exemplo: qq-main 123456 valor-secreto"
	case "panel.qq.create_format":
		return "Formato: <bot-id> <appid> <appsecret>"
	case "panel.qq.create_hint":
		return "Enter criar bot • Esc cancelar"
	case "panel.qq.created":
		return "Criados: %d"
	case "panel.qq.current_binding":
		return "Vinculação Atual"
	case "panel.qq.current_directory_channel":
		return "Canal do Diretório Atual: %s"
	case "panel.qq.current_directory_target":
		return "Alvo do Diretório Atual: %s"
	case "panel.qq.default":
		return "(padrão)"
	case "panel.qq.details":
		return "Detalhes"
	case "panel.qq.directory":
		return "Diretório"
	case "panel.qq.entry.active":
		return "Ativo"
	case "panel.qq.entry.available":
		return "Disponível"
	case "panel.qq.entry.bound":
		return "Vinculado"
	case "panel.qq.entry.bound_other":
		return "Vinculado: %s"
	case "panel.qq.entry.muted":
		return "Silenciado"
	case "panel.qq.error.adapter_required":
		return "Nome do adaptador QQ é obrigatório"
	case "panel.qq.error.config_format":
		return "Config do bot QQ deve ser: <bot-id> <appid> <appsecret>"
	case "panel.qq.error.config_unavailable":
		return "config indisponível"
	case "panel.qq.error.disabled":
		return "Bot QQ %q está desativado"
	case "panel.qq.error.not_configured":
		return "Bot QQ %q não está configurado"
	case "panel.qq.error.not_online":
		return "Bot QQ %q não está online"
	case "panel.qq.error.not_online_detail":
		return "Bot QQ %q não está online: %s"
	case "panel.qq.error.not_qq_adapter":
		return "adaptador %q não é um bot QQ"
	case "panel.qq.last_error":
		return "Último Erro: %s"
	case "panel.qq.message.added_bot":
		return "Bot QQ %s adicionado."
	case "panel.qq.message.bound_success":
		return "Bot QQ vinculado ao workspace atual. Use c para gerar o QR code de vinculação do canal."
	case "panel.qq.message.cleared":
		return "Autorização do canal QQ limpa para o workspace atual."
	case "panel.qq.message.no_bot":
		return "Nenhum bot QQ disponível."
	case "panel.qq.message.share_generated":
		return "Link de compartilhamento QQ gerado. Escaneie o QR code, adicione o bot, depois envie uma mensagem para iniciar o pareamento."
	case "panel.qq.message.unbound":
		return "Canal QQ desvinculado."
	case "panel.qq.no_bots":
		return "Nenhum bot QQ configurado."
	case "panel.qq.none":
		return "(nenhum)"
	case "panel.qq.occupied_by":
		return "Ocupado por: %s"
	case "panel.qq.qr_code":
		return "QR Code:"
	case "panel.qq.runtime":
		return "Runtime"
	case "panel.qq.runtime.available":
		return "disponível"
	case "panel.qq.runtime.disabled":
		return "desativado (defina im.enabled: true e reinicie o ggcode)"
	case "panel.qq.runtime.not_started":
		return "não iniciado (reinicie o ggcode para inicializar o runtime IM)"
	case "panel.qq.scan_hint":
		return "Escaneie o QR code, adicione o bot, depois envie uma mensagem para iniciar o pareamento."
	case "panel.qq.share_link":
		return "Link de Compartilhamento:"
	case "panel.qq.status":
		return "Status: %s"
	case "panel.qq.status.not_started":
		return "não iniciado"
	case "panel.qq.status.unknown":
		return "desconhecido"
	case "panel.qq.target":
		return "Alvo: %s"
	case "panel.qq.transport":
		return "Transporte: %s"
	case "panel.qq.waiting_for_pairing":
		return "(aguardando pareamento)"

	// ── Panel misc ────────────────────────────────────────────────
	case "panel.review_file_change":
		return "Revisar alteração de arquivo"
	case "panel.session_usage":
		return "Uso da sessão"
	case "panel.startup":
		return "Inicializando"
	case "panel.update":
		return "Atualizar"

		// ── Harness ───────────────────────────────────────────────────
	}
	return enCatalog(key)
}
