package tui

// Korean translations for TUI i18n.
// All 722 keys translated. Untranslated patterns fall back to enCatalog.

func koCatalog(key string) string {
	switch key {
	case "workspace.tagline":
		return "AI 코딩 어시스턴트"
	case "header.terminal_native":
		return "네이티브 터미널"
	case "session.ephemeral":
		return "임시"
	case "agents.idle":
		return "대기 중"
	case "agents.running":
		return "실행 중"
	case "cron.firing":
		return "실행 중"
	case "activity.idle":
		return "대기 중"
	case "panel.conversation":
		return "대화"
	case "panel.composer":
		return "입력"
	case "panel.composer_locked":
		return "잠김"
	case "panel.commands":
		return "명령"
	case "panel.files":
		return "파일"
	case "panel.agent_status":
		return "에이전트 상태"
	case "panel.mode_policy":
		return "모드"
	case "panel.session_usage":
		return "세션 사용량"
	case "panel.metrics":
		return "메트릭"
	case "panel.context":
		return "컨텍스트"
	case "panel.im":
		return "IM"
	case "panel.mcp":
		return "MCP"
	case "panel.mcp.install_spec_required":
		return "설치 사양을 먼저 입력하세요."
	case "panel.mcp.installing_server":
		return "MCP 서버 설치 중..."
	case "panel.mcp.reconnect_unavailable":
		return "이 세션에서는 재연결을 사용할 수 없습니다."
	case "panel.mcp.reconnecting":
		return "%s 재연결 중..."
	case "panel.mcp.reconnect_failed":
		return "%s 재연결 불가"
	case "panel.mcp.uninstalling":
		return "%s 제거 중..."
	case "panel.startup":
		return "시작 중..."
	case "panel.approval_required":
		return "승인 필요"
	case "panel.bypass_approval":
		return "승인 바이패스"
	case "panel.review_file_change":
		return "변경 확인"
	case "label.vendor":
		return "벤더"
	case "label.endpoint":
		return "엔드포인트"
	case "label.model":
		return "모델"
	case "label.mode":
		return "모드"
	case "label.session":
		return "세션"
	case "label.agents":
		return "에이전트"
	case "label.cwd":
		return "작업 디렉토리"
	case "label.branch":
		return "브랜치"
	case "label.context":
		return "컨텍스트"
	case "label.skills":
		return "스킬"
	case "label.activity":
		return "활동"
	case "label.window":
		return "창"
	case "label.usage":
		return "사용량"
	case "label.compact":
		return "압축"
	case "label.total":
		return "합계"
	case "label.cost":
		return "예상 비용"
	case "label.approval_policy":
		return "승인"
	case "label.tool_policy":
		return "도구"
	case "label.agent_policy":
		return "에이전트"
	case "label.tool":
		return "도구"
	case "label.input":
		return "입력"
	case "label.output":
		return "출력"
	case "label.cache_read":
		return "캐시 읽기"
	case "label.cache_write":
		return "캐시 쓰기"
	case "label.cache_hit":
		return "캐시 적중"
	case "label.turns":
		return "턴"
	case "label.avg_ttft":
		return "평균 TTFT"
	case "label.p95_ttft":
		return "P95 TTFT"
	case "label.avg_duration":
		return "평균 소요"
	case "label.p95_duration":
		return "P95 소요"
	case "label.avg_think":
		return "평균 사고"
	case "label.fail_rate":
		return "실패율"
	case "label.slow_tools":
		return "느린 도구"
	case "label.recent_turns":
		return "최근 턴"
	case "label.file":
		return "파일"
	case "label.directory":
		return "디렉토리"
	case "context.unavailable":
		return "아직 컨텍스트 데이터가 없습니다"
	case "metrics.empty":
		return "메트릭이 없습니다"
	case "im.none":
		return "IM 어댑터 없음"
	case "im.summary":
		return "%d 어댑터 • %d 정상"
	case "im.more":
		return "+%d 더보기 (/qq)"
	case "im.runtime.available":
		return "런타임 사용 가능"
	case "im.runtime.disabled":
		return "비활성화"
	case "im.runtime.not_started":
		return "활성화됨 • 초기화하려면 재시작"
	case "im.status.not_started":
		return "시작되지 않음"
	case "context.until_compact":
		return "남음"
	case "empty.ask":
		return "리팩토링, 버그 수정, 설명 또는 테스트를 요청하세요."
	case "empty.tips":
		return "팁: @path로 파일 포함, /?로 도움말, Shift+Tab으로 모드 변경."
	case "startup.banner":
		return "터미널 UI를 준비하고 시작 터미널 노이즈를 필터링하는 중입니다. 지금 바로 입력할 수 있으며, 시작이 완료되면 이 배너가 사라집니다."
	case "harness.views":
		return "뷰"
	case "harness.items":
		return "항목"
	case "harness.action":
		return "작업"
	case "harness.details":
		return "세부정보"
	case "harness.none":
		return "(없음)"
	case "harness.unknown":
		return "알 수 없음"
	case "harness.unscoped":
		return "범위 없음"
	case "harness.unavailable":
		return "하네스를 사용할 수 없습니다"
	case "harness.unavailable_intro":
		return "기존 프로젝트에서 여기서 시작:"
	case "harness.unavailable_step_init":
		return "  1. Enter 또는 i를 눌러 하네스 초기화"
	case "harness.unavailable_step_refresh":
		return "  2. 초기화가 완료되면 r을 눌러 새로고침"
	case "harness.section.init":
		return "초기화"
	case "harness.section.check":
		return "검사"
	case "harness.section.doctor":
		return "닥터"
	case "harness.section.monitor":
		return "모니터"
	case "harness.section.gc":
		return "GC"
	case "harness.section.contexts":
		return "컨텍스트"
	case "harness.section.tasks":
		return "작업"
	case "harness.section.queue":
		return "대기열"
	case "harness.section.run":
		return "실행"
	case "harness.section.run_queued":
		return "대기 중인 실행"
	case "harness.section.inbox":
		return "받은편지함"
	case "harness.section.review":
		return "리뷰"
	case "harness.section.promote":
		return "승격"
	case "harness.section.release":
		return "릴리스"
	case "harness.section.rollouts":
		return "롤아웃"
	case "harness.hints.unavailable":
		return "Enter/i 하네스 초기화 • r 새로고침 • Esc 닫기"
	case "harness.hints.move":
		return "j/k 이동"
	case "harness.hints.tab":
		return "Tab 전환"
	case "harness.hints.refresh":
		return "r 새로고침"
	case "harness.hints.close":
		return "Esc 닫기"
	case "harness.hints.check":
		return "Enter 검사 실행"
	case "harness.hints.monitor":
		return "Enter 스냅샷 새로고침"
	case "harness.hints.gc":
		return "Enter GC 실행"
	case "harness.hints.type_goal":
		return "목표 입력"
	case "harness.hints.queue":
		return "Enter 대기열 추가"
	case "harness.hints.run":
		return "Enter 실행"
	case "harness.hints.focus_input":
		return "Tab 입력 포커스"
	case "harness.hints.rerun":
		return "Enter 실패 재실행"
	case "harness.hints.next":
		return "Enter 다음"
	case "harness.hints.all":
		return "a 전체"
	case "harness.hints.retry_failed":
		return "f 실패 재시도"
	case "harness.hints.resume":
		return "s 재개"
	case "harness.hints.promote_owner":
		return "p 소유자 승격"
	case "harness.hints.retry_owner":
		return "f 소유자 재시도"
	case "harness.hints.approve":
		return "Enter 승인"
	case "harness.hints.reject":
		return "x 거부"
	case "harness.hints.promote":
		return "Enter 승격"
	case "harness.hints.apply_batch":
		return "Enter 배치 적용"
	case "harness.hints.advance":
		return "Enter 진행"
	case "harness.hints.approve_gate":
		return "g 게이트 승인"
	case "harness.hints.pause_resume":
		return "p 일시정지/재개"
	case "harness.hints.abort":
		return "x 중단"
	case "harness.hint.primary.check":
		return "Enter를 눌러 검사를 실행하세요."
	case "harness.hint.primary.monitor":
		return "Enter를 눌러 모니터 스냅샷을 새로고침하세요."
	case "harness.hint.primary.gc":
		return "Enter를 눌러 가비지 컬렉션을 실행하세요."
	case "harness.hint.primary.queue":
		return "목표를 입력한 후 Enter를 눌러 대기열에 추가하세요."
	case "harness.hint.primary.run":
		return "목표를 입력한 후 Enter를 눌러 실행을 시작하세요."
	case "harness.hint.primary.tasks":
		return "Enter를 눌러 선택한 실패한 작업을 재실행하세요."
	case "harness.hint.primary.run_queued":
		return "Enter 다음 실행; a 전체 실행; f 실패 재시도; s 중단 재개."
	case "harness.hint.primary.inbox":
		return "p를 눌러 이 소유자를 승격하거나 f를 눌러 재시도하세요."
	case "harness.hint.primary.review":
		return "Enter를 눌러 승인하거나 x를 눌러 거부하세요."
	case "harness.hint.primary.promote":
		return "Enter를 눌러 선택한 작업을 승격하세요."
	case "harness.hint.primary.release":
		return "Enter를 눌러 현재 릴리스 배치를 적용하세요."
	case "harness.hint.primary.rollouts":
		return "Enter 진행; g 게이트 승인; p 일시정지/재개; x 중단."
	case "harness.hint.primary.none":
		return "이 섹션에는 인라인 입력이 필요하지 않습니다."
	case "harness.message.read_only":
		return "다른 실행이 진행 중일 때 하네스 패널은 읽기 전용입니다."
	case "harness.message.monitor_refreshed":
		return "하네스 모니터가 새로고침되었습니다."
	case "harness.message.rerun_failed_only":
		return "하네스 작업 %s이(가) %s입니다. 실패한 작업만 재실행할 수 있습니다."
	case "harness.message.review_approved":
		return "%s 리뷰 승인됨"
	case "harness.message.review_rejected":
		return "%s 리뷰 거부됨"
	case "harness.message.promoted":
		return "%s 승격됨"
	case "harness.message.no_release_tasks":
		return "릴리스할 준비가 된 하네스 작업이 없습니다."
	case "harness.message.release_applied":
		return "릴리스 배치 %s 적용됨"
	case "harness.message.no_rollouts":
		return "저장된 롤아웃이 없습니다."
	case "harness.message.rollout_advanced":
		return "롤아웃 %s 진행됨"
	case "harness.message.owner_promoted":
		return "%s에 대해 %d개 작업 승격됨"
	case "harness.message.owner_retried":
		return "%s에 대해 실패한 작업 재시도됨"
	case "harness.message.gate_approved":
		return "%s의 다음 게이트 승인됨"
	case "harness.message.rollout_resumed":
		return "롤아웃 %s 재개됨"
	case "harness.message.rollout_paused":
		return "롤아웃 %s 일시정지됨"
	case "harness.message.rollout_aborted":
		return "롤아웃 %s 중단됨"
	case "harness.message.check_passed":
		return "하네스 검사 통과."
	case "harness.message.check_failed":
		return "하네스 검사에서 문제가 발견되었습니다."
	case "harness.message.gc_complete":
		return "하네스 GC 완료."
	case "harness.message.queue_goal_required":
		return "먼저 패널 입력에 대기열 목표를 입력하세요."
	case "harness.message.queued":
		return "하네스 작업 %s 대기열에 추가됨"
	case "harness.activity.status":
		return "하네스 %s"
	case "harness.log.phase":
		return "단계"
	case "harness.log.worker":
		return "워커"
	case "harness.tool.read_file":
		return "파일 읽기"
	case "harness.tool.write_file":
		return "파일 쓰기"
	case "harness.tool.browse_files":
		return "파일 탐색"
	case "harness.tool.search_code":
		return "코드 검색"
	case "harness.tool.run_command":
		return "명령 실행"
	case "harness.tool.fetch_web_page":
		return "웹 페이지 가져오기"
	case "harness.tool.run_subagent":
		return "서브에이전트 실행"
	case "harness.tool.update_task_state":
		return "작업 상태 업데이트"
	case "harness.message.run_goal_required":
		return "먼저 패널 입력에 실행 목표를 입력하세요."
	case "harness.message.no_queued_executed":
		return "실행된 대기 중인 하네스 작업이 없습니다."
	case "harness.message.queue_retried":
		return "실패한 대기 중인 작업 %d개 재시도됨."
	case "harness.message.queue_resumed":
		return "중단된 대기 중인 작업 %d개 재개됨."
	case "harness.message.queue_ran":
		return "대기 중인 작업 %d개 실행됨."
	case "harness.preview.not_initialized":
		return `이 프로젝트에는 아직 하네스가 초기화되지 않았습니다.

Enter 또는 i를 눌러 현재 저장소에서 하네스 초기화를 실행하세요.`
	case "harness.preview.check":
		return `현재 프로젝트에 대해 하네스 검사를 실행합니다.

Enter: 필수 파일/콘텐츠/컨텍스트 검사 및 구성된 검증 명령을 실행합니다.`
	case "harness.preview.gc":
		return `하네스 가비지 컬렉션을 실행합니다.

Enter: 오래된 작업 보관, 오래된 차단/실행 중인 작업 폐기, 오래된 로그 정리, 고아 워크트리 제거.`
	case "harness.preview.queue_help":
		return "여기에 하네스 목표를 입력한 후 Enter를 눌러 대기열에 추가하세요."
	case "harness.preview.run_help":
		return "여기에 하네스 목표를 입력한 후 Enter를 눌러 실행을 시작하세요."
	case "harness.preview.run_queued":
		return `대기열 상태:
대기=%d 실행=%d 차단=%d 실패=%d

Enter 다음 실행 가능한 작업 실행.
a 모든 실행 가능한 작업 실행.
f 실패한 작업 재시도.
s 중단된 작업 재개.`
	case "harness.preview.no_owner":
		return "선택된 하네스 소유자가 없습니다."
	case "harness.preview.no_context":
		return "선택된 하네스 컨텍스트가 없습니다."
	case "harness.preview.no_task":
		return "선택된 하네스 작업이 없습니다."
	case "harness.preview.project_not_initialized":
		return "이 프로젝트에는 아직 하네스가 초기화되지 않았습니다."
	case "harness.preview.project_initialized":
		return "하네스가 초기화되었습니다."
	case "harness.preview.project_help":
		return "/harness를 사용하여 컨트롤 플레인을 탐색하고 조작하세요."
	case "harness.preview.no_doctor":
		return "하네스 닥터 보고서가 없습니다."
	case "harness.preview.monitor_unavailable":
		return "하네스 모니터를 사용할 수 없습니다."
	case "harness.label.context_title":
		return "컨텍스트"
	case "harness.label.owner_title":
		return "소유자"
	case "harness.label.id":
		return "id"
	case "harness.label.status":
		return "상태"
	case "harness.label.goal":
		return "목표"
	case "harness.label.attempts":
		return "시도"
	case "harness.label.depends_on":
		return "의존성"
	case "harness.label.context":
		return "컨텍스트"
	case "harness.label.workspace":
		return "작업공간"
	case "harness.label.branch":
		return "브랜치"
	case "harness.label.worker":
		return "워커"
	case "harness.label.progress":
		return "진행"
	case "harness.label.verification":
		return "검증"
	case "harness.label.changed_files":
		return "변경된 파일"
	case "harness.label.delivery_report":
		return "배포_보고서"
	case "harness.label.delivery_report_human":
		return "배포 보고서"
	case "harness.label.log":
		return "로그"
	case "harness.label.review":
		return "리뷰"
	case "harness.label.review_notes":
		return "리뷰_메모"
	case "harness.label.promotion":
		return "승격"
	case "harness.label.promotion_notes":
		return "승격_메모"
	case "harness.label.release_batch":
		return "릴리스_배치"
	case "harness.label.release_batch_human":
		return "릴리스 배치"
	case "harness.label.release_notes":
		return "릴리스_노트"
	case "harness.label.error":
		return "오류"
	case "harness.label.name":
		return "이름"
	case "harness.label.description":
		return "설명"
	case "harness.label.owner":
		return "소유자"
	case "harness.label.commands":
		return "명령"
	case "harness.label.tasks":
		return "작업"
	case "harness.label.rollouts":
		return "롤아웃"
	case "harness.label.gates":
		return "게이트"
	case "harness.label.latest":
		return "최신"
	case "harness.label.repo":
		return "저장소"
	case "harness.label.config":
		return "설정"
	case "harness.label.project":
		return "프로젝트"
	case "harness.label.structure":
		return "구조"
	case "harness.label.contexts":
		return "컨텍스트"
	case "harness.label.workers":
		return "워커"
	case "harness.label.workflow":
		return "워크플로우"
	case "harness.label.quality":
		return "품질"
	case "harness.label.worktrees":
		return "워크트리"
	case "harness.label.snapshot":
		return "스냅샷"
	case "harness.label.events":
		return "이벤트"
	case "harness.label.target":
		return "대상"
	case "harness.label.review_ready":
		return "리뷰_준비됨"
	case "harness.label.promotion_ready":
		return "승격_준비됨"
	case "harness.label.retryable":
		return "재시도 가능"
	case "harness.task_title":
		return "하네스 작업"
	case "harness.doctor_title":
		return "하네스 닥터"
	case "harness.monitor_title":
		return "하네스 모니터"
	case "harness.latest_task":
		return "최신 작업"
	case "harness.latest_event":
		return "최신 이벤트"
	case "harness.focus":
		return "포커스"
	case "harness.status.ok":
		return "정상"
	case "harness.status.needs_attention":
		return "주의 필요"
	case "harness.group.review":
		return "리뷰"
	case "harness.group.promotion":
		return "승격"
	case "harness.group.retry":
		return "재시도"
	case "harness.review_ready_short":
		return "리뷰"
	case "harness.promote_ready_short":
		return "승격"
	case "harness.tasks_count":
		return "작업"
	case "harness.input_empty":
		return "(입력창이 비어 있습니다)"
	case "harness.no_waves":
		return "웨이브 없음"
	case "harness.mixed":
		return "혼합"
	case "hint.autocomplete":
		return "Tab/Shift+Tab 전환 • Enter 적용 • Esc 닫기"
	case "hint.mention":
		return "@ 파일/폴더 첨부 • Tab/Shift+Tab 전환 • Enter 적용"
	case "hint.mode":
		return "모드"
	case "mode.approval.ask":
		return "매번 확인"
	case "mode.approval.none":
		return "승인 불필요"
	case "mode.approval.critical":
		return "중요 작업만 확인"
	case "mode.tools.rules":
		return "도구 규칙"
	case "mode.tools.readonly":
		return "읽기 전용"
	case "mode.tools.safe":
		return "안전"
	case "mode.tools.open":
		return "열림"
	case "mode.agent.waits":
		return "에이전트 대기"
	case "mode.agent.autocontinue":
		return "자동 계속"
	case "hint.enter_send":
		return "Enter로 전송"
	case "hint.ctrlv_image":
		return "Ctrl+V / Ctrl+Shift+V 이미지 붙여넣기"
	case "hint.ctrlr_sidebar":
		return "Ctrl+R 사이드바"
	case "hint.help":
		return "/help 도움말"
	case "hint.add_context":
		return "@ 컨텍스트 추가"
	case "hint.scroll":
		return "PgUp/PgDn 스크롤"
	case "hint.shift_tab_mode":
		return "Shift+Tab으로 모드 전환"
	case "hint.ctrlc_cancel":
		return "Ctrl+C로 취소"
	case "hint.ctrlc_exit":
		return "Ctrl+C 지우기/종료"
	case "hint.image_attached":
		return "이미지 첨부됨"
	case "hint.image_attached_count":
		return "이미지 %d개 첨부됨"
	case "hint.follow_panel":
		return "Ctrl+N 팔로우"
	case "hint.unfollow_panel":
		return "Ctrl+N 언팔로우"
	case "queued.count":
		return "%d개 대기 중"
	case "queued.output":
		return "[대기 %d 보류 중]\\n\\n"
	case "interrupt.delivered":
		return "[활성 실행에 전달됨; 계획 수정 중]\\n"
	case "status.thinking":
		return "생각 중..."
	case "status.writing":
		return "작성 중..."
	case "status.cancelling":
		return "취소 중..."
	case "status.compacting":
		return "컨텍스트 압축 중..."
	case "status.compacted":
		return "컨텍스트 압축 완료"
	case "reasoning.effort.status":
		return "추론 강도: %s"
	case "reasoning.effort.set":
		return "이 세션의 추론 강도가 %s(으)로 설정됨"
	case "reasoning.effort.unsupported.status":
		return "현재 제공자는 추론 강도를 지원하지 않음"
	case "reasoning.effort.unsupported":
		return "현재 제공자는 추론 강도를 지원하지 않습니다"
	case "follow.loading":
		return "  팔로우 뷰 로딩 중..."
	case "follow.active_agent":
		return "에이전트 %s 팔로우 중 — 입력 일시정지. Esc로 돌아가기."
	case "follow.active_teammate":
		return "팀원 %s 팔로우 중 — 입력 일시정지. Esc로 돌아가기."
	case "follow.status_running":
		return "실행 중"
	case "follow.status_done":
		return "완료"
	case "follow.more":
		return "  +%d개 더"
	case "follow.hint":
		return "  ↑↓←→ 전환  Esc 닫기"
	case "status.tools_used":
		return "도구 사용"
	case "tool.done":
		return "완료"
	case "tool.failed":
		return "실패"
	case "tool.no_output":
		return "출력 없음"
	case "tool.output":
		return "출력"
	case "tool.content":
		return "내용"
	case "tool.match":
		return "일치"
	case "tool.matches":
		return "일치"
	case "tool.entry":
		return "항목"
	case "tool.result":
		return "결과"
	case "approval.rejected":
		return "거부됨"
	case "approval.allow":
		return "허용"
	case "approval.allow_always":
		return "항상 허용"
	case "approval.deny":
		return "거부"
	case "approval.accept":
		return "승인"
	case "approval.reject":
		return "거부"
	case "exit.confirm":
		return "ggcode를 종료하시겠습니까?"
	case "cancel.confirm":
		return "실행 중인 에이전트를 취소하려면 Ctrl-C 또는 Esc를 다시 누르세요.\\n\\n"
	case "interrupted":
		return "중단됨"
	case "lang.current":
		return "현재 언어"
	case "lang.invalid":
		return "잘못된 언어"
	case "lang.switch":
		return "언어 전환"
	case "lang.selection.current":
		return "현재 언어"
	case "lang.selection.hint":
		return "언어를 선택하세요"
	case "lang.first_use.title":
		return "선호하는 언어를 선택하세요"
	case "lang.first_use.body":
		return " ggcode가 사용할 언어를 선택하세요."
	case "lang.first_use.hint":
		return " Tab/j/k 이동 • Enter 확인 • e/z 단축키"
	case "mode.current":
		return "현재 모드"
	case "mode.persist_failed":
		return "모드 설정 저장 실패: %v"
	case "input.placeholder":
		return "메시지를 입력하세요..."
	case "panel.model_filter.prompt":
		return "필터> "
	case "panel.model_filter.placeholder":
		return "입력하여 모델 필터"
	case "panel.model_list.none":
		return "(없음)"
	case "panel.model_list.no_matches":
		return "(일치 항목 없음)"
	case "panel.model_list.showing":
		return "모델 %d/%d 표시"
	case "panel.model_list.hidden_above":
		return "위에 %d개"
	case "panel.model_list.hidden_more":
		return "%d개 더"
	case "panel.provider.vendors":
		return "제공자"
	case "panel.provider.endpoints":
		return "엔드포인트"
	case "panel.provider.models":
		return "모델"
	case "panel.provider.active_draft":
		return "활성 드래프트"
	case "panel.provider.protocol":
		return "프로토콜"
	case "panel.provider.protocol.unknown":
		return "(알 수 없음)"
	case "panel.provider.auth":
		return "인증"
	case "panel.provider.env_var":
		return "환경 변수"
	case "panel.provider.api_key":
		return "API 키"
	case "panel.provider.api_key.missing":
		return "누락"
	case "panel.provider.api_key.configured":
		return "설정됨"
	case "panel.provider.auth.connected":
		return "연결됨"
	case "panel.provider.auth.not_connected":
		return "연결되지 않음"
	case "panel.provider.base_url":
		return "기본 URL"
	case "panel.provider.base_url.not_set":
		return "(설정되지 않음)"
	case "panel.provider.enterprise_url":
		return "엔터프라이즈 URL"
	case "panel.provider.tags":
		return "태그"
	case "panel.provider.model.set_with_m":
		return "(m으로 설정)"
	case "panel.provider.edit":
		return "편집"
	case "panel.provider.edit.vendor_api_key":
		return "제공자 API 키"
	case "panel.provider.edit.endpoint_api_key":
		return "엔드포인트 API 키"
	case "panel.provider.edit.endpoint_base_url":
		return "엔드포인트 기본 URL"
	case "panel.provider.edit.custom_model":
		return "커스텀 모델"
	case "panel.provider.edit.new_endpoint_name":
		return "새 엔드포인트 이름"
	case "panel.provider.hint.edit":
		return "Enter 저장 • Esc 취소"
	case "panel.provider.hint.main":
		return "Tab/Shift+Tab 포커스 변경 • j/k 이동 • / 필터 포커스 • Enter 또는 s 적용 • a 제공자 키 • u 엔드포인트 키 • b 기본 URL • m 커스텀 모델 • e 엔드포인트 추가 • Esc 닫기"
	case "panel.provider.hint.copilot":
		return "GitHub Copilot: l 로그인 • x 로그아웃 • b 엔터프라이즈 도메인 편집"
	case "panel.provider.saved":
		return "저장됨."
	case "panel.provider.saved_activated":
		return "저장 및 활성화됨."
	case "panel.provider.login.starting":
		return "GitHub Copilot 로그인 시작..."
	case "panel.provider.login.instructions":
		return "%s를 열고 코드 %s를 입력하세요. 인증 대기 중..."
	case "panel.provider.login.copied":
		return "기기 코드가 클립보드에 복사됨."
	case "panel.provider.login.copy_failed":
		return "기기 코드 복사 실패: %s"
	case "panel.provider.login.browser_opened":
		return "브라우저에서 인증 페이지를 열었습니다."
	case "panel.provider.login.browser_failed":
		return "인증 페이지 열기 실패: %s"
	case "panel.provider.login.success":
		return "GitHub Copilot 연결됨."
	case "panel.provider.login.failed":
		return "GitHub Copilot 로그인 실패: %s"
	case "panel.provider.logout.success":
		return "GitHub Copilot 연결 해제됨."
	case "panel.provider.refreshing_vendor":
		return "%s의 모델 새로고침 중..."
	case "panel.provider.refresh.save_failed":
		return "모델을 새로고침했지만 설정 저장 실패: %s"
	case "panel.provider.refresh.partial":
		return "%d개 엔드포인트 새로고침, %d개 모델 발견. 일부 엔드포인트 실패: %v"
	case "panel.provider.refresh.success":
		return "%d개 엔드포인트 새로고침, %d개 모델 발견."
	case "panel.provider.refresh.failed":
		return "모델 새로고침 실패: %s"
	case "panel.provider.refresh.none":
		return "이 제공자에 대해 새로고침 가능한 엔드포인트가 없습니다."
	case "panel.model.models":
		return "모델"
	case "panel.model.vendor":
		return "제공자"
	case "panel.model.endpoint":
		return "엔드포인트"
	case "panel.model.protocol":
		return "프로토콜"
	case "panel.model.source":
		return "소스"
	case "panel.model.source.builtin":
		return "내장"
	case "panel.model.source.remote":
		return "원격"
	case "panel.model.refreshing":
		return "최신 모델 새로고침 중..."
	case "panel.model.hint.main":
		return "j/k 이동 • Enter 또는 s 적용 • w 컨텍스트 윈도우 • o 최대 토큰 • r 새로고침 • / 필터 • Esc 닫기"
	case "panel.model.hint.edit":
		return "Enter 저장 • Esc 취소 (0 또는 빈 값 = 자동, K/M/G 접미사 가능 예: 256k)"
	case "panel.model.context_window":
		return "컨텍스트 윈도우"
	case "panel.model.max_tokens":
		return "최대 출력 토큰"
	case "panel.model.edit":
		return "편집"
	case "panel.model.saved_runtime_inactive":
		return "설정이 저장되었지만 현재 런타임이 비활성 상태입니다: %s"
	case "panel.model.context_applied":
		return "context_window=%d, max_tokens=%d 적용됨 (저장됨)"
	case "panel.model.context_cleared":
		return "자동 감지로 재설정 (저장됨)"
	case "panel.model.switched":
		return "모델이 %s(으)로 전환됨."
	case "panel.model.refresh.save_failed":
		return "모델을 새로고침했지만 설정 저장 실패: %s"
	case "panel.model.refresh.builtin_reason":
		return "내장 모델 사용 중: %s"
	case "panel.model.refresh.remote_loaded":
		return "원격 모델 %d개 로드됨."
	case "panel.model.refresh.builtin_loaded":
		return "내장 모델 로드됨."
	case "command.unknown":
		return "알 수 없는 명령"
	case "command.retry_empty":
		return "재시도할 이전 제출이 없습니다."
	case "command.retry_busy":
		return "에이전트가 실행 중입니다. 재시도 전 현재 실행이 완료될 때까지 기다리세요."
	case "command.edit_empty":
		return "편집할 이전 제출이 없습니다."
	case "command.edit_busy":
		return "에이전트가 실행 중입니다. 편집 전 현재 실행이 완료될 때까지 기다리세요."
	case "command.edit_ready":
		return "이전 제출을 로드했습니다 — 편집 후 Enter를 눌러 전송하세요."
	case "command.help_hint":
		return "사용 가능한 명령은 /help를 입력하세요\\n\\n"
	case "command.usage.allow":
		return "사용법: /allow <도구-이름>\\n\\n"
	case "command.usage.resume":
		return "사용법: /resume <세션-id>\\n\\n"
	case "command.usage.export":
		return "사용법: /export <세션-id>\\n\\n"
	case "init.resolve_failed":
		return "초기화 대상 확인 실패: %v\\n\\n"
	case "init.generate_failed":
		return "GGCODE.md 콘텐츠 생성 실패: %v\\n\\n"
	case "init.collecting":
		return "프로젝트 지식 수집 중..."
	case "init.prompt.title":
		return "프로젝트 초기화"
	case "init.prompt.body":
		return "이 프로젝트에 GGCODE.md가 없습니다. 에이전트가 프로젝트를 이해하는 데 도움이 되도록 생성하세요."
	case "init.prompt.yes":
		return "생성"
	case "init.prompt.no":
		return "건너뛰기"
	case "init.prompt.hint":
		return " y = GGCODE.md 생성 • n/Esc = 건너뛰기"
	case "command.model_switched":
		return "모델 전환: %s (제공자: %s)\\n\\n"
	case "command.model_failed":
		return "모델 전환 실패: %v\\n\\n"
	case "command.model_current":
		return "현재 모델: %s (제공자: %s)\\n사용 가능한 모델: %s\\n/model로 모델 패널을 열거나 /model <모델-이름>으로 직접 전환하세요.\\n\\n"
	case "command.provider_unknown":
		return "알 수 없는 제공자: %s (사용 가능: %v)\\n\\n"
	case "command.provider_switched":
		return "제공자 전환: %s (모델: %s)\\n\\n"
	case "command.provider_failed":
		return "제공자 선택 업데이트 실패: %v\\n\\n"
	case "command.provider_current":
		return "현재 제공자: %s (엔드포인트: %s, 모델: %s)\\n사용 가능한 제공자: %s\\n사용 가능한 엔드포인트: %s\\n사용법: /provider [제공자] [엔드포인트]\\n\\n"
	case "command.allow_set":
		return "✓ %s이(가) 항상 허용되었습니다\\n\\n"
	case "command.custom":
		return "사용자 정의 명령 /%s:\\n"
	case "command.mention_error":
		return "멘션 확장 오류: %v"
	case "session.list_failed":
		return "세션 목록 조회 오류: %v\\n\\n"
	case "session.untitled":
		return "제목 없음"
	case "session.store_missing":
		return "세션 저장소가 구성되지 않았습니다.\\n\\n"
	case "session.none":
		return "세션을 찾을 수 없습니다.\\n\\n"
	case "session.list.title":
		return "세션:\\n\\n"
	case "session.list.item":
		return "  %d. %s  %s  (%s)\\n"
	case "session.list.hint":
		return "\\n/resume <id>로 세션을 계속하세요\\n\\n"
	case "session.new":
		return "새 세션: %s\\n\\n"
	case "session.resume":
		return "세션 재개: %s — %s (메시지 %d개)\\n\\n"
	case "session.resume_failed":
		return "세션 %s 재개 실패: %v\\n\\n"
	case "session.resume_fallback":
		return "대신 새 세션을 시작합니다.\\n\\n"
	case "session.export_failed":
		return "세션 내보내기 오류: %v\\n\\n"
	case "session.write_failed":
		return "파일 쓰기 오류: %v\\n\\n"
	case "session.exported":
		return "세션 %s을(를) %s(으)로 내보냈습니다\\n\\n"
	case "checkpoint.disabled":
		return "체크포인트가 활성화되지 않았습니다.\\n\\n"
	case "checkpoint.undo_failed":
		return "실행 취소 실패: %v\\n\\n"
	case "checkpoint.undid":
		return "%s의 %s 실행 취소 (체크포인트 %s)\\n"
	case "checkpoint.none":
		return "체크포인트가 없습니다.\\n\\n"
	case "files.disabled":
		return "파일 브라우저 비활성화"
	case "files.none":
		return "이 세션에서 에이전트가 수정한 파일이 없습니다.\\n\\n"
	case "files.title":
		return "파일"
	case "files.item":
		return "  %s  %d회 편집  마지막: %s%s\\n"
	case "files.hint":
		return "파일 선택"
	case "checkpoint.list.title":
		return "체크포인트 (%d):\\n\\n"
	case "checkpoint.list.item":
		return "  %d. %s  %s  %s  %s\\n"
	case "checkpoint.list.hint":
		return "\\n/undo로 가장 최근 항목을 되돌리세요\\n\\n"
	case "memory.auto_unavailable":
		return "자동 메모리가 초기화되지 않았습니다.\\n\\n"
	case "memory.list_failed":
		return "메모리 목록 조회 오류: %v\\n\\n"
	case "memory.none":
		return "메모리가 없습니다"
	case "memory.auto_title":
		return "자동 메모리:\\n"
	case "memory.clear_failed":
		return "메모리 삭제 오류: %v\\n\\n"
	case "memory.cleared":
		return "모든 자동 메모리가 삭제되었습니다.\\n\\n"
	case "memory.title":
		return "메모리"
	case "memory.project":
		return "프로젝트 메모리:\\n"
	case "memory.project_none":
		return "  로드된 프로젝트 메모리 파일이 없습니다.\\n"
	case "memory.auto":
		return "자동 메모리:\\n"
	case "memory.auto_none":
		return "  로드된 자동 메모리가 없습니다.\\n"
	case "memory.usage":
		return "\\n사용법: /memory [list|clear]\\n\\n"
	case "compact.unavailable":
		return "컨텍스트 관리자를 사용할 수 없습니다.\\n\\n"
	case "compact.failed":
		return "압축 실패: %v\\n\\n"
	case "compact.done":
		return "압축 완료"
	case "compact.done_with_stats":
		return "대화 기록이 압축되었습니다 (%d → %d 토큰).\\n\\n"
	case "todo.cleared":
		return "TODO 목록이 삭제되었습니다.\\n\\n"
	case "todo.clear_failed":
		return "TODO 삭제 오류: %v\\n\\n"
	case "todo.none":
		return "TODO 목록을 찾을 수 없습니다. todo_write 도구로 생성하세요.\\n\\n"
	case "todo.read_failed":
		return "TODO 읽기 오류: %v\\n\\n"
	case "todo.parse_failed":
		return "TODO 파싱 오류: %v\\n\\n"
	case "todo.title":
		return "TODO 목록:\\n%s\\n\\n"
	case "bug.title":
		return "=== 버그 보고 진단 ===\\n\\n"
	case "bug.version":
		return "버전: %s\\n"
	case "bug.os":
		return "운영체제: %s %s\\n"
	case "bug.go":
		return "Go: %s\\n"
	case "bug.provider":
		return "제공자: %s\\n"
	case "bug.model":
		return "모델: %s\\n"
	case "bug.session":
		return "세션: %s (메시지 %d개)\\n"
	case "bug.mcp":
		return "MCP 서버: %d개\\n"
	case "bug.last_error":
		return "마지막 오류: %s\\n"
	case "bug.hint":
		return "\\n버그를 보고할 때 이 정보를 포함해 주세요.\\n\\n"
	case "config.usage":
		return "사용법: /config set <키> <값>\\n\\n키: model, vendor, endpoint, language, apikey [--vendor]\\n\\n엔드포인트: /config add-endpoint <이름> <base_url> [--protocol openai] [--apikey sk-xxx]\\n          /config remove-endpoint <이름>\\n\\n"
	case "config.not_loaded":
		return "설정을 불러오지 못했습니다.\\n\\n"
	case "config.model_set":
		return "설정: 모델 = %s\\n\\n"
	case "config.provider_set":
		return "설정: 제공자 = %s\\n\\n"
	case "config.language_set":
		return "설정: 언어 = %s\\n\\n"
	case "config.unknown_key":
		return "알 수 없는 설정 키: %s\\n지원됨: model, provider, language\\n\\n"
	case "config.title":
		return "현재 설정:\\n"
	case "status.title":
		return "상태:\\n"
	case "panel.update":
		return "업데이트"
	case "label.version":
		return "버전"
	case "label.latest":
		return "최신"
	case "update.sidebar_hint":
		return "새 릴리스가 있습니다. /update를 실행하세요."
	case "update.up_to_date":
		return "최신 버전입니다"
	case "update.available":
		return "업데이트 가능"
	case "update.current":
		return "현재: %s (최신: %s)"
	case "update.unknown":
		return "확인되지 않음"
	case "update.check_failed":
		return "확인 실패: %s"
	case "update.unavailable":
		return "이 세션에서는 업데이트를 사용할 수 없습니다.\\n\\n"
	case "update.preparing":
		return "업데이트 준비 중"
	case "update.failed":
		return "업데이트 실패: %v\\n\\n"
	case "update.restart_failed":
		return "업데이트가 준비되었지만 재시작에 실패했습니다: %v\\n\\n"
	case "update.pm_hint.brew":
		return "업데이트 설치 완료. 참고: Homebrew로 설치되었습니다.\\nHomebrew 동기화를 위해 `brew upgrade ggcode`를 실행하세요.\\n\\n"
	case "update.pm_hint.scoop":
		return "업데이트 설치 완료. 참고: Scoop으로 설치되었습니다.\\nScoop 동기화를 위해 `scoop update ggcode`를 실행하세요.\\n\\n"
	case "update.pm_hint.winget":
		return "업데이트 설치 완료. 참고: winget으로 설치되었습니다.\\nwinget 동기화를 위해 `winget upgrade ggcode`를 실행하세요.\\n\\n"
	case "update.pm_hint.snap":
		return "업데이트 설치 완료. 참고: Snap으로 설치되었습니다.\\nSnap 동기화를 위해 `sudo snap refresh ggcode`를 실행하세요.\\n\\n"
	case "update.other_installs":
		return "이 시스템에서 다른 ggcode 설치가 감지되었습니다:\\n%s\\n다른 ggcode가 PATH에 먼저 나타나면 함께 업데이트하거나 PATH 순서를 조정하세요.\\n\\n"
	case "update.dual_scope":
		return "경고: 사용자 및 시스템 전체 ggcode 설치가 모두 발견되었습니다:\\n  사용자: %s\\n  시스템: %s\\nPATH 충돌이 발생할 수 있습니다. 설정 > 앱에서 하나를 제거하는 것을 고려하세요.\\n\\n"
	case "plugins.unavailable":
		return "플러그인 관리자를 사용할 수 없습니다.\\n\\n"
	case "plugins.none":
		return "로드된 플러그인이 없습니다.\\n\\n"
	case "plugins.title":
		return "플러그인:\\n"
	case "mcp.none":
		return "구성된 MCP 서버가 없습니다.\\n\\n"
	case "mcp.title":
		return "MCP 서버:\\n"
	case "mcp.active_tools":
		return "활성 도구"
	case "mcp.more":
		return "… %d개 더 • /mcp"
	case "image.usage":
		return "사용법: /image <경로/파일.png> 또는 /image paste\\n"
	case "image.formats":
		return "지원 형식: PNG, JPEG, GIF, WebP (최대 20MB)\\n\\n"
	case "image.attached":
		return "이미지 첨부됨: %s\\n"
	case "image.attached_hint":
		return "메시지를 보내 이미지를 포함하거나, /image로 다른 이미지를 첨부하세요.\\n\\n"
	case "image.clipboard_failed":
		return "클립보드에서 이미지를 붙여널 수 없습니다: %v"
	case "image.clipboard_no_image_windows":
		return "클립보드에서 이미지를 찾을 수 없습니다. Windows에서는 Ctrl+V가 종종 가로채집니다."
	case "agents.unavailable":
		return "서브에이전트 관리자가 구성되지 않았습니다.\\n\\n"
	case "agents.none":
		return "아직 생성된 서브에이전트가 없습니다.\\nLLLM이 spawn_agent 도구를 사용하여 서브에이전트를 생성할 수 있습니다.\\n\\n"
	case "agents.title":
		return "서브에이전트 %d개:\\n"
	case "agents.item":
		return "  %s [%s]%s - %s\\n"
	case "agents.hint":
		return "\\n/agent <id>로 상세 정보, /agent cancel <id>로 취소하세요.\\n\\n"
	case "agent.usage":
		return "사용법: /agent <id> 또는 /agent cancel <id>\\n\\n"
	case "agent.cancelled":
		return "에이전트가 취소되었습니다"
	case "agent.cancel_failed":
		return "%s을(를) 취소할 수 없습니다 (찾을 수 없거나 실행 중이 아님)\\n\\n"
	case "agent.not_found":
		return "서브에이전트 %s을(를) 찾을 수 없습니다\\n\\n"
	case "agent.title":
		return "에이전트"
	case "agent.result":
		return "결과: %s\\n"
	case "agent.error":
		return "오류: %v\\n"
	case "slash.help":
		return "도움말 표시"
	case "slash.sessions":
		return "저장된 세션 목록"
	case "slash.resume":
		return "이전 세션 재개"
	case "slash.export":
		return "세션을 Markdown으로 내보내기"
	case "slash.model":
		return "모델 전환"
	case "slash.provider":
		return "프로바이더 관리자 열기"
	case "slash.clear":
		return "대화 지우기"
	case "slash.mcp":
		return "MCP 서버 표시"
	case "slash.memory":
		return "메모리 관리"
	case "slash.undo":
		return "마지막 파일 편집 취소"
	case "slash.files":
		return "파일 브라우저 열기"
	case "slash.checkpoints":
		return "체크포인트 목록"
	case "slash.allow":
		return "도구 항상 허용"
	case "slash.plugins":
		return "로드된 플러그인 목록"
	case "slash.image":
		return "이미지 첨부"
	case "slash.init":
		return "프로젝트 GGCODE.md 생성"
	case "slash.harness":
		return "하네스 워크플로 명령 실행"
	case "slash.lang":
		return "인터페이스 언어 전환"
	case "slash.skills":
		return "사용 가능한 스킬 표시"
	case "slash.exit":
		return "ggcode 종료"
	case "slash.compact":
		return "컨텍스트 압축"
	case "slash.todo":
		return "TODO 목록 표시"
	case "slash.bug":
		return "버그 보고서 표시"
	case "slash.config":
		return "설정 표시"
	case "slash.qq":
		return "QQ 채널 바인딩 관리"
	case "slash.telegram":
		return "Telegram 채널 바인딩 관리"
	case "slash.pc":
		return "PC 채널 바인딩 관리"
	case "slash.discord":
		return "Discord 채널 바인딩 관리"
	case "slash.feishu":
		return "Feishu 채널 바인딩 관리"
	case "slash.slack":
		return "Slack 채널 바인딩 관리"
	case "slash.dingtalk":
		return "DingTalk 채널 바인딩 관리"
	case "slash.wechat":
		return "WeChat 채널 바인딩 관리"
	case "slash.wecom":
		return "WeCom (Enterprise WeChat) 채널 바인딩 관리"
	case "slash.mattermost":
		return "Mattermost 채널 바인딩 관리"
	case "slash.matrix":
		return "Matrix 채널 바인딩 관리"
	case "slash.signal":
		return "Signal 채널 바인딩 관리"
	case "slash.irc":
		return "IRC 채널 바인딩 관리"
	case "slash.nostr":
		return "Nostr 채널 바인딩 관리"
	case "slash.twitch":
		return "Twitch 채널 바인딩 관리"
	case "slash.whatsapp":
		return "WhatsApp 채널 바인딩 관리"
	case "slash.impersonate":
		return "쉘 프롬프트 표시를 위한 CLI 도구 가장"
	case "slash.knight":
		return "자율 백그라운드 에이전트 관리"
	case "slash.stream":
		return "스트리밍 출력 모드 구성"
	case "slash.diff":
		return "대화에서 git diff 표시"
	case "slash.hooks":
		return "구성된 훅 표시 (모든 이벤트, 유형, 매치 패턴)"
	case "slash.cost":
		return "비용 통계 표시"
	case "slash.review":
		return "현재 변경사항 AI 코드 리뷰"
	case "slash.copy":
		return "대화 복사"
	case "slash.context":
		return "컨텍스트 정보 표시"
	case "slash.im":
		return "IM 어댑터 관리"
	case "panel.qq.directory":
		return "디렉토리"
	case "panel.qq.runtime":
		return "런타임"
	case "panel.qq.bots":
		return "QQ 봇"
	case "panel.qq.created":
		return "생성됨: %d"
	case "panel.qq.bound":
		return "바인딩됨: %d"
	case "panel.qq.available":
		return "사용 가능: %d"
	case "panel.qq.current_binding":
		return "현재 바인딩"
	case "panel.qq.none":
		return "(없음)"
	case "panel.qq.default":
		return "(기본값)"
	case "panel.qq.adapter":
		return "어댑터: %s"
	case "panel.qq.target":
		return "대상: %s"
	case "panel.qq.channel":
		return "채널: %s"
	case "panel.qq.bot_list":
		return "QQ 봇 목록"
	case "panel.qq.no_bots":
		return "구성된 QQ 봇이 없습니다."
	case "panel.qq.entry.available":
		return "사용 가능"
	case "panel.qq.entry.bound":
		return "바인딩됨"
	case "panel.qq.entry.active":
		return "활성"
	case "panel.qq.entry.bound_other":
		return "바인딩됨: %s"
	case "panel.qq.entry.muted":
		return "음소거됨"
	case "panel.qq.details":
		return "상세"
	case "panel.qq.status":
		return "상태: %s"
	case "panel.qq.transport":
		return "전송: %s"
	case "panel.qq.bound_directory":
		return "바인딩된 디렉토리: %s"
	case "panel.qq.current_directory_target":
		return "현재 디렉토리 대상: %s"
	case "panel.qq.current_directory_channel":
		return "현재 디렉토리 채널: %s"
	case "panel.qq.waiting_for_pairing":
		return "(페어링 대기 중)"
	case "panel.qq.last_error":
		return "마지막 오류: %s"
	case "panel.qq.occupied_by":
		return "점유 중: %s"
	case "panel.qq.create":
		return "생성"
	case "panel.qq.bot_input":
		return "QQ 봇: %s"
	case "panel.qq.create_format":
		return "형식: <bot-id> <appid> <appsecret>"
	case "panel.qq.create_example":
		return "예: qq-main 123456 secret-value"
	case "panel.qq.create_hint":
		return "Enter 봇 생성 • Esc 취소"
	case "panel.qq.actions_hint":
		return "j/k 이동 • Enter 또는 b 봇 바인딩 • c 채널 바인딩 • x 채널 언바인딩"
	case "panel.qq.bind_channel":
		return "채널 바인딩"
	case "panel.qq.scan_hint":
		return "QR 코드를 스캔하고 봇을 추가한 후 메시지를 보내 페어링을 시작하세요."
	case "panel.qq.qr_code":
		return "QR 코드:"
	case "panel.qq.share_link":
		return "공유 링크:"
	case "panel.qq.message.no_bot":
		return "사용 가능한 QQ 봇이 없습니다."
	case "panel.qq.message.bound_success":
		return "QQ 봇이 현재 워크스페이스에 바인딩되었습니다. c를 눌러 채널 공유 링크를 생성하세요."
	case "panel.qq.message.share_generated":
		return "QQ 공유 링크가 생성되었습니다. QR 코드를 스캔하고 봇을 추가한 후 메시지를 보내세요."
	case "panel.qq.message.unbound":
		return "QQ 채널 바인딩이 해제되었습니다."
	case "panel.qq.message.cleared":
		return "현재 워크스페이스의 QQ 채널 인증이 삭제되었습니다."
	case "panel.qq.message.added_bot":
		return "QQ 봇 %s이(가) 추가되었습니다."
	case "panel.qq.error.config_unavailable":
		return "설정을 사용할 수 없습니다"
	case "panel.qq.error.config_format":
		return "QQ 봇 설정 형식: <bot-id> <appid> <appsecret>"
	case "panel.qq.error.adapter_required":
		return "QQ 어댑터 이름이 필요합니다"
	case "panel.qq.error.not_configured":
		return "QQ 봇 %q이(가) 구성되지 않았습니다"
	case "panel.qq.error.disabled":
		return "QQ 봇 %q이(가) 비활성화되었습니다"
	case "panel.qq.error.not_qq_adapter":
		return "어댑터 %q은(는) QQ 봇이 아닙니다"
	case "panel.qq.error.not_online":
		return "QQ 봇 %q이(가) 온라인이 아닙니다"
	case "panel.qq.error.not_online_detail":
		return "QQ 봇 %q이(가) 온라인이 아닙니다: %s"
	case "panel.qq.runtime.available":
		return "사용 가능"
	case "panel.qq.runtime.disabled":
		return "비활성화됨 (im.enabled: true로 설정하고 ggcode 재시작)"
	case "panel.qq.runtime.not_started":
		return "시작되지 않음 (ggcode 재시작으로 IM 런타임 초기화)"
	case "panel.qq.status.not_started":
		return "시작되지 않음"
	case "panel.qq.status.unknown":
		return "알 수 없음"
	case "slash.status":
		return "상태 표시"
	case "slash.update":
		return "업데이트 확인"
	case "slash.cron":
		return "정기 작업 관리"
	case "slash.branch":
		return "브랜치 정보 표시"
	case "slash.chat":
		return "채팅 모드"
	case "slash.edit":
		return "편집 모드"
	case "slash.inspector":
		return "인스펙터 패널 전환"
	case "slash.mode":
		return "권한 모드 설정"
	case "slash.nick":
		return "닉네임 설정"
	case "slash.reflect":
		return "실행 회고"
	case "slash.regenerate":
		return "마지막 응답 재생성"
	case "slash.restart":
		return "에이전트 재시작"
	case "slash.retry":
		return "마지막 요청 재시도"
	case "slash.rules":
		return "래칫 규칙 표시"
	case "slash.share":
		return "세션 공유"
	case "slash.stats":
		return "통계 표시"
	case "slash.tmux":
		return "tmux 패널"
	case "slash.tunnel":
		return "터널 관리"
	case "slash.unshare":
		return "공유 해제"
	case "regenerate.busy":
		return "에이전트가 실행 중에는 재생성할 수 없습니다. 취소하려면 Ctrl+C를 누르세요."
	case "regenerate.no_agent":
		return "에이전트가 초기화되지 않았습니다."
	case "regenerate.no_context":
		return "컨텍스트 관리자를 사용할 수 없습니다."
	case "regenerate.no_response":
		return "재생성할 어시스턴트 응답이 없습니다."
	case "branch.busy":
		return "에이전트가 실행 중에는 분기할 수 없습니다. 취소하려면 Ctrl+C를 누르세요."
	case "branch.no_session":
		return "분기할 활성 세션이 없습니다."
	case "branch.empty":
		return "분기할 메시지가 없습니다."
	case "branch.save_failed":
		return "분기 세션 생성 실패: %v"
	case "branch.success":
		return "새 세션 %s(으)로 분기됨 (출처: %s). 원본 세션은 보존됩니다."
	case "command.harness_usage":
		return "사용법: /harness <init|check|queue|tasks|run|rerun|run-queued|monitor|contexts|inbox|review|promote|release|gc|doctor> ... (release는 rollouts|advance|pause|resume|abort|approve|reject 지원)"
	case "command.harness_queue_usage":
		return "사용법: /harness queue <목표>"
	case "command.harness_run_usage":
		return "사용법: /harness run <목표>"
	case "command.harness_rerun_usage":
		return "사용법: /harness rerun <작업-id>"
	case "command.skill_agent_only":
		return "스킬 %s은(는) 에이전트만 호출할 수 있습니다."
	case "command.harness_owner_promoted":
		return "소유자 %s에 대해 하네스 작업 %d개 승격됨."
	case "command.harness_review_approved":
		return "하네스 작업 %s 승인됨."
	case "command.harness_review_rejected":
		return "하네스 작업 %s 거부됨."
	case "command.harness_promoted_many":
		return "하네스 작업 %d개 승격됨."
	case "command.harness_promoted_one":
		return "하네스 작업 %s 승격됨."
	case "command.harness_task_queued_detail":
		return "하네스 작업 %s이(가) 대기열에 추가되었습니다.\\n- 목표: %s"
	case "command.harness_tasks_empty":
		return "기록된 하네스 작업이 없습니다."
	case "command.harness_run_start":
		return "추적된 하네스 실행 시작 중...\\n/harness monitor 또는 작업/모니터 뷰에서 실시간 상태를 확인하세요."
	case "command.harness_rerun_start":
		return "추적된 하네스 재실행 시작 중...\\n/harness monitor 또는 작업/모니터 뷰에서 실시간 상태를 확인하세요."
	case "command.harness_rerun_invalid_status":
		return "하네스 작업 %s 상태: %s; 실패한 작업만 재실행할 수 있습니다."
	case "command.harness_status_starting_run":
		return "하네스 실행 시작 중..."
	case "command.harness_status_starting_rerun":
		return "하네스 재실행 시작 중..."
	case "command.harness_spinner_running":
		return "하네스 실행 중"
	case "command.harness_cancelled":
		return "하네스 실행이 취소되었습니다."
	case "tunnel.stopped":
		return "터널이 중지되었습니다."
	case "tunnel.not_active":
		return "활성 공유 세션이 없습니다."
	case "tunnel.mobile_connected":
		return "모바일 클라이언트가 연결되었습니다."
	case "config.save_scope_global":
		return "저장 대상 → 전역"
	case "config.save_scope_instance":
		return "저장 대상 → 인스턴스"
	case "config.save_scope_instance_new":
		return "저장 대상 → 인스턴스 (새 설정이 생성됨)"
	case "config.instance_unavailable":
		return "이 워크스페이스에 대한 인스턴스 설정을 사용할 수 없습니다"
	case "config.scope_instance":
		return "인스턴스"
	case "config.scope_global":
		return "전역"
	case "config.save_target_new_hint":
		return " (저장 시 새 설정이 생성됨)"
	case "config.save_target_line":
		return " 저장 대상: %s%s  [Ctrl+T 전환]"
	case "shell.empty":
		return "쉘 명령이 비어 있습니다."
	case "lanchat.unavailable":
		return "LAN Chat을 사용할 수 없습니다."
	case "reflect.no_agent":
		return "에이전트가 초기화되지 않았습니다."
	case "reflect.no_workdir":
		return "작업 디렉토리가 설정되지 않았습니다."
	case "reflect.no_memory":
		return "이 디렉토리에 대한 프로젝트 메모리를 사용할 수 없습니다."
	case "reflect.load_failed":
		return "인사이트 로드 실패: %v"
	case "reflect.empty":
		return "아직 실행 인사이트가 없습니다. 인사이트는 도구 호출 3회 이상 또는 파일 편집이 있는 에이전트 실행 후 자동으로 생성됩니다."
	case "reflect.title":
		return "## 누적 실행 인사이트\\n\\n"
	case "reflect.memory_location":
		return "메모리 위치: %s\\n"
	case "knight.unavailable":
		return "Knight를 사용할 수 없습니다"
	case "pairing.rejected":
		return "현재 페어링 요청이 거부되었습니다. 다시 시도해 주세요."
	case "pairing.blacklisted":
		return "이 채널은 여러 번 거부되어 블랙리스트에 추가되었습니다."
	case "help.text":
		return `Available commands:

Session & History:
  /help, /?          Show this help message
  /sessions          List all saved sessions
  /resume <id>       Resume a previous session
  /export <id>       Export session to markdown file
  /clear             Clear conversation history
  /compact           Compress conversation history (manual)
  /undo              Undo the last file edit (checkpoint rollback)
  /checkpoints       List all file edit checkpoints
  /regenerate        Discard last response and regenerate (alias: /regen)
  /branch            Fork current conversation into a new session (alias: /fork)

Model & Provider:
  /model [name]      Open model panel or switch directly
  /provider [vendor] Open provider manager
  /mode <mode>       Set agent mode (supervised|plan|auto|bypass|autopilot)

Development:
  /diff [opts]       Show git diff in chat (--cached, --stat, <file>)
  /review [opts]     AI code review of current changes (--cached, --staged)
  /copy              Copy last assistant response to clipboard
  /cost              Show session token usage and estimated cost
  /context           Show context window usage breakdown
  /hooks             Show configured hooks
  /init              Generate GGCODE.md from the current project
  /harness ...       Run harness control-plane commands
  /todo              View todo list
  /todo clear        Clear todo list

Integrations:
  /im                Open unified IM channels panel
  /mcp               Show connected MCP servers and tools
  /plugins           List loaded plugins and their tools
  /skills            Browse available skills
  /memory            Show loaded memory files
  /agents            List sub-agents
  /cron <sub>        Manage scheduled jobs (list|get|pause|resume|pauseall|resumeall)

System:
  /lang [code]       Choose or switch interface language
  /config            Show current configuration
  /config set <k> <v> Set a config value
  /status            Show current status
  /update            Update ggcode to the latest release
  /restart           Restart ggcode (picks up latest binary)
  /bug               Report a bug with diagnostics
  /exit, /quit       Exit ggcode

Keyboard shortcuts:
  Tab                Cycle autocomplete or approval choices
  Shift+Tab          Reverse cycle autocomplete, otherwise toggle permission mode
  Ctrl+R             Toggle sidebar
  Ctrl+N/P           New/previous session
  Ctrl+T             Open tunnel (mobile sharing)
  Enter              Send message / apply current selection
  Esc                Cancel autocomplete / exit idle shell mode
  Up/Down            Browse command history (or autocomplete)
  PgUp/PgDn          Scroll conversation output
  Ctrl+C             Cancel current activity, otherwise clear input then press again to exit
  Ctrl+D             Exit immediately
  Ctrl+A / Ctrl+E    Move cursor to start / end of line
  Ctrl+K             Delete from cursor to end of line
  Ctrl+U             Delete from start of line to cursor
  Ctrl+W             Delete word before cursor
  Ctrl+Backspace     Remove last attached image
  Shift+Enter        Insert newline (Ctrl+J or Alt+Enter in tmux)
  $ / !              Enter shell mode
  #                  Enter LAN Chat quick-send mode

Mouse:
  Option+drag / Shift+drag  Select text to copy (bypasses app mouse capture)
  Mouse wheel               Scroll conversation output`
	default:
		return enCatalog(key)
	}
}
