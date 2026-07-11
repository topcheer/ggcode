package tui

// ruCatalog returns the Russian translation for the given key.
// Keys not yet translated fall through to enCatalog.
func ruCatalog(key string) string {
	switch key {
	// --- Workspace & header ---
	case "workspace.tagline":
		return "Рабочая среда Geek-AI"
	case "header.terminal_native":
		return "Терминальный AI-кодинг"
	case "session.ephemeral":
		return "эфемерный"

	// --- Agents ---
	case "agents.idle":
		return "ожидание"
	case "agents.running":
		return "%d активных"
	case "cron.firing":
		return "⏰ Сработала задача cron"
	case "activity.idle":
		return "ожидание"

	// --- Panel ---
	case "panel.conversation":
		return "Беседа"
	case "panel.composer":
		return "Ввод"
	case "panel.composer_locked":
		return "Ввод заблокирован"
	case "panel.commands":
		return "Команды:"
	case "panel.files":
		return "Файлы:"
	case "panel.agent_status":
		return "Статус агента"
	case "panel.mode_policy":
		return "Политика режима"
	case "panel.session_usage":
		return "Использование сессии"
	case "panel.metrics":
		return "Метрики"
	case "panel.context":
		return "Контекст"
	case "panel.im":
		return "IM"
	case "panel.mcp":
		return "MCP"
	case "panel.mcp.install_spec_required":
		return "Сначала укажите спецификацию установки."
	case "panel.mcp.installing_server":
		return "Установка MCP-сервера..."
	case "panel.mcp.reconnect_unavailable":
		return "Переподключение недоступно в этой сессии."
	case "panel.mcp.reconnecting":
		return "Переподключение %s..."
	case "panel.mcp.reconnect_failed":
		return "Не удалось переподключить %s."
	case "panel.mcp.uninstalling":
		return "Удаление %s..."
	case "panel.startup":
		return "Инициализация"
	case "panel.approval_required":
		return "Требуется подтверждение"
	case "panel.bypass_approval":
		return "Подтверждение в режиме обхода"
	case "panel.review_file_change":
		return "Проверка изменения файла"

	// --- Labels ---
	case "label.vendor":
		return "Поставщик"
	case "label.endpoint":
		return "Эндпоинт"
	case "label.model":
		return "Модель"
	case "label.mode":
		return "Режим"
	case "label.session":
		return "Сессия"
	case "label.agents":
		return "Агенты"
	case "label.cwd":
		return "Дир."
	case "label.branch":
		return "Ветвь"
	case "label.context":
		return "Контекст"
	case "label.skills":
		return "Навыки"
	case "label.activity":
		return "Активность"
	case "label.window":
		return "Окно"
	case "label.usage":
		return "Использ."
	case "label.compact":
		return "компакт."
	case "label.total":
		return "всего"
	case "label.cost":
		return "оц. стоимость"
	case "label.approval_policy":
		return "Подтверждение"
	case "label.tool_policy":
		return "Инструменты"
	case "label.agent_policy":
		return "Агент"
	case "label.tool":
		return "Инструмент"
	case "label.input":
		return "Вход"
	case "label.output":
		return "Выход"
	case "label.cache_read":
		return "Чтение кэша"
	case "label.cache_write":
		return "Запись кэша"
	case "label.cache_hit":
		return "Попадание кэша"
	case "label.turns":
		return "Ходы"
	case "label.avg_ttft":
		return "Ø TTFT"
	case "label.p95_ttft":
		return "p95 TTFT"
	case "label.avg_duration":
		return "Ø длительность"
	case "label.p95_duration":
		return "p95 длительность"
	case "label.avg_think":
		return "Ø размышление"
	case "label.fail_rate":
		return "Уровень ошибок"
	case "label.slow_tools":
		return "медл. инструменты"
	case "label.recent_turns":
		return "недавние ходы"
	case "label.file":
		return "Файл"
	case "label.directory":
		return "Каталог"

	// --- Context & metrics ---
	case "context.unavailable":
		return "Данные контекста пока недоступны"
	case "metrics.empty":
		return "Метрики пока недоступны"
	case "im.none":
		return "Адаптеры не настроены"
	case "im.summary":
		return "%d адаптеров • %d здоровых"
	case "im.more":
		return "+%d ещё (/qq)"
	case "im.runtime.available":
		return "Среда доступна"
	case "im.runtime.disabled":
		return "отключено"
	case "im.runtime.not_started":
		return "включено • перезапустите для инициализации"
	case "im.status.not_started":
		return "не запущено"
	case "context.until_compact":
		return "осталось"

	// --- Empty ---
	case "empty.ask":
		return "Запросите рефакторинг, исправление бага, объяснение или тесты."
	case "empty.tips":
		return "Подсказки: @путь для файлов, /? для справки, Shift+Tab для смены режима."
	case "startup.banner":
		return "Подготовка интерфейса терминала и фильтрация шумов запуска. Вы можете начать вводить сразу; этот баннер исчезнет после завершения инициализации."

	// --- Harness ---
	case "harness.views":
		return "Представления"
	case "harness.items":
		return "Элементы"
	case "harness.action":
		return "Действие"
	case "harness.details":
		return "Детали"
	case "harness.none":
		return "(нет)"
	case "harness.unknown":
		return "неизвестно"
	case "harness.unscoped":
		return "без области"
	case "harness.unavailable":
		return "Harness недоступен"
	case "harness.unavailable_intro":
		return "Начните здесь в существующем проекте:"
	case "harness.unavailable_step_init":
		return "  1. Нажмите Enter или i для инициализации Harness"
	case "harness.unavailable_step_refresh":
		return "  2. Нажмите r для обновления после завершения"
	case "harness.section.init":
		return "Init"
	case "harness.section.check":
		return "Проверка"
	case "harness.section.doctor":
		return "Doctor"
	case "harness.section.monitor":
		return "Монитор"
	case "harness.section.gc":
		return "GC"
	case "harness.section.contexts":
		return "Контексты"
	case "harness.section.tasks":
		return "Задачи"
	case "harness.section.queue":
		return "Очередь"
	case "harness.section.run":
		return "Запуск"
	case "harness.section.run_queued":
		return "Запуск очереди"
	case "harness.section.inbox":
		return "Входящие"
	case "harness.section.review":
		return "Ревью"
	case "harness.section.promote":
		return "Продвижение"
	case "harness.section.release":
		return "Релиз"
	case "harness.section.rollouts":
		return "Роллауты"
	case "harness.hints.unavailable":
		return "Enter/i init Harness • r обновить • Esc закрыть"
	case "harness.hints.move":
		return "j/k перемещение"
	case "harness.hints.tab":
		return "Tab переключить"
	case "harness.hints.refresh":
		return "r обновить"
	case "harness.hints.close":
		return "Esc закрыть"
	case "harness.hints.check":
		return "Enter запустить проверки"
	case "harness.hints.monitor":
		return "Enter обновить снапшот"
	case "harness.hints.gc":
		return "Enter запустить GC"
	case "harness.hints.type_goal":
		return "введите цель"
	case "harness.hints.queue":
		return "Enter добавить в очередь"
	case "harness.hints.run":
		return "Enter запустить"
	case "harness.hints.focus_input":
		return "Tab фокус на ввод"
	case "harness.hints.rerun":
		return "Enter повторить неудачные"
	case "harness.hints.next":
		return "Enter следующая"
	case "harness.hints.all":
		return "a все"
	case "harness.hints.retry_failed":
		return "f повторить неудачные"
	case "harness.hints.resume":
		return "s возобновить"
	case "harness.hints.promote_owner":
		return "p взять владельца"
	case "harness.hints.retry_owner":
		return "f повторить владельца"
	case "harness.hints.approve":
		return "Enter одобрить"
	case "harness.hints.reject":
		return "x отклонить"
	case "harness.hints.promote":
		return "Enter продвинуть"
	case "harness.hints.apply_batch":
		return "Enter применить пакет"
	case "harness.hints.advance":
		return "Enter продолжить"
	case "harness.hints.approve_gate":
		return "g одобрить гейт"
	case "harness.hints.pause_resume":
		return "p пауза/возобновить"
	case "harness.hints.abort":
		return "x отменить"
	case "harness.hint.primary.check":
		return "Нажмите Enter, чтобы запустить проверки."
	case "harness.hint.primary.monitor":
		return "Нажмите Enter, чтобы обновить снапшот монитора."
	case "harness.hint.primary.gc":
		return "Нажмите Enter, чтобы запустить сборку мусора."
	case "harness.hint.primary.queue":
		return "Введите цель, затем нажмите Enter для добавления в очередь."
	case "harness.hint.primary.run":
		return "Введите цель, затем нажмите Enter для запуска."
	case "harness.hint.primary.tasks":
		return "Нажмите Enter, чтобы повторить выбранную неудачную задачу."
	case "harness.hint.primary.run_queued":
		return "Enter для следующей; a запускает все; f повторяет неудачные; s возобновляет прерванные."
	case "harness.hint.primary.inbox":
		return "Нажмите p, чтобы взять этого владельца, или f, чтобы повторить."
	case "harness.hint.primary.review":
		return "Нажмите Enter для одобрения или x для отклонения."
	case "harness.hint.primary.promote":
		return "Нажмите Enter, чтобы продвинуть выбранную задачу."
	case "harness.hint.primary.release":
		return "Нажмите Enter, чтобы применить текущий пакет релиза."
	case "harness.hint.primary.rollouts":
		return "Нажмите Enter для продолжения; g одобряет гейт; p ставит на паузу/возобновляет; x отменяет."
	case "harness.hint.primary.none":
		return "Для этого раздела не требуется прямого ввода."
	case "harness.message.read_only":
		return "Панель Harness доступна только для чтения, пока активен другой запуск."
	case "harness.message.monitor_refreshed":
		return "Монитор Harness обновлён."
	case "harness.message.rerun_failed_only":
		return "Задача Harness %s имеет статус %s; можно повторять только неудачные задачи."
	case "harness.message.review_approved":
		return "Ревью одобрено для %s"
	case "harness.message.review_rejected":
		return "Ревью отклонено для %s"
	case "harness.message.promoted":
		return "Продвинуто: %s"
	case "harness.message.no_release_tasks":
		return "Нет задач Harness, готовых к релизу."
	case "harness.message.release_applied":
		return "Пакет релиза %s применён"
	case "harness.message.no_rollouts":
		return "Сохранённые роллауты не найдены."
	case "harness.message.rollout_advanced":
		return "Роллаут %s продолжен"
	case "harness.message.owner_promoted":
		return "Продвинуто %d задач(и) для %s"
	case "harness.message.owner_retried":
		return "Повторены неудачные задачи для %s"
	case "harness.message.gate_approved":
		return "Следующий гейт одобрен для %s"
	case "harness.message.rollout_resumed":
		return "Роллаут %s возобновлён"
	case "harness.message.rollout_paused":
		return "Роллаут %s приостановлен"
	case "harness.message.rollout_aborted":
		return "Роллаут %s отменён"
	case "harness.message.check_passed":
		return "Проверка Harness пройдена."
	case "harness.message.check_failed":
		return "Проверка Harness обнаружила проблемы."
	case "harness.message.gc_complete":
		return "Сборка мусора Harness завершена."
	case "harness.message.queue_goal_required":
		return "Сначала введите цель очереди в поле ввода."
	case "harness.message.queued":
		return "Задача Harness %s добавлена в очередь"
	case "harness.activity.status":
		return "Harness статус: %s"
	case "harness.log.phase":
		return "Фаза"
	case "harness.log.worker":
		return "Воркер"
	case "harness.tool.read_file":
		return "Чтение файла"
	case "harness.tool.write_file":
		return "Запись файла"
	case "harness.tool.browse_files":
		return "Просмотр файлов"
	case "harness.tool.search_code":
		return "Поиск по коду"
	case "harness.tool.run_command":
		return "Выполнить команду"
	case "harness.tool.fetch_web_page":
		return "Загрузить веб-страницу"
	case "harness.tool.run_subagent":
		return "Запустить суб-агента"
	case "harness.tool.update_task_state":
		return "Обновить состояние задачи"
	case "harness.message.run_goal_required":
		return "Сначала введите цель запуска в поле ввода."
	case "harness.message.no_queued_executed":
		return "Ни одна задача из очереди не была выполнена."
	case "harness.message.queue_retried":
		return "Повторено %d неудачных задач из очереди."
	case "harness.message.queue_resumed":
		return "Возобновлено %d прерванных задач из очереди."
	case "harness.message.queue_ran":
		return "Выполнено %d задач из очереди."
	case "harness.preview.not_initialized":
		return "Harness ещё не инициализирован в этом проекте.\n\nНажмите Enter или i, чтобы запустить Harness init в текущем репозитории."
	case "harness.preview.check":
		return "Запустить проверки Harness для текущего проекта.\n\nEnter: выполнить необходимые проверки файлов/контента/контекста и настроенные команды валидации."
	case "harness.preview.gc":
		return "Запустить сборку мусора Harness.\n\nEnter: архивировать устаревшие задачи, прервать заблокированные/текущие работы, очистить старые логи и удалить потерянные worktree."
	case "harness.preview.queue_help":
		return "Введите цель Harness здесь, затем нажмите Enter для добавления в очередь."
	case "harness.preview.run_help":
		return "Введите цель Harness здесь, затем нажмите Enter для запуска."
	case "harness.preview.run_queued":
		return "Статус очереди:\nв очереди=%d активно=%d заблокировано=%d неудачных=%d\n\nEnter запускает следующую выполнимую задачу.\na запускает все выполнимые задачи.\nf повторяет неудачные задачи.\ns возобновляет прерванные задачи."
	case "harness.preview.no_owner":
		return "Владелец Harness не выбран."
	case "harness.preview.no_context":
		return "Контекст Harness не выбран."
	case "harness.preview.no_task":
		return "Задача Harness не выбрана."
	case "harness.preview.project_not_initialized":
		return "Harness ещё не инициализирован в этом проекте."
	case "harness.preview.project_initialized":
		return "Harness инициализирован."
	case "harness.preview.project_help":
		return "Используйте /harness для навигации и управления."
	case "harness.preview.no_doctor":
		return "Нет отчёта Doctor."
	case "harness.preview.monitor_unavailable":
		return "Монитор Harness недоступен."
	case "harness.label.context_title":
		return "Контекст"
	case "harness.label.owner_title":
		return "Владелец"
	case "harness.label.id":
		return "id"
	case "harness.label.status":
		return "статус"
	case "harness.label.goal":
		return "цель"
	case "harness.label.attempts":
		return "попытки"
	case "harness.label.depends_on":
		return "зависит_от"
	case "harness.label.context":
		return "контекст"
	case "harness.label.workspace":
		return "воркспейс"
	case "harness.label.branch":
		return "ветвь"
	case "harness.label.worker":
		return "воркер"
	case "harness.label.progress":
		return "прогресс"
	case "harness.label.verification":
		return "верификация"
	case "harness.label.changed_files":
		return "изменённые_файлы"
	case "harness.label.delivery_report":
		return "отчёт_доставки"
	case "harness.label.delivery_report_human":
		return "Отчёт о доставке"
	case "harness.label.log":
		return "лог"
	case "harness.label.review":
		return "ревью"
	case "harness.label.review_notes":
		return "заметки_ревью"
	case "harness.label.promotion":
		return "продвижение"
	case "harness.label.promotion_notes":
		return "заметки_продвижения"
	case "harness.label.release_batch":
		return "партия_релиза"
	case "harness.label.release_batch_human":
		return "Пакет релиза"
	case "harness.label.release_notes":
		return "заметки_релиза"
	case "harness.label.error":
		return "ошибка"
	case "harness.label.name":
		return "имя"
	case "harness.label.description":
		return "описание"
	case "harness.label.owner":
		return "владелец"
	case "harness.label.commands":
		return "команды"
	case "harness.label.tasks":
		return "задачи"
	case "harness.label.rollouts":
		return "роллауты"
	case "harness.label.gates":
		return "гейты"
	case "harness.label.latest":
		return "последний"
	case "harness.label.repo":
		return "репо"
	case "harness.label.config":
		return "конфиг"
	case "harness.label.project":
		return "проект"
	case "harness.label.structure":
		return "структура"
	case "harness.label.contexts":
		return "контексты"
	case "harness.label.workers":
		return "воркеры"
	case "harness.label.workflow":
		return "рабочий_процесс"
	case "harness.label.quality":
		return "качество"
	case "harness.label.worktrees":
		return "ворктри"
	case "harness.label.snapshot":
		return "снапшот"
	case "harness.label.events":
		return "события"
	case "harness.label.target":
		return "цель"
	case "harness.label.review_ready":
		return "готов_к_ревью"
	case "harness.label.promotion_ready":
		return "готов_к_продвижению"
	case "harness.label.retryable":
		return "повторяемый"
	case "harness.task_title":
		return "Задача Harness"
	case "harness.doctor_title":
		return "Диагностика Harness"
	case "harness.monitor_title":
		return "Монитор Harness"
	case "harness.latest_task":
		return "Последняя задача"
	case "harness.latest_event":
		return "Последнее событие"
	case "harness.focus":
		return "Фокус"
	case "harness.status.ok":
		return "ок"
	case "harness.status.needs_attention":
		return "требует внимания"
	case "harness.group.review":
		return "ревью"
	case "harness.group.promotion":
		return "продвижение"
	case "harness.group.retry":
		return "повтор"
	case "harness.review_ready_short":
		return "ревью"
	case "harness.promote_ready_short":
		return "продв."
	case "harness.tasks_count":
		return "Задачи"
	case "harness.input_empty":
		return "(поле ввода пусто)"
	case "harness.no_waves":
		return "нет волн"
	case "harness.mixed":
		return "смешанно"

	// --- Hints ---
	case "hint.autocomplete":
		return "Tab/Shift+Tab переключать • Enter применить • Esc закрыть"
	case "hint.mention":
		return "@ прикрепить файлы/папки • Tab/Shift+Tab переключать • Enter применить"
	case "hint.mode":
		return "Режим"

	// --- Mode approval ---
	case "mode.approval.ask":
		return "спрашивать при необходимости"
	case "mode.approval.none":
		return "без остановок для подтверждения"
	case "mode.approval.critical":
		return "только критические"
	case "mode.tools.rules":
		return "следовать правилам инструментов"
	case "mode.tools.readonly":
		return "только чтение"
	case "mode.tools.safe":
		return "только безопасные операции"
	case "mode.tools.open":
		return "почти все инструменты"
	case "mode.agent.waits":
		return "ожидает вас"
	case "mode.agent.autocontinue":
		return "продолжает работу"

	// --- Hints ---
	case "hint.enter_send":
		return "Enter отправить"
	case "hint.ctrlv_image":
		return "Ctrl+V / Ctrl+Shift+V вставить изображение"
	case "hint.ctrlr_sidebar":
		return "Ctrl+R боковая панель"
	case "hint.help":
		return "/? справка"
	case "hint.add_context":
		return "@ добавить контекст"
	case "hint.scroll":
		return "PgUp/PgDn прокрутка"
	case "hint.shift_tab_mode":
		return "Shift+Tab режим"
	case "hint.ctrlc_cancel":
		return "Ctrl+C отмена"
	case "hint.ctrlc_exit":
		return "Ctrl+C очистить/выйти"
	case "hint.image_attached":
		return "Изображение прикреплено"
	case "hint.image_attached_count":
		return "%d изображ.(й) прикреплено"
	case "hint.follow_panel":
		return "Ctrl+N следовать"
	case "hint.unfollow_panel":
		return "Ctrl+N перестать следовать"

	// --- Queued ---
	case "queued.count":
		return "%d в очереди"
	case "queued.output":
		return "[в очереди %d ожидающих]\n\n"
	case "interrupt.delivered":
		return "[доставлено в активный запуск; план пересматривается]\n"

	// --- Status ---
	case "status.thinking":
		return "Размышление..."
	case "status.writing":
		return "Написание..."
	case "status.cancelling":
		return "Отмена..."
	case "status.compacting":
		return "Компактация контекста..."
	case "status.compacted":
		return "[Беседа скомпактирована]"
	case "reasoning.effort.status":
		return "Усилие размышления: %s"
	case "reasoning.effort.set":
		return "Усилие размышления установлено на %s для этой сессии"
	case "reasoning.effort.unsupported.status":
		return "Усилие размышления не поддерживается текущим поставщиком"
	case "reasoning.effort.unsupported":
		return "Усилие размышления не поддерживается текущим поставщиком"

	// --- Follow ---
	case "follow.loading":
		return "  Загрузка представления следования..."
	case "follow.active_agent":
		return "Следование за агентом %s — ввод приостановлен. Нажмите Esc для возврата."
	case "follow.active_teammate":
		return "Следование за участником %s — ввод приостановлен. Нажмите Esc для возврата."
	case "follow.status_running":
		return "активно"
	case "follow.status_done":
		return "готово"
	case "follow.more":
		return "  +%d ещё"
	case "follow.hint":
		return "  ↑↓←→ переключать  Esc закрыть"

	// --- Tools ---
	case "status.tools_used":
		return "%d инструментов использовано"
	case "tool.done":
		return "готово"
	case "tool.failed":
		return "ошибка"
	case "tool.no_output":
		return "нет вывода"
	case "tool.output":
		return "вывод"
	case "tool.content":
		return "содержимое"
	case "tool.match":
		return "совпадение"
	case "tool.matches":
		return "совпадений"
	case "tool.entry":
		return "запись"
	case "tool.result":
		return "результат"

	// --- Approval ---
	case "approval.rejected":
		return "  Отклонено.\n"
	case "approval.allow":
		return "Разрешить"
	case "approval.allow_always":
		return "Всегда разрешать"
	case "approval.deny":
		return "Отклонить"
	case "approval.accept":
		return "Принять"
	case "approval.reject":
		return "Отклонить"

	// --- Exit ---
	case "exit.confirm":
		return "Нажмите Ctrl-C ещё раз для выхода.\n\n"
	case "cancel.confirm":
		return "Нажмите Ctrl-C или Esc ещё раз для отмены активного агента.\n\n"
	case "interrupted":
		return "[прервано]\n\n"

	// --- Language ---
	case "lang.current":
		return "Текущий язык: %s\nИспользуйте /lang для интерактивного выбора или /lang <en|zh-CN> для прямого переключения.\n%s\n\n"
	case "lang.invalid":
		return "Неподдерживаемый язык: %s\n%s\n\n"
	case "lang.switch":
		return "Язык переключён на: %s\n\n"
	case "lang.selection.current":
		return " Текущий: %s"
	case "lang.selection.hint":
		return " Tab/j/k перемещение • Enter подтвердить • e/z быстрый выбор • Esc отмена"
	case "lang.first_use.title":
		return "Выберите предпочитаемый язык"
	case "lang.first_use.body":
		return " Выберите язык, который ggcode будет использовать далее."
	case "lang.first_use.hint":
		return " Tab/j/k перемещение • Enter подтвердить • e/z быстрый выбор"

	// --- Mode ---
	case "mode.current":
		return "Текущий режим: %s\nИспользование: /mode <supervised|plan|auto|bypass|autopilot>\n  supervised  Спрашивать, когда у инструмента нет явного правила\n  plan        Исследование только для чтения; отклонять запись и команды\n  auto        Разрешать безопасные операции, отклонять опасные\n  bypass      Разрешать почти всё; останавливаться только при критических действиях\n  autopilot   bypass + продолжать, когда модель переспрашивает\n\n"
	case "mode.persist_failed":
		return "Не удалось сохранить настройку режима: %v"

	// --- Input ---
	case "input.placeholder":
		return "Введите сообщение... ($ оболочка, # чат)"

	// --- Model panel ---
	case "panel.model_filter.prompt":
		return "Фильтр> "
	case "panel.model_filter.placeholder":
		return "Печатайте для фильтрации моделей"
	case "panel.model_list.none":
		return "(нет)"
	case "panel.model_list.no_matches":
		return "(нет совпадений)"
	case "panel.model_list.showing":
		return "показано %d/%d моделей"
	case "panel.model_list.hidden_above":
		return "%d выше"
	case "panel.model_list.hidden_more":
		return "%d ещё"

	// --- Provider panel ---
	case "panel.provider.vendors":
		return "Поставщики"
	case "panel.provider.endpoints":
		return "Эндпоинты"
	case "panel.provider.models":
		return "Модели"
	case "panel.provider.active_draft":
		return "Активный черновик"
	case "panel.provider.protocol":
		return "Протокол"
	case "panel.provider.protocol.unknown":
		return "(неизвестно)"
	case "panel.provider.auth":
		return "Аутентификация"
	case "panel.provider.env_var":
		return "Переменная окружения"
	case "panel.provider.api_key":
		return "API-ключ"
	case "panel.provider.api_key.missing":
		return "отсутствует"
	case "panel.provider.api_key.configured":
		return "настроен"
	case "panel.provider.auth.connected":
		return "подключено"
	case "panel.provider.auth.not_connected":
		return "не подключено"
	case "panel.provider.base_url":
		return "Базовый URL"
	case "panel.provider.base_url.not_set":
		return "(не задан)"
	case "panel.provider.enterprise_url":
		return "Корпоративный URL"
	case "panel.provider.tags":
		return "Теги"
	case "panel.provider.model.set_with_m":
		return "(установлено через m)"
	case "panel.provider.edit":
		return "Редактировать"
	case "panel.provider.edit.vendor_api_key":
		return "api-ключ поставщика"
	case "panel.provider.edit.endpoint_api_key":
		return "api-ключ эндпоинта"
	case "panel.provider.edit.endpoint_base_url":
		return "базовый url эндпоинта"
	case "panel.provider.edit.custom_model":
		return "пользовательская модель"
	case "panel.provider.edit.new_endpoint_name":
		return "новое имя эндпоинта"
	case "panel.provider.hint.edit":
		return "Enter сохранить • Esc отмена"
	case "panel.provider.hint.main":
		return "Tab/Shift+Tab сменить фокус • j/k перемещение • / фокус на фильтр • Enter или s применить • a ключ поставщика • u ключ эндпоинта • b базовый URL • m польз. модель • e добавить эндпоинт • Esc закрыть"
	case "panel.provider.hint.copilot":
		return "GitHub Copilot: l вход • x выход • b редактировать Enterprise-домен"
	case "panel.provider.saved":
		return "Сохранено."
	case "panel.provider.saved_activated":
		return "Сохранено и активировано."
	case "panel.provider.login.starting":
		return "Запуск входа GitHub Copilot..."
	case "panel.provider.login.instructions":
		return "Откройте %s и введите код %s. Ожидание авторизации..."
	case "panel.provider.login.copied":
		return "Код устройства скопирован в буфер обмена."
	case "panel.provider.login.copy_failed":
		return "Не удалось скопировать код устройства: %s"
	case "panel.provider.login.browser_opened":
		return "Страница проверки открыта в браузере."
	case "panel.provider.login.browser_failed":
		return "Не удалось открыть страницу проверки: %s"
	case "panel.provider.login.success":
		return "GitHub Copilot подключен."
	case "panel.provider.login.failed":
		return "Ошибка входа GitHub Copilot: %s"
	case "panel.provider.logout.success":
		return "GitHub Copilot отключен."
	case "panel.provider.refreshing_vendor":
		return "Обновление моделей для %s..."
	case "panel.provider.refresh.save_failed":
		return "Модели обновлены, но не удалось сохранить конфигурацию: %s"
	case "panel.provider.refresh.partial":
		return "Обновлено %d эндпоинт(ов), обнаружено %d моделей. Некоторые эндпоинты не удалось обновить: %v"
	case "panel.provider.refresh.success":
		return "Обновлено %d эндпоинт(ов), обнаружено %d моделей."
	case "panel.provider.refresh.failed":
		return "Ошибка обновления моделей: %s"
	case "panel.provider.refresh.none":
		return "Нет обновляемых эндпоинтов для этого поставщика."

	// --- Model panel details ---
	case "panel.model.models":
		return "Модели"
	case "panel.model.vendor":
		return "Поставщик"
	case "panel.model.endpoint":
		return "Эндпоинт"
	case "panel.model.protocol":
		return "Протокол"
	case "panel.model.source":
		return "Источник"
	case "panel.model.source.builtin":
		return "встроенный"
	case "panel.model.source.remote":
		return "удалённый"
	case "panel.model.refreshing":
		return "Обновление актуальных моделей..."
	case "panel.model.hint.main":
		return "j/k перемещение • Enter или s применить • w контекстное окно • o макс. токены • r обновить • / фильтр • Esc закрыть"
	case "panel.model.hint.edit":
		return "Enter сохранить • Esc отмена (0 или пусто = авто, суффиксы K/M/G допустимы, например 256k)"
	case "panel.model.context_window":
		return "Контекстное окно"
	case "panel.model.max_tokens":
		return "Макс. выходных токенов"
	case "panel.model.edit":
		return "Редактировать"
	case "panel.model.saved_runtime_inactive":
		return "Конфигурация сохранена, но текущая среда ещё неактивна: %s"
	case "panel.model.context_applied":
		return "context_window=%d, max_tokens=%d применено (сохранено)"
	case "panel.model.context_cleared":
		return "Сброшено на автоопределение (сохранено)"
	case "panel.model.switched":
		return "Модель переключена на %s."
	case "panel.model.refresh.save_failed":
		return "Модели обновлены, но не удалось сохранить конфигурацию: %s"
	case "panel.model.refresh.builtin_reason":
		return "Использованы встроенные модели: %s"
	case "panel.model.refresh.remote_loaded":
		return "%d удалённых моделей загружено."
	case "panel.model.refresh.builtin_loaded":
		return "Встроенные модели загружены."

	// --- Commands ---
	case "command.unknown":
		return "Неизвестная команда: %s\n"
	case "command.retry_empty":
		return "Нет предыдущего ввода для повтора."
	case "command.retry_busy":
		return "Агент занят. Дождитесь завершения текущего запуска перед повтором."
	case "command.edit_empty":
		return "Нет предыдущего ввода для редактирования."
	case "command.edit_busy":
		return "Агент занят. Дождитесь завершения текущего запуска перед редактированием."
	case "command.edit_ready":
		return "Последний ввод загружен — отредактируйте и нажмите Enter для отправки."
	case "command.help_hint":
		return "Введите /help для списка доступных команд\n\n"
	case "command.usage.allow":
		return "Использование: /allow <имя_инструмента>\n\n"
	case "command.usage.resume":
		return "Использование: /resume <id_сессии>\n\n"
	case "command.usage.export":
		return "Использование: /export <id_сессии>\n\n"

	// --- Init ---
	case "init.resolve_failed":
		return "Не удалось разрешить цель init: %v\n\n"
	case "init.generate_failed":
		return "Не удалось сгенерировать содержимое GGCODE.md: %v\n\n"
	case "init.collecting":
		return "Сбор знаний о проекте..."
	case "init.prompt.title":
		return "Инициализация проекта"
	case "init.prompt.body":
		return "GGCODE.md не найдена в этом проекте. Создать, чтобы агент понимал конвенции вашей кодовой базы?"
	case "init.prompt.yes":
		return "Создать"
	case "init.prompt.no":
		return "Пропустить"
	case "init.prompt.hint":
		return " y = создать GGCODE.md • n/Esc = пропустить"

	// --- Model commands ---
	case "command.model_switched":
		return "Модель переключена на: %s (поставщик: %s)\n\n"
	case "command.model_failed":
		return "Ошибка переключения модели: %v\n\n"
	case "command.model_current":
		return "Текущая модель: %s (поставщик: %s)\nДоступные модели: %s\nИспользуйте /model для панели или /model <имя> для прямого переключения.\n\n"
	case "command.provider_unknown":
		return "Неизвестный поставщик: %s (доступно: %v)\n\n"
	case "command.provider_switched":
		return "Поставщик переключен на: %s (модель: %s)\n\n"
	case "command.provider_failed":
		return "Не удалось обновить выбор поставщика: %v\n\n"
	case "command.provider_current":
		return "Текущий поставщик: %s (эндпоинт: %s, модель: %s)\nДоступные поставщики: %s\nДоступные эндпоинты: %s\nИспользование: /provider [поставщик] [эндпоинт]\n\n"
	case "command.allow_set":
		return "✓ %s теперь всегда разрешён\n\n"
	case "command.custom":
		return "Пользовательская команда /%s:\n"
	case "command.mention_error":
		return "Ошибка расширения упоминания: %v"

	// --- Sessions ---
	case "session.list_failed":
		return "Ошибка получения списка сессий: %v\n\n"
	case "session.untitled":
		return "без названия"
	case "session.store_missing":
		return "Хранилище сессий не настроено.\n\n"
	case "session.none":
		return "Сессии не найдены.\n\n"
	case "session.list.title":
		return "Сессии:\n\n"
	case "session.list.item":
		return "  %d. %s  %s  (%s)\n"
	case "session.list.hint":
		return "\nИспользуйте /resume <id> для продолжения сессии\n\n"
	case "session.new":
		return "Новая сессия: %s\n\n"
	case "session.resume":
		return "Сессия возобновлена: %s — %s (%d сообщений)\n\n"
	case "session.resume_failed":
		return "Ошибка возобновления сессии %s: %v\n\n"
	case "session.resume_fallback":
		return "Вместо этого запускается новая сессия.\n\n"
	case "session.export_failed":
		return "Ошибка экспорта сессии: %v\n\n"
	case "session.write_failed":
		return "Ошибка записи файла: %v\n\n"
	case "session.exported":
		return "Сессия %s экспортирована в %s\n\n"

	// --- Checkpoints ---
	case "checkpoint.disabled":
		return "Контрольные точки не включены.\n\n"
	case "checkpoint.undo_failed":
		return "Отмена не удалась: %v\n\n"
	case "checkpoint.undid":
		return "Отменено: %s на %s (контрольная точка %s)\n"
	case "checkpoint.none":
		return "Нет контрольных точек.\n\n"

	// --- Files ---
	case "files.disabled":
		return "Контрольные точки не включены.\n\n"
	case "files.none":
		return "Нет файлов, изменённых агентом в этой сессии.\n\n"
	case "files.title":
		return "Файлы, изменённые агентом (%d файлов, %d правок):\n\n"
	case "files.item":
		return "  %s  %d правок  последняя: %s%s\n"
	case "files.hint":
		return "\nИспользуйте /undo для отмены последней правки, /checkpoints для деталей.\n\n"
	case "checkpoint.list.title":
		return "Контрольные точки (%d):\n\n"
	case "checkpoint.list.item":
		return "  %d. %s  %s  %s  %s\n"
	case "checkpoint.list.hint":
		return "\nИспользуйте /undo для отмены последней.\n\n"

	// --- Memory ---
	case "memory.auto_unavailable":
		return "Авто-память не инициализирована.\n\n"
	case "memory.list_failed":
		return "Ошибка получения списка воспоминаний: %v\n\n"
	case "memory.none":
		return "Нет сохранённых авто-воспоминаний.\n\n"
	case "memory.auto_title":
		return "Авто-воспоминания:\n"
	case "memory.clear_failed":
		return "Ошибка очистки воспоминаний: %v\n\n"
	case "memory.cleared":
		return "Все авто-воспоминания очищены.\n\n"
	case "memory.title":
		return "Память:\n"
	case "memory.project":
		return "Память проекта:\n"
	case "memory.project_none":
		return "  Нет загруженных файлов памяти проекта.\n"
	case "memory.auto":
		return "Авто-память:\n"
	case "memory.auto_none":
		return "  Нет загруженных авто-воспоминаний.\n"
	case "memory.usage":
		return "\nИспользование: /memory [list|clear]\n\n"

	// --- Compact ---
	case "compact.unavailable":
		return "Менеджер контекста недоступен.\n\n"
	case "compact.failed":
		return "Компактация не удалась: %v\n\n"
	case "compact.done":
		return "История беседы скомпактирована.\n\n"
	case "compact.done_with_stats":
		return "История беседы скомпактирована (%d → %d токенов).\n\n"

	// --- Todo ---
	case "todo.cleared":
		return "Список задач очищен.\n\n"
	case "todo.clear_failed":
		return "Ошибка очистки задач: %v\n\n"
	case "todo.none":
		return "Список задач не найден. Используйте инструмент todo_write для создания.\n\n"
	case "todo.read_failed":
		return "Ошибка чтения задач: %v\n\n"
	case "todo.parse_failed":
		return "Ошибка разбора задач: %v\n\n"
	case "todo.title":
		return "Список задач:\n%s\n\n"

	// --- Bug report ---
	case "bug.title":
		return "=== Диагностика баг-репорта ===\n\n"
	case "bug.version":
		return "Версия: %s\n"
	case "bug.os":
		return "ОС: %s %s\n"
	case "bug.go":
		return "Go: %s\n"
	case "bug.provider":
		return "Поставщик: %s\n"
	case "bug.model":
		return "Модель: %s\n"
	case "bug.session":
		return "Сессия: %s (%d сообщений)\n"
	case "bug.mcp":
		return "MCP-серверы: %d\n"
	case "bug.last_error":
		return "Последняя ошибка: %s\n"
	case "bug.hint":
		return "\nПожалуйста, предоставьте эту информацию при сообщении об ошибке.\n\n"

	// --- Config ---
	case "config.usage":
		return "Использование: /config set <ключ> <значение>\n\nКлючи: model, vendor, endpoint, language, apikey [--vendor]\n\nЭндпоинты: /config add-endpoint <имя> <base_url> [--protocol openai] [--apikey sk-xxx]\n          /config remove-endpoint <имя>\n\n"
	case "config.not_loaded":
		return "Конфигурация не загружена.\n\n"
	case "config.model_set":
		return "Конфиг: model = %s\n\n"
	case "config.provider_set":
		return "Конфиг: поставщик = %s\n\n"
	case "config.language_set":
		return "Конфиг: язык = %s\n\n"
	case "config.unknown_key":
		return "Неизвестный ключ конфигурации: %s\nПоддерживаются: model, provider, language\n\n"
	case "config.title":
		return "Текущая конфигурация:\n"
	case "status.title":
		return "Статус:\n"

	// --- Update ---
	case "panel.update":
		return "Обновление"
	case "label.version":
		return "Версия"
	case "label.latest":
		return "Последняя"
	case "update.sidebar_hint":
		return "Доступен новый релиз. Запустите /update."
	case "update.up_to_date":
		return "У вас последняя версия."
	case "update.available":
		return "Доступно обновление: %s"
	case "update.current":
		return "текущая: %s (последняя: %s)"
	case "update.unknown":
		return "ещё не проверено"
	case "update.check_failed":
		return "Проверка не удалась: %s"
	case "update.unavailable":
		return "Обновление недоступно в этой сессии.\n\n"
	case "update.preparing":
		return "Подготовка обновления"
	case "update.failed":
		return "Обновление не удалось: %v\n\n"
	case "update.restart_failed":
		return "Обновление подготовлено, но перезапуск не удался: %v\n\n"
	case "update.pm_hint.brew":
		return "Обновление установлено. Примечание: ggcode был установлен через Homebrew.\nВыполните `brew upgrade ggcode`, чтобы синхронизировать Homebrew.\n\n"
	case "update.pm_hint.scoop":
		return "Обновление установлено. Примечание: ggcode был установлен через Scoop.\nВыполните `scoop update ggcode`, чтобы синхронизировать Scoop.\n\n"
	case "update.pm_hint.winget":
		return "Обновление установлено. Примечание: ggcode был установлен через winget.\nВыполните `winget upgrade ggcode`, чтобы синхронизировать winget.\n\n"
	case "update.pm_hint.snap":
		return "Обновление установлено. Примечание: ggcode был установлен через Snap.\nВыполните `sudo snap refresh ggcode`, чтобы синхронизировать Snap.\n\n"
	case "update.other_installs":
		return "Найдены другие установки ggcode в этой системе:\n%s\nЕсли другая установка ggcode появляется раньше в PATH, рассмотрите возможность её обновления или изменения порядка PATH.\n\n"
	case "update.dual_scope":
		return "Предупреждение: Найдены как пользовательская, так и системная установки ggcode:\n  Пользовательская: %s\n  Системная: %s\nЭто может привести к конфликтам PATH. Рассмотрите удаление одной через Настройки > Приложения.\n\n"

	// --- Plugins ---
	case "plugins.unavailable":
		return "Менеджер плагинов недоступен.\n\n"
	case "plugins.none":
		return "Плагины не загружены.\n\n"
	case "plugins.title":
		return "Плагины:\n"

	// --- MCP ---
	case "mcp.none":
		return "MCP-серверы не настроены.\n\n"
	case "mcp.title":
		return "MCP-серверы:\n"
	case "mcp.active_tools":
		return "Активные инструменты"
	case "mcp.more":
		return "… %d ещё • /mcp"

	// --- Image ---
	case "image.usage":
		return "Использование: /image <путь/к/файлу.png> или /image paste\n"
	case "image.formats":
		return "Поддерживаемые форматы: PNG, JPEG, GIF, WebP (макс. 20МБ)\n\n"
	case "image.attached":
		return "Изображение прикреплено: %s\n"
	case "image.attached_hint":
		return "Отправьте сообщение, чтобы включить изображение, или /image для ещё одного.\n\n"
	case "image.clipboard_failed":
		return "Не удалось вставить изображение из буфера обмена: %v"
	case "image.clipboard_no_image_windows":
		return "Изображение в буфере обмена не найдено. В Windows Ctrl+V часто перехватывается терминалом. Попробуйте Ctrl+Shift+V или /image paste."

	// --- Agents ---
	case "agents.unavailable":
		return "Менеджер суб-агентов не настроен.\n\n"
	case "agents.none":
		return "Суб-агенты ещё не созданы.\nИспользование: LLM может использовать инструмент spawn_agent для создания суб-агентов.\n\n"
	case "agents.title":
		return "%d суб-агент(ов):\n"
	case "agents.item":
		return "  %s [%s]%s — %s\n"
	case "agents.hint":
		return "\nИспользуйте /agent <id> для деталей, /agent cancel <id> для отмены.\n\n"
	case "agent.usage":
		return "Использование: /agent <id> или /agent cancel <id>\n\n"
	case "agent.cancelled":
		return "Суб-агент %s отменён\n\n"
	case "agent.cancel_failed":
		return "Не удалось отменить %s (не найден или не активен)\n\n"
	case "agent.not_found":
		return "Суб-агент %s не найден\n\n"
	case "agent.title":
		return "Агент: %s\nСтатус: %s\nЗадача: %s\n"
	case "agent.result":
		return "Результат: %s\n"
	case "agent.error":
		return "Ошибка: %v\n"

	// --- Slash command descriptions ---
	case "slash.help":
		return "Показать справку"
	case "slash.sessions":
		return "Показать сохранённые сессии"
	case "slash.resume":
		return "Возобновить предыдущую сессию"
	case "slash.export":
		return "Экспортировать сессию в Markdown"
	case "slash.model":
		return "Сменить модель"
	case "slash.provider":
		return "Открыть менеджер поставщиков"
	case "slash.clear":
		return "Очистить беседу"
	case "slash.mcp":
		return "Показать MCP-серверы"
	case "slash.memory":
		return "Управление памятью"
	case "slash.undo":
		return "Отменить последнюю правку файла"
	case "slash.files":
		return "Показать изменённые агентом файлы"
	case "slash.checkpoints":
		return "Показать контрольные точки"
	case "slash.allow":
		return "Всегда разрешать инструмент"
	case "slash.plugins":
		return "Показать загруженные плагины"
	case "slash.image":
		return "Прикрепить изображение"
	case "slash.init":
		return "Сгенерировать GGCODE.md для проекта"
	case "slash.harness":
		return "Выполнить команды Harness workflow"
	case "slash.lang":
		return "Сменить язык интерфейса"
	case "slash.skills":
		return "Просмотр доступных навыков"
	case "slash.exit":
		return "Выйти из ggcode"
	case "slash.compact":
		return "Компактировать историю беседы"
	case "slash.todo":
		return "Просмотр/управление списком задач"
	case "slash.bug":
		return "Сообщить об ошибке"
	case "slash.config":
		return "Просмотр/изменение конфигурации"
	case "slash.qq":
		return "Управление привязкой QQ-канала"
	case "slash.telegram":
		return "Управление привязкой Telegram-канала"
	case "slash.pc":
		return "Управление привязкой PC-канала"
	case "slash.discord":
		return "Управление привязкой Discord-канала"
	case "slash.feishu":
		return "Управление привязкой Feishu-канала"
	case "slash.slack":
		return "Управление привязкой Slack-канала"
	case "slash.dingtalk":
		return "Управление привязкой DingTalk-канала"
	case "slash.wechat":
		return "Управление привязкой WeChat-канала"
	case "slash.wecom":
		return "Управление привязкой WeCom (Enterprise WeChat)"
	case "slash.mattermost":
		return "Управление привязкой Mattermost-канала"
	case "slash.matrix":
		return "Управление привязкой Matrix-канала"
	case "slash.signal":
		return "Управление привязкой Signal-канала"
	case "slash.irc":
		return "Управление привязкой IRC-канала"
	case "slash.nostr":
		return "Управление привязкой Nostr-канала"
	case "slash.twitch":
		return "Управление привязкой Twitch-канала"
	case "slash.whatsapp":
		return "Управление привязкой WhatsApp-канала"
	case "slash.impersonate":
		return "Имитировать CLI-инструмент для отображения в shell-промпте"
	case "slash.knight":
		return "Управление автономным фоновым агентом"
	case "slash.stream":
		return "Настройка режима потокового вывода"
	case "slash.diff":
		return "Показать git diff в чате (поддержка --cached, <file>, --stat)"
	case "slash.hooks":
		return "Показать настроенные хуки (все события, типы, шаблоны)"
	case "slash.cost":
		return "Показать использование токенов и оценочную стоимость"
	case "slash.review":
		return "AI-ревью текущих изменений (баги, безопасность, гонки)"
	case "slash.copy":
		return "Скопировать последний ответ ассистента в буфер обмена"
	case "slash.context":
		return "Показать разбивку контекстного окна (токены, сообщения, ёмкость)"
	case "slash.im":
		return "Открыть единую панель IM-каналов"

	// --- QQ panel ---
	case "panel.qq.directory":
		return "Каталог"
	case "panel.qq.runtime":
		return "Среда"
	case "panel.qq.bots":
		return "QQ-боты"
	case "panel.qq.created":
		return "Создано: %d"
	case "panel.qq.bound":
		return "Привязано: %d"
	case "panel.qq.available":
		return "Доступно: %d"
	case "panel.qq.current_binding":
		return "Текущая привязка"
	case "panel.qq.none":
		return "(нет)"
	case "panel.qq.default":
		return "(по умолчанию)"
	case "panel.qq.adapter":
		return "Адаптер: %s"
	case "panel.qq.target":
		return "Цель: %s"
	case "panel.qq.channel":
		return "Канал: %s"
	case "panel.qq.bot_list":
		return "Список QQ-ботов"
	case "panel.qq.no_bots":
		return "QQ-боты не настроены."
	case "panel.qq.entry.available":
		return "Доступен"
	case "panel.qq.entry.bound":
		return "Привязан"
	case "panel.qq.entry.active":
		return "Активен"
	case "panel.qq.entry.bound_other":
		return "Привязан: %s"
	case "panel.qq.entry.muted":
		return "Заглушен"
	case "panel.qq.details":
		return "Детали"
	case "panel.qq.status":
		return "Статус: %s"
	case "panel.qq.transport":
		return "Транспорт: %s"
	case "panel.qq.bound_directory":
		return "Привязанный каталог: %s"
	case "panel.qq.current_directory_target":
		return "Цель текущего каталога: %s"
	case "panel.qq.current_directory_channel":
		return "Канал текущего каталога: %s"
	case "panel.qq.waiting_for_pairing":
		return "(ожидание сопряжения)"
	case "panel.qq.last_error":
		return "Последняя ошибка: %s"
	case "panel.qq.occupied_by":
		return "Занят: %s"
	case "panel.qq.create":
		return "Создать"
	case "panel.qq.bot_input":
		return "QQ-бот: %s"
	case "panel.qq.create_format":
		return "Формат: <bot-id> <appid> <appsecret>"
	case "panel.qq.create_example":
		return "Пример: qq-main 123456 secret-value"
	case "panel.qq.create_hint":
		return "Enter создать бота • Esc отмена"
	case "panel.qq.actions_hint":
		return "j/k перемещение • Enter или b привязать бота • c привязать канал • x отвязать канал • u отвязать бота • i создать бота • Esc закрыть"
	case "panel.qq.bind_channel":
		return "Привязать канал"
	case "panel.qq.scan_hint":
		return "Отсканируйте QR-код, добавьте бота, затем отправьте сообщение для начала сопряжения."
	case "panel.qq.qr_code":
		return "QR-код:"
	case "panel.qq.share_link":
		return "Ссылка:"
	case "panel.qq.message.no_bot":
		return "Нет доступных QQ-ботов."
	case "panel.qq.message.bound_success":
		return "QQ-бот привязан к текущей рабочей области. Используйте c для генерации QR-кода привязки канала."
	case "panel.qq.message.share_generated":
		return "Сгенерирована ссылка QQ. Отсканируйте QR-код, добавьте бота, затем отправьте сообщение для начала сопряжения."
	case "panel.qq.message.unbound":
		return "QQ-канал отвязан."
	case "panel.qq.message.cleared":
		return "Авторизация QQ-канала для текущей рабочей области очищена."
	case "panel.qq.message.added_bot":
		return "QQ-бот %s добавлен."
	case "panel.qq.error.config_unavailable":
		return "Конфигурация недоступна"
	case "panel.qq.error.config_format":
		return "Конфигурация QQ-бота должна быть: <bot-id> <appid> <appsecret>"
	case "panel.qq.error.adapter_required":
		return "Требуется имя QQ-адаптера"
	case "panel.qq.error.not_configured":
		return "QQ-бот %q не настроен"
	case "panel.qq.error.disabled":
		return "QQ-бот %q отключён"
	case "panel.qq.error.not_qq_adapter":
		return "Адаптер %q не является QQ-ботом"
	case "panel.qq.error.not_online":
		return "QQ-бот %q не в сети"
	case "panel.qq.error.not_online_detail":
		return "QQ-бот %q не в сети: %s"
	case "panel.qq.runtime.available":
		return "доступна"
	case "panel.qq.runtime.disabled":
		return "отключено (установите im.enabled: true и перезапустите ggcode)"
	case "panel.qq.runtime.not_started":
		return "не запущено (перезапустите ggcode для инициализации IM-среды)"
	case "panel.qq.status.not_started":
		return "не запущено"
	case "panel.qq.status.unknown":
		return "неизвестно"

	// --- More slash commands ---
	case "slash.status":
		return "Показать текущий статус"
	case "slash.update":
		return "Обновить ggcode"
	case "slash.cron":
		return "Управление задачами cron (list, pause, resume, create)"
	case "slash.branch":
		return "Создать ветку текущей сессии (форк беседы)"
	case "slash.chat":
		return "Открыть панель LAN-чата"
	case "slash.edit":
		return "Редактировать последнее сообщение и отправить заново"
	case "slash.inspector":
		return "Переключить панель инспектора"
	case "slash.mode":
		return "Показать или сменить режим разрешений"
	case "slash.nick":
		return "Установить ник для LAN-чата"
	case "slash.reflect":
		return "Выполнить саморефлексию текущей сессии"
	case "slash.regenerate":
		return "Перегенерировать последний ответ AI (отменить и повторить)"
	case "slash.restart":
		return "Перезапустить процесс ggcode"
	case "slash.retry":
		return "Повторить последний неудачный запуск агента"
	case "slash.rules":
		return "Показать изученные ratchet-правила"
	case "slash.share":
		return "Поделиться сессией через туннель (мобильный релей)"
	case "slash.stats":
		return "Показать статистику сессии (токены, итерации, инструменты)"
	case "slash.tmux":
		return "Открыть меню управления tmux-панелями"
	case "slash.tunnel":
		return "Переключить туннельное соединение для мобильного релея"
	case "slash.unshare":
		return "Остановить совместный доступ к сессии через туннель"

	// --- Regenerate ---
	case "regenerate.busy":
		return "Невозможно перегенерировать во время работы агента. Сначала нажмите Ctrl+C для отмены."
	case "regenerate.no_agent":
		return "Агент не инициализирован."
	case "regenerate.no_context":
		return "Менеджер контекста недоступен."
	case "regenerate.no_response":
		return "Нет ответа ассистента для перегенерации."

	// --- Branch ---
	case "branch.busy":
		return "Невозможно создать ветку во время работы агента. Сначала нажмите Ctrl+C для отмены."
	case "branch.no_session":
		return "Нет активной сессии для ветвления."
	case "branch.empty":
		return "В сессии нет сообщений для ветвления."
	case "branch.save_failed":
		return "Не удалось создать ответвлённую сессию: %v"
	case "branch.success":
		return "Создано ответвление в новую сессию %s (из: %s). Исходная сессия сохранена."

	// --- Help text ---
	case "help.text":
		return `Доступные команды:

Сессия и история:
  /help, /?          Показать эту справку
  /sessions          Показать все сохранённые сессии
  /resume <id>       Возобновить предыдущую сессию
  /export <id>       Экспортировать сессию в Markdown
  /clear             Очистить историю беседы
  /compact           Компактировать историю беседы (вручную)
  /undo              Отменить последнюю правку файла (откат чекпойнта)
  /checkpoints       Показать все чекпойнты правок файлов
  /regenerate        Отменить и перегенерировать последний ответ (псевдоним: /regen)
  /branch            Форк текущей беседы в новую сессию (псевдоним: /fork)

Модель и поставщик:
  /model [name]      Открыть панель моделей или переключить напрямую
  /provider [vendor] Открыть менеджер поставщиков
  /mode <mode>       Установить режим агента (supervised|plan|auto|bypass|autopilot)

Разработка:
  /diff [opts]       Показать git diff в чате (--cached, --stat, <file>)
  /review [opts]     AI-ревью текущих изменений (--cached, --staged)
  /copy              Скопировать последний ответ ассистента в буфер обмена
  /cost              Показать использование токенов и оценочную стоимость
  /context           Показать разбивку контекстного окна
  /hooks             Показать настроенные хуки
  /init              Сгенерировать GGCODE.md из текущего проекта
  /harness ...       Выполнить команды Harness
  /todo              Показать список задач
  /todo clear        Очистить список задач

Интеграции:
  /im                Открыть единую панель IM-каналов
  /mcp               Показать подключённые MCP-серверы и инструменты
  /plugins           Показать загруженные плагины и их инструменты
  /skills            Просмотр доступных навыков
  /memory            Показать загруженные файлы памяти
  /agents            Показать суб-агентов
  /cron <sub>        Управление задачами (list|get|pause|resume|pauseall|resumeall)

Система:
  /lang [code]       Выбрать или сменить язык интерфейса
  /config            Показать текущую конфигурацию
  /config set <k> <v> Установить значение конфигурации
  /status            Показать текущий статус
  /update            Обновить ggcode до последней версии
  /restart           Перезапустить ggcode (загрузить последний бинарник)
  /bug               Сообщить об ошибке с диагностикой
  /exit, /quit       Выйти из ggcode

Горячие клавиши:
  Tab                Автодополнение или переключение опций подтверждения
  Shift+Tab          Переключение назад, или переключение режима разрешений
  Ctrl+R             Переключить боковую панель
  Ctrl+N/P           Новая/предыдущая сессия
  Ctrl+T             Открыть туннель (мобильный доступ)
  Enter              Отправить сообщение / применить выбор
  Esc                Отменить автодополнение / выйти из режима shell
  Up/Down            История команд (или автодополнение)
  PgUp/PgDn          Прокрутка вывода беседы
  Ctrl+C             Отменить текущую активность, иначе очистить ввод, затем ещё раз для выхода
  Ctrl+D             Немедленный выход
  Ctrl+A / Ctrl+E    Переместить курсор в начало / конец строки
  Ctrl+K             Удалить от курсора до конца строки
  Ctrl+U             Удалить от начала строки до курсора
  Ctrl+W             Удалить слово перед курсором
  Ctrl+Backspace     Удалить последнее прикреплённое изображение
  Shift+Enter        Вставить перенос строки (Ctrl+J или Alt+Enter в tmux)
  $ / !              Активировать режим оболочки
  #                  Активировать режим быстрой отправки LAN-чата

Мышь:
  Option+перетаскивание / Shift+перетаскивание  Выделить текст для копирования`

	// --- Harness commands ---
	case "command.harness_usage":
		return "Использование: /harness <init|check|queue|tasks|run|rerun|run-queued|monitor|contexts|inbox|review|promote|release|gc|doctor> ... (release поддерживает rollouts|advance|pause|resume|abort|approve|reject)"
	case "command.harness_queue_usage":
		return "Использование: /harness queue <цель>"
	case "command.harness_run_usage":
		return "Использование: /harness run <цель>"
	case "command.harness_rerun_usage":
		return "Использование: /harness rerun <task-id>"
	case "command.skill_agent_only":
		return "Навык %s может быть вызван только агентом."
	case "command.harness_owner_promoted":
		return "Продвинуто %d задач(и) Harness для владельца %s."
	case "command.harness_review_approved":
		return "Задача Harness %s одобрена."
	case "command.harness_review_rejected":
		return "Задача Harness %s отклонена."
	case "command.harness_promoted_many":
		return "Продвинуто %d задач(и) Harness."
	case "command.harness_promoted_one":
		return "Задача Harness %s продвинута."
	case "command.harness_task_queued_detail":
		return "Задача Harness %s добавлена в очередь.\n- Цель: %s"
	case "command.harness_tasks_empty":
		return "Нет записанных задач Harness."
	case "command.harness_run_start":
		return "Запуск отслеживаемого Harness-запуска...\nИспользуйте /harness monitor или представления задач/монитора для статуса в реальном времени."
	case "command.harness_rerun_start":
		return "Запуск отслеживаемого повторного Harness-запуска...\nИспользуйте /harness monitor или представления задач/монитора для статуса в реальном времени."
	case "command.harness_rerun_invalid_status":
		return "Задача Harness %s имеет статус %s; можно повторять только неудачные задачи."
	case "command.harness_status_starting_run":
		return "Запуск Harness..."
	case "command.harness_status_starting_rerun":
		return "Повторный запуск Harness..."
	case "command.harness_spinner_running":
		return "Harness выполняется"
	case "command.harness_cancelled":
		return "Harness-запуск отменён."

	// --- Tunnel ---
	case "tunnel.stopped":
		return "Туннель остановлен."
	case "tunnel.not_active":
		return "Нет активной сессии совместного доступа."
	case "tunnel.mobile_connected":
		return "Мобильный клиент подключен."

	// --- Config save scope ---
	case "config.save_scope_global":
		return "Цель сохранения → Глобально"
	case "config.save_scope_instance":
		return "Цель сохранения → Экземпляр"
	case "config.save_scope_instance_new":
		return "Цель сохранения → Экземпляр (новая конфигурация будет создана)"
	case "config.instance_unavailable":
		return "Конфигурация экземпляра недоступна для этой рабочей области"
	case "config.scope_instance":
		return "Экземпляр"
	case "config.scope_global":
		return "Глобально"
	case "config.save_target_new_hint":
		return " (новая конфигурация будет создана при сохранении)"
	case "config.save_target_line":
		return " Цель сохранения: %s%s  [Ctrl+T переключить]"

	// --- Shell ---
	case "shell.empty":
		return "Команда оболочки пуста."

	// --- LAN Chat ---
	case "lanchat.unavailable":
		return "LAN-чат недоступен."

	// --- Reflect ---
	case "reflect.no_agent":
		return "Агент не инициализирован."
	case "reflect.no_workdir":
		return "Рабочий каталог не установлен."
	case "reflect.no_memory":
		return "Память проекта недоступна для этого каталога."
	case "reflect.load_failed":
		return "Не удалось загрузить инсайты: %v"
	case "reflect.empty":
		return "Пока нет инсайтов запуска. Инсайты автоматически генерируются после каждого запуска агента с 3+ вызовами инструментов или правками файлов."
	case "reflect.title":
		return "## Накопленные инсайты запуска\n\n"
	case "reflect.memory_location":
		return "Расположение памяти: %s\n"

	// --- Knight ---
	case "knight.unavailable":
		return "Knight недоступен"

	// --- Pairing ---
	case "pairing.rejected":
		return "Текущий запрос на сопряжение был отклонён. Инициируйте заново для продолжения."
	case "pairing.blacklisted":
		return "Этот канал добавлен в чёрный список из-за многократных отказов."

	default:
		return enCatalog(key)
	}
}
