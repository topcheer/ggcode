package tui

// jaCatalog returns the Japanese translation for the given key.
func jaCatalog(key string) string {
	switch key {
	case "workspace.tagline":
		return "AIコーディングアシスタント"
	case "header.terminal_native":
		return "ネイティブターミナル"
	case "session.ephemeral":
		return "一時的"
	case "agents.idle":
		return "アイドル"
	case "agents.running":
		return "実行中"
	case "cron.firing":
		return "実行中"
	case "activity.idle":
		return "アイドル"
	case "panel.conversation":
		return "会話"
	case "panel.composer":
		return "入力"
	case "panel.composer_locked":
		return "ロック中"
	case "panel.commands":
		return "コマンド"
	case "panel.files":
		return "ファイル"
	case "panel.agent_status":
		return "エージェント状態"
	case "panel.mode_policy":
		return "モード"
	case "panel.session_usage":
		return "セッション使用量"
	case "panel.metrics":
		return "メトリクス"
	case "panel.context":
		return "コンテキスト"
	case "panel.im":
		return "IM"
	case "panel.mcp":
		return "MCP"
	case "panel.mcp.install_spec_required":
		return "最初にインストール仕様を入力してください。"
	case "panel.mcp.installing_server":
		return "MCP サーバーをインストール中..."
	case "panel.mcp.reconnect_unavailable":
		return "このセッションでは再接続できません。"
	case "panel.mcp.reconnecting":
		return "%s に再接続中..."
	case "panel.mcp.reconnect_failed":
		return "%s に再接続できません。"
	case "panel.mcp.uninstalling":
		return "%s をアンインストール中..."
	case "panel.startup":
		return "起動中..."
	case "panel.approval_required":
		return "承認が必要"
	case "panel.bypass_approval":
		return "承認バイパス"
	case "panel.review_file_change":
		return "変更を確認"
	case "label.vendor":
		return "ベンダー"
	case "label.endpoint":
		return "エンドポイント"
	case "label.model":
		return "モデル"
	case "label.mode":
		return "モード"
	case "label.session":
		return "セッション"
	case "label.agents":
		return "エージェント"
	case "label.cwd":
		return "作業ディレクトリ"
	case "label.branch":
		return "ブランチ"
	case "label.context":
		return "コンテキスト"
	case "label.skills":
		return "スキル"
	case "label.activity":
		return "アクティビティ"
	case "label.window":
		return "ウィンドウ"
	case "label.usage":
		return "使用量"
	case "label.compact":
		return "圧縮"
	case "label.total":
		return "合計"
	case "label.cost":
		return "コスト"
	case "label.approval_policy":
		return "承認"
	case "label.tool_policy":
		return "ツール"
	case "label.agent_policy":
		return "エージェント"
	case "label.tool":
		return "ツール"
	case "label.input":
		return "入力"
	case "label.output":
		return "出力"
	case "label.cache_read":
		return "キャッシュ読込"
	case "label.cache_write":
		return "キャッシュ書込"
	case "label.cache_hit":
		return "キャッシュヒット"
	case "label.turns":
		return "ターン"
	case "label.avg_ttft":
		return "平均TTFT"
	case "label.p95_ttft":
		return "P95 TTFT"
	case "label.avg_duration":
		return "平均時間"
	case "label.p95_duration":
		return "P95時間"
	case "label.avg_think":
		return "平均思考"
	case "label.fail_rate":
		return "失敗率"
	case "label.slow_tools":
		return "低速ツール"
	case "label.recent_turns":
		return "最近のターン"
	case "label.file":
		return "ファイル"
	case "label.directory":
		return "ディレクトリ"
	case "context.unavailable":
		return "コンテキストデータがまだありません"
	case "metrics.empty":
		return "メトリクスがまだありません"
	case "im.none":
		return "IMアダプタなし"
	case "im.summary":
		return "%d アダプター • %d 正常"
	case "im.more":
		return "+%d 件 (/qq)"
	case "im.runtime.available":
		return "ランタイム利用可能"
	case "im.runtime.disabled":
		return "無効"
	case "im.runtime.not_started":
		return "有効 • 再起動して初期化"
	case "im.status.not_started":
		return "未開始"
	case "context.until_compact":
		return "残り"
	case "empty.ask":
		return "リファクタリング、バグ修正、説明、テストなどを依頼してください。"
	case "empty.tips":
		return "ヒント: @path でファイルを含める、/? でヘルプ、Shift+Tab でモード切替"
	case "startup.banner":
		return "ターミナル UI を準備し、起動時のターミナルノイズをフィルタリングしています。すぐに入力できます; バナーは起動が完了すると消えます。"
	case "harness.views":
		return "ビュー"
	case "harness.items":
		return "アイテム"
	case "harness.action":
		return "アクション"
	case "harness.details":
		return "詳細"
	case "harness.none":
		return "（なし）"
	case "harness.unknown":
		return "不明"
	case "harness.unscoped":
		return "スコープ外"
	case "harness.unavailable":
		return "Harness は利用できません"
	case "harness.unavailable_intro":
		return "既存のプロジェクトでここから開始:"
	case "harness.unavailable_step_init":
		return "  1. Enter または i を押して harness を初期化"
	case "harness.unavailable_step_refresh":
		return "  2. 初期化完了後、r を押して更新"
	case "harness.section.init":
		return "初期化"
	case "harness.section.check":
		return "チェック"
	case "harness.section.doctor":
		return "ドクター"
	case "harness.section.monitor":
		return "モニター"
	case "harness.section.gc":
		return "GC"
	case "harness.section.contexts":
		return "コンテキスト"
	case "harness.section.tasks":
		return "タスク"
	case "harness.section.queue":
		return "キュー"
	case "harness.section.run":
		return "実行"
	case "harness.section.run_queued":
		return "キュー実行"
	case "harness.section.inbox":
		return "受信箱"
	case "harness.section.review":
		return "レビュー"
	case "harness.section.promote":
		return "プロモート"
	case "harness.section.release":
		return "リリース"
	case "harness.section.rollouts":
		return "ロールアウト"
	case "harness.hints.unavailable":
		return "Enter/i でハーネス初期化 • r 更新 • Esc 閉じる"
	case "harness.hints.move":
		return "j/k 移動"
	case "harness.hints.tab":
		return "Tab 切替"
	case "harness.hints.refresh":
		return "r 更新"
	case "harness.hints.close":
		return "Esc 閉じる"
	case "harness.hints.check":
		return "Enter チェック実行"
	case "harness.hints.monitor":
		return "Enter スナップショット更新"
	case "harness.hints.gc":
		return "Enter GC 実行"
	case "harness.hints.type_goal":
		return "目標を入力"
	case "harness.hints.queue":
		return "Enter キュー追加"
	case "harness.hints.run":
		return "Enter 実行"
	case "harness.hints.focus_input":
		return "Tab 入力フォーカス"
	case "harness.hints.rerun":
		return "Enter 失敗を再実行"
	case "harness.hints.next":
		return "Enter 次へ"
	case "harness.hints.all":
		return "a 全て"
	case "harness.hints.retry_failed":
		return "f 失敗を再試行"
	case "harness.hints.resume":
		return "s 再開"
	case "harness.hints.promote_owner":
		return "p オーナーをプロモート"
	case "harness.hints.retry_owner":
		return "f オーナーを再試行"
	case "harness.hints.approve":
		return "Enter 承認"
	case "harness.hints.reject":
		return "x 拒否"
	case "harness.hints.promote":
		return "Enter プロモート"
	case "harness.hints.apply_batch":
		return "Enter バッチ適用"
	case "harness.hints.advance":
		return "Enter 進行"
	case "harness.hints.approve_gate":
		return "g ゲート承認"
	case "harness.hints.pause_resume":
		return "p 一時停止/再開"
	case "harness.hints.abort":
		return "x 中止"
	case "harness.hint.primary.check":
		return "Enter を押してチェックを実行。"
	case "harness.hint.primary.monitor":
		return "Enter を押してモニタースナップショットを更新。"
	case "harness.hint.primary.gc":
		return "Enter を押してガベージコレクションを実行。"
	case "harness.hint.primary.queue":
		return "目標を入力後、Enter でキューに追加。"
	case "harness.hint.primary.run":
		return "目標を入力後、Enter で実行を開始。"
	case "harness.hint.primary.tasks":
		return "Enter を押して選択した失敗タスクを再実行。"
	case "harness.hint.primary.run_queued":
		return "Enter で次へ; a は全て実行; f は失敗を再試行; s は中断を再開。"
	case "harness.hint.primary.inbox":
		return "p でこのオーナーをプロモート、f で再試行。"
	case "harness.hint.primary.review":
		return "Enter で承認、x で拒否。"
	case "harness.hint.primary.promote":
		return "Enter を押して選択したタスクをプロモート。"
	case "harness.hint.primary.release":
		return "Enter を押して現在のリリースバッチを適用。"
	case "harness.hint.primary.rollouts":
		return "Enter で進行; g はゲート承認; p は一時停止/再開; x は中止。"
	case "harness.hint.primary.none":
		return "このセクションにはインライン入力は不要です。"
	case "harness.message.read_only":
		return "別の実行がアクティブな間、harness パネルは読み取り専用です。"
	case "harness.message.monitor_refreshed":
		return "Harness モニターを更新しました。"
	case "harness.message.rerun_failed_only":
		return "Harness タスク %s は %s です; 失敗したタスクのみ再実行できます。"
	case "harness.message.review_approved":
		return "%s のレビューを承認しました"
	case "harness.message.review_rejected":
		return "%s のレビューを拒否しました"
	case "harness.message.promoted":
		return "%s をプロモートしました"
	case "harness.message.no_release_tasks":
		return "リリース準備完了の harness タスクはありません。"
	case "harness.message.release_applied":
		return "リリースバッチ %s を適用しました"
	case "harness.message.no_rollouts":
		return "永続化されたロールアウトが見つかりません。"
	case "harness.message.rollout_advanced":
		return "ロールアウト %s を進行しました"
	case "harness.message.owner_promoted":
		return "%s の %d タスクをプロモートしました"
	case "harness.message.owner_retried":
		return "%s の失敗タスクを再試行しました"
	case "harness.message.gate_approved":
		return "%s の次のゲートを承認しました"
	case "harness.message.rollout_resumed":
		return "ロールアウト %s を再開しました"
	case "harness.message.rollout_paused":
		return "ロールアウト %s を一時停止しました"
	case "harness.message.rollout_aborted":
		return "ロールアウト %s を中止しました"
	case "harness.message.check_passed":
		return "Harness チェックが合格しました。"
	case "harness.message.check_failed":
		return "Harness チェックで問題が見つかりました。"
	case "harness.message.gc_complete":
		return "Harness GC が完了しました。"
	case "harness.message.queue_goal_required":
		return "パネル入力欄にキューゴールを入力してください。"
	case "harness.message.queued":
		return "Harness タスク %s をキューに追加しました"
	case "harness.activity.status":
		return "Harness ステータス: %s"
	case "harness.log.phase":
		return "フェーズ"
	case "harness.log.worker":
		return "ワーカー"
	case "harness.tool.read_file":
		return "ファイル読込"
	case "harness.tool.write_file":
		return "ファイル書込"
	case "harness.tool.browse_files":
		return "ファイル参照"
	case "harness.tool.search_code":
		return "コード検索"
	case "harness.tool.run_command":
		return "コマンド実行"
	case "harness.tool.fetch_web_page":
		return "Web ページ取得"
	case "harness.tool.run_subagent":
		return "サブエージェント実行"
	case "harness.tool.update_task_state":
		return "タスク状態更新"
	case "harness.message.run_goal_required":
		return "パネル入力欄に実行ゴールを入力してください。"
	case "harness.message.no_queued_executed":
		return "キュー内の harness タスクは実行されませんでした。"
	case "harness.message.queue_retried":
		return "失敗したキュータスク %d 件を再試行しました。"
	case "harness.message.queue_resumed":
		return "中断されたキュータスク %d 件を再開しました。"
	case "harness.message.queue_ran":
		return "キュータスク %d 件を実行しました。"
	case "harness.preview.not_initialized":
		return "このプロジェクトでは Harness はまだ初期化されていません。\n\nEnter または i を押して、現在のリポジトリで harness init を実行してください。"
	case "harness.preview.check":
		return "現在のプロジェクトに対して harness チェックを実行します。\n\nEnter: 必要なファイル/コンテンツ/コンテキストチェックと設定された検証コマンドを実行。"
	case "harness.preview.gc":
		return "Harness のガベージコレクションを実行します。\n\nEnter: 古いタスクをアーカイブ、ブロック中/実行中の作業を破棄、古いログと孤立したワークツリーを削除。"
	case "harness.preview.queue_help":
		return "ここに harness のゴールを入力し、Enter でキューに追加します。"
	case "harness.preview.run_help":
		return "ここに harness のゴールを入力し、Enter で実行を開始します。"
	case "harness.preview.run_queued":
		return "キューステータス:\nキュー=%d 実行中=%d ブロック=%d 失敗=%d\n\nEnter で次の実行可能タスクを実行。\na で全ての実行可能タスクを実行。\nf で失敗タスクを再試行。\ns で中断タスクを再開。"
	case "harness.preview.no_owner":
		return "Harness オーナーが選択されていません。"
	case "harness.preview.no_context":
		return "Harness コンテキストが選択されていません。"
	case "harness.preview.no_task":
		return "Harness タスクが選択されていません。"
	case "harness.preview.project_not_initialized":
		return "このプロジェクトでは Harness はまだ初期化されていません。"
	case "harness.preview.project_initialized":
		return "Harness は初期化されています。"
	case "harness.preview.project_help":
		return "/harness を使用してコントロールプレーンを操作します。"
	case "harness.preview.no_doctor":
		return "Harness 診断レポートがありません。"
	case "harness.preview.monitor_unavailable":
		return "Harness モニターは利用できません。"
	case "harness.label.context_title":
		return "コンテキスト"
	case "harness.label.owner_title":
		return "オーナー"
	case "harness.label.id":
		return "id"
	case "harness.label.status":
		return "ステータス"
	case "harness.label.goal":
		return "目標"
	case "harness.label.attempts":
		return "試行回数"
	case "harness.label.depends_on":
		return "依存関係"
	case "harness.label.context":
		return "コンテキスト"
	case "harness.label.workspace":
		return "ワークスペース"
	case "harness.label.branch":
		return "ブランチ"
	case "harness.label.worker":
		return "ワーカー"
	case "harness.label.progress":
		return "進捗"
	case "harness.label.verification":
		return "検証"
	case "harness.label.changed_files":
		return "変更ファイル"
	case "harness.label.delivery_report":
		return "delivery_report"
	case "harness.label.delivery_report_human":
		return "デリバリーレポート"
	case "harness.label.log":
		return "ログ"
	case "harness.label.review":
		return "レビュー"
	case "harness.label.review_notes":
		return "レビューメモ"
	case "harness.label.promotion":
		return "プロモーション"
	case "harness.label.promotion_notes":
		return "プロモーションメモ"
	case "harness.label.release_batch":
		return "リリースバッチ"
	case "harness.label.release_batch_human":
		return "リリースバッチ"
	case "harness.label.release_notes":
		return "リリースノート"
	case "harness.label.error":
		return "エラー"
	case "harness.label.name":
		return "名前"
	case "harness.label.description":
		return "説明"
	case "harness.label.owner":
		return "オーナー"
	case "harness.label.commands":
		return "コマンド"
	case "harness.label.tasks":
		return "タスク"
	case "harness.label.rollouts":
		return "ロールアウト"
	case "harness.label.gates":
		return "ゲート"
	case "harness.label.latest":
		return "最新"
	case "harness.label.repo":
		return "リポジトリ"
	case "harness.label.config":
		return "設定"
	case "harness.label.project":
		return "プロジェクト"
	case "harness.label.structure":
		return "構造"
	case "harness.label.contexts":
		return "コンテキスト"
	case "harness.label.workers":
		return "ワーカー"
	case "harness.label.workflow":
		return "ワークフロー"
	case "harness.label.quality":
		return "品質"
	case "harness.label.worktrees":
		return "ワークツリー"
	case "harness.label.snapshot":
		return "スナップショット"
	case "harness.label.events":
		return "イベント"
	case "harness.label.target":
		return "ターゲット"
	case "harness.label.review_ready":
		return "レビュー準備完了"
	case "harness.label.promotion_ready":
		return "プロモート準備完了"
	case "harness.label.retryable":
		return "再試行可"
	case "harness.task_title":
		return "Harness タスク"
	case "harness.doctor_title":
		return "Harness 診断"
	case "harness.monitor_title":
		return "Harness モニター"
	case "harness.latest_task":
		return "最新タスク"
	case "harness.latest_event":
		return "最新イベント"
	case "harness.focus":
		return "フォーカス"
	case "harness.status.ok":
		return "OK"
	case "harness.status.needs_attention":
		return "要対応"
	case "harness.group.review":
		return "レビュー"
	case "harness.group.promotion":
		return "プロモーション"
	case "harness.group.retry":
		return "リトライ"
	case "harness.review_ready_short":
		return "レビュー"
	case "harness.promote_ready_short":
		return "プロモート"
	case "harness.tasks_count":
		return "タスク"
	case "harness.input_empty":
		return "(入力ボックスが空です)"
	case "harness.no_waves":
		return "ウェーブなし"
	case "harness.mixed":
		return "混在"
	case "hint.autocomplete":
		return "Tab/Shift+Tab 切替 • Enter 適用 • Esc 閉じる"
	case "hint.mention":
		return "@ ファイル/フォルダ添付 • Tab/Shift+Tab 切替 • Enter 適用"
	case "hint.mode":
		return "モード"
	case "mode.approval.ask":
		return "毎回確認"
	case "mode.approval.none":
		return "承認不要"
	case "mode.approval.critical":
		return "重要な操作のみ確認"
	case "mode.tools.rules":
		return "ツールルール"
	case "mode.tools.readonly":
		return "読み取り専用"
	case "mode.tools.safe":
		return "安全"
	case "mode.tools.open":
		return "オープン"
	case "mode.agent.waits":
		return "エージェント待機"
	case "mode.agent.autocontinue":
		return "自動継続"
	case "hint.enter_send":
		return "Enter で送信"
	case "hint.ctrlv_image":
		return "Ctrl+V / Ctrl+Shift+V 画像貼り付け"
	case "hint.ctrlr_sidebar":
		return "Ctrl+R でサイドバー"
	case "hint.help":
		return "/help でヘルプ"
	case "hint.add_context":
		return "@ コンテキスト追加"
	case "hint.scroll":
		return "PgUp/PgDn スクロール"
	case "hint.shift_tab_mode":
		return "Shift+Tab でモード切替"
	case "hint.ctrlc_cancel":
		return "Ctrl+C でキャンセル"
	case "hint.ctrlc_exit":
		return "Ctrl+C クリア/終了"
	case "hint.image_attached":
		return "画像添付済み"
	case "hint.image_attached_count":
		return "%d 個の画像が添付されました"
	case "hint.follow_panel":
		return "Ctrl+N フォロー"
	case "hint.unfollow_panel":
		return "Ctrl+N フォロー解除"
	case "queued.count":
		return "%d 件キュー中"
	case "queued.output":
		return "[キュー %d 件保留中]\\n\\n"
	case "interrupt.delivered":
		return "[アクティブ実行に配信; 計画修正中]\\n"
	case "status.thinking":
		return "考え中..."
	case "status.writing":
		return "書き込み中..."
	case "status.cancelling":
		return "キャンセル中..."
	case "status.compacting":
		return "コンテキスト圧縮中..."
	case "status.compacted":
		return "コンテキスト圧縮完了"
	case "reasoning.effort.status":
		return "推論強度: %s"
	case "reasoning.effort.set":
		return "このセッションの推論強度を %s に設定しました"
	case "reasoning.effort.unsupported.status":
		return "現在のプロバイダーは推論強度をサポートしていません"
	case "reasoning.effort.unsupported":
		return "現在のプロバイダーは推論強度をサポートしていません"
	case "follow.loading":
		return "  フォロービューを読み込み中..."
	case "follow.active_agent":
		return "エージェント %s をフォロー中 — 入力一時停止。Esc で戻る。"
	case "follow.active_teammate":
		return "チームメイト %s をフォロー中 — 入力一時停止。Esc で戻る。"
	case "follow.status_running":
		return "実行中"
	case "follow.status_done":
		return "完了"
	case "follow.more":
		return "  +%d 件以上"
	case "follow.hint":
		return "  ↑↓←→ 切替  Esc 閉じる"
	case "status.tools_used":
		return "ツール使用"
	case "tool.done":
		return "完了"
	case "tool.failed":
		return "失敗"
	case "tool.no_output":
		return "出力なし"
	case "tool.output":
		return "出力"
	case "tool.content":
		return "内容"
	case "tool.match":
		return "一致"
	case "tool.matches":
		return "一致"
	case "tool.entry":
		return "エントリ"
	case "tool.result":
		return "結果"
	case "approval.rejected":
		return "拒否されました"
	case "approval.allow":
		return "許可"
	case "approval.allow_always":
		return "常に許可"
	case "approval.deny":
		return "拒否"
	case "approval.accept":
		return "承認"
	case "approval.reject":
		return "却下"
	case "exit.confirm":
		return "ggcodeを終了しますか？"
	case "cancel.confirm":
		return "現在のアクティビティをキャンセルしますか？"
	case "interrupted":
		return "中断されました"
	case "lang.current":
		return "現在の言語"
	case "lang.invalid":
		return "無効な言語"
	case "lang.switch":
		return "言語を切り替え"
	case "lang.selection.current":
		return "現在の言語"
	case "lang.selection.hint":
		return "言語を選択してください"
	case "lang.first_use.title":
		return " preferred言語を選択してください"
	case "lang.first_use.body":
		return " ggcodeが使用する言語を選択してください。"
	case "lang.first_use.hint":
		return " Tab/j/k 移動 • Enter 確認 • e/z ショートカット"
	case "mode.current":
		return "現在のモード"
	case "mode.persist_failed":
		return "モード設定の保存に失敗しました: %v"
	case "input.placeholder":
		return "メッセージを入力..."
	case "panel.model_filter.prompt":
		return "フィルター> "
	case "panel.model_filter.placeholder":
		return "入力してモデルをフィルター"
	case "panel.model_list.none":
		return "(なし)"
	case "panel.model_list.no_matches":
		return "(一致なし)"
	case "panel.model_list.showing":
		return "%d/%d モデル表示中"
	case "panel.model_list.hidden_above":
		return "%d 個上"
	case "panel.model_list.hidden_more":
		return "%d 個追加"
	case "panel.provider.vendors":
		return "プロバイダー"
	case "panel.provider.endpoints":
		return "エンドポイント"
	case "panel.provider.models":
		return "モデル"
	case "panel.provider.active_draft":
		return "アクティブドラフト"
	case "panel.provider.protocol":
		return "プロトコル"
	case "panel.provider.protocol.unknown":
		return "(不明)"
	case "panel.provider.auth":
		return "認証"
	case "panel.provider.env_var":
		return "環境変数"
	case "panel.provider.api_key":
		return "APIキー"
	case "panel.provider.api_key.missing":
		return "未設定"
	case "panel.provider.api_key.configured":
		return "設定済み"
	case "panel.provider.auth.connected":
		return "接続済み"
	case "panel.provider.auth.not_connected":
		return "未接続"
	case "panel.provider.base_url":
		return "ベースURL"
	case "panel.provider.base_url.not_set":
		return "(未設定)"
	case "panel.provider.enterprise_url":
		return "エンタープライズURL"
	case "panel.provider.tags":
		return "タグ"
	case "panel.provider.model.set_with_m":
		return "(mで設定)"
	case "panel.provider.edit":
		return "編集"
	case "panel.provider.edit.vendor_api_key":
		return "ベンダーAPIキー"
	case "panel.provider.edit.endpoint_api_key":
		return "エンドポイントAPIキー"
	case "panel.provider.edit.endpoint_base_url":
		return "エンドポイントベースURL"
	case "panel.provider.edit.custom_model":
		return "カスタムモデル"
	case "panel.provider.edit.new_endpoint_name":
		return "新規エンドポイント名"
	case "panel.provider.hint.edit":
		return "Enter 保存 • Esc キャンセル"
	case "panel.provider.hint.main":
		return "Tab/Shift+Tab フォーカス切替 • j/k 移動 • / フィルター • Enter/s 適用 • a ベンダーキー • u エンドポイントキー • b ベースURL • m カスタムモデル • e エンドポイント追加 • Esc 閉じる"
	case "panel.provider.hint.copilot":
		return "GitHub Copilot: l ログイン • x ログアウト • b エンタープライズドメイン編集"
	case "panel.provider.saved":
		return "保存しました。"
	case "panel.provider.saved_activated":
		return "保存して有効化しました。"
	case "panel.provider.login.starting":
		return "GitHub Copilotログインを開始しています..."
	case "panel.provider.login.instructions":
		return "%sを開いてコード%sを入力してください。認証を待機中..."
	case "panel.provider.login.copied":
		return "デバイスコードをクリップボードにコピーしました。"
	case "panel.provider.login.copy_failed":
		return "デバイスコードのコピーに失敗しました: %s"
	case "panel.provider.login.browser_opened":
		return "ブラウザで認証ページを開きました。"
	case "panel.provider.login.browser_failed":
		return "認証ページを開けませんでした: %s"
	case "panel.provider.login.success":
		return "GitHub Copilotに接続しました。"
	case "panel.provider.login.failed":
		return "GitHub Copilotログインに失敗しました: %s"
	case "panel.provider.logout.success":
		return "GitHub Copilotの接続を解除しました。"
	case "panel.provider.refreshing_vendor":
		return "%sのモデルを更新中..."
	case "panel.provider.refresh.save_failed":
		return "モデルを更新しましたが、設定の保存に失敗しました: %s"
	case "panel.provider.refresh.partial":
		return "%dエンドポイント更新、%dモデル発見。一部のエンドポイントが失敗しました: %v"
	case "panel.provider.refresh.success":
		return "%dエンドポイント更新、%dモデル発見。"
	case "panel.provider.refresh.failed":
		return "モデルの更新に失敗しました: %s"
	case "panel.provider.refresh.none":
		return "このベンダーには更新可能なエンドポイントがありません。"
	case "panel.model.models":
		return "モデル"
	case "panel.model.vendor":
		return "ベンダー"
	case "panel.model.endpoint":
		return "エンドポイント"
	case "panel.model.protocol":
		return "プロトコル"
	case "panel.model.source":
		return "ソース"
	case "panel.model.source.builtin":
		return "組み込み"
	case "panel.model.source.remote":
		return "リモート"
	case "panel.model.refreshing":
		return "最新モデルを更新中..."
	case "panel.model.hint.main":
		return "j/k 移動 • Enter/s 適用 • w コンテキストウィンドウ • o 最大トークン • r 更新 • / フィルター • Esc 閉じる"
	case "panel.model.hint.edit":
		return "Enter 保存 • Esc キャンセル (0または空 = 自動、K/M/G サフィックスOK 例: 256k)"
	case "panel.model.context_window":
		return "コンテキストウィンドウ"
	case "panel.model.max_tokens":
		return "最大出力トークン"
	case "panel.model.edit":
		return "編集"
	case "panel.model.saved_runtime_inactive":
		return "設定を保存しましたが、ランタイムが非アクティブです: %s"
	case "panel.model.context_applied":
		return "context_window=%d, max_tokens=%d を適用しました (保存済み)"
	case "panel.model.context_cleared":
		return "自動検出にリセットしました (保存済み)"
	case "panel.model.switched":
		return "モデルを %s に切り替えました。"
	case "panel.model.refresh.save_failed":
		return "モデルを更新しましたが、設定の保存に失敗しました: %s"
	case "panel.model.refresh.builtin_reason":
		return "内蔵モデルを使用中: %s"
	case "panel.model.refresh.remote_loaded":
		return "リモートモデル %d 件を読み込みました。"
	case "panel.model.refresh.builtin_loaded":
		return "内蔵モデルを読み込みました。"
	case "command.unknown":
		return "不明なコマンド"
	case "command.retry_empty":
		return "再試行する前回の送信がありません。"
	case "command.retry_busy":
		return "エージェントが実行中です。再試行前に現在の実行が完了するまでお待ちください。"
	case "command.edit_empty":
		return "前のメッセージがありません"
	case "command.edit_busy":
		return "エージェント実行中は編集できません。Ctrl+C でキャンセルしてください"
	case "command.edit_ready":
		return "メッセージを編集してください (Enter で再送信, Esc でキャンセル)"
	case "command.help_hint":
		return "/helpでコマンド一覧を表示\n\n"
	case "command.usage.allow":
		return "使用法: /allow <ツール名>\n\n"
	case "command.usage.resume":
		return "使用法: /resume <セッションID>\n\n"
	case "command.usage.export":
		return "使用法: /export <セッションID>\n\n"
	case "init.resolve_failed":
		return "初期化ターゲットの解決に失敗しました: %v\n\n"
	case "init.generate_failed":
		return "GGCODE.md コンテンツの生成に失敗しました: %v\n\n"
	case "init.collecting":
		return "プロジェクト知識を収集中..."
	case "init.prompt.title":
		return "プロジェクトを初期化"
	case "init.prompt.body":
		return "このプロジェクトに GGCODE.md が見つかりません。エージェントがコードベースの規約を理解できるように作成しますか？"
	case "init.prompt.yes":
		return "作成"
	case "init.prompt.no":
		return "スキップ"
	case "init.prompt.hint":
		return " y = GGCODE.md作成 • n/Esc = スキップ"
	case "command.model_switched":
		return "モデルを %s に切り替えました（ベンダー: %s）\n\n"
	case "command.model_failed":
		return "モデルの切り替えに失敗しました: %v\n\n"
	case "command.model_current":
		return "現在のモデル: %s（ベンダー: %s）\n利用可能なモデル: %s\n/model でモデルパネルを開くか、/model <モデル名> で直接切り替えできます。\n\n"
	case "command.provider_unknown":
		return "不明なベンダー: %s（利用可能: %v）\n\n"
	case "command.provider_switched":
		return "ベンダーを %s に切り替えました（モデル: %s）\n\n"
	case "command.provider_failed":
		return "プロバイダーの選択に失敗しました: %v\n\n"
	case "command.provider_current":
		return "現在のベンダー: %s（エンドポイント: %s、モデル: %s）\n利用可能なベンダー: %s\n利用可能なエンドポイント: %s\n使用法: /provider [ベンダー] [エンドポイント]\n\n"
	case "command.allow_set":
		return "%s を %s に設定しました"
	case "command.custom":
		return "カスタムコマンド"
	case "command.mention_error":
		return "メンション展開エラー: %v"
	case "session.list_failed":
		return "セッション一覧の取得エラー: %v\n\n"
	case "session.untitled":
		return "無題"
	case "session.store_missing":
		return "セッションストアが設定されていません。\n\n"
	case "session.none":
		return "セッションが見つかりません。\n\n"
	case "session.list.title":
		return "セッション一覧:\n\n"
	case "session.list.item":
		return "  %d. %s  %s  (%s)\n"
	case "session.list.hint":
		return "\n/resume <ID> でセッションを再開できます\n\n"
	case "session.new":
		return "新しいセッション"
	case "session.resume":
		return "セッションを再開しました: %s — %s（%d メッセージ）\n\n"
	case "session.resume_failed":
		return "セッション %s の再開に失敗しました: %v\n\n"
	case "session.resume_fallback":
		return "代わりに新しいセッションを開始します。\n\n"
	case "session.export_failed":
		return "セッションのエクスポートエラー: %v\n\n"
	case "session.write_failed":
		return "ファイルの書き込みエラー: %v\n\n"
	case "session.exported":
		return "セッション %s を %s にエクスポートしました\n\n"
	case "checkpoint.disabled":
		return "チェックポイントは無効です"
	case "checkpoint.undo_failed":
		return "チェックポイントのロールバックに失敗: %v"
	case "checkpoint.undid":
		return "チェックポイント %d にロールバックしました"
	case "checkpoint.none":
		return "チェックポイントがありません"
	case "files.disabled":
		return "ファイルブラウザ無効"
	case "files.none":
		return "このセッションでエージェントによって変更されたファイルはありません。\n\n"
	case "files.title":
		return "ファイル"
	case "files.item":
		return "  %s  %d 回編集  最終: %s%s\n"
	case "files.hint":
		return "ファイルを選択"
	case "checkpoint.list.title":
		return "チェックポイント一覧"
	case "checkpoint.list.item":
		return "チェックポイント %d: %s (%dファイル変更)"
	case "checkpoint.list.hint":
		return "チェックポイントを選択してロールバック (Esc でキャンセル)"
	case "memory.auto_unavailable":
		return "自動メモリが初期化されていません。\n\n"
	case "memory.list_failed":
		return "メモリ一覧の取得エラー: %v\n\n"
	case "memory.none":
		return "メモリがありません"
	case "memory.auto_title":
		return "自動メモリ:\n"
	case "memory.clear_failed":
		return "メモリのクリアエラー: %v\n\n"
	case "memory.cleared":
		return "すべての自動メモリをクリアしました。\n\n"
	case "memory.title":
		return "メモリ"
	case "memory.project":
		return "プロジェクト"
	case "memory.project_none":
		return "  プロジェクトメモリファイルが読み込まれていません。\n"
	case "memory.auto":
		return "自動メモリ:\n"
	case "memory.auto_none":
		return "  自動メモリが読み込まれていません。\n"
	case "memory.usage":
		return "\n使用法: /memory [list|clear]\n\n"
	case "compact.unavailable":
		return "コンテキストマネージャーが利用できません。\n\n"
	case "compact.failed":
		return "圧縮に失敗しました: %v\n\n"
	case "compact.done":
		return "圧縮完了"
	case "compact.done_with_stats":
		return "会話履歴を圧縮しました（%d → %d トークン）。\n\n"
	case "todo.cleared":
		return "TODOリストをクリアしました"
	case "todo.clear_failed":
		return "TODOのクリアエラー: %v\n\n"
	case "todo.none":
		return "TODO リストが見つかりません。todo_write ツールを使用して作成してください。\n\n"
	case "todo.read_failed":
		return "TODOの読み込みエラー: %v\n\n"
	case "todo.parse_failed":
		return "TODOのパースエラー: %v\n\n"
	case "todo.title":
		return "TODO"
	case "bug.title":
		return "バグレポート情報"
	case "bug.version":
		return "バージョン"
	case "bug.os":
		return "OS"
	case "bug.go":
		return "Goバージョン"
	case "bug.provider":
		return "プロバイダ"
	case "bug.model":
		return "モデル"
	case "bug.session":
		return "セッションID"
	case "bug.mcp":
		return "MCPサーバ"
	case "bug.last_error":
		return "最後のエラー"
	case "bug.hint":
		return "上記の情報をバグレポートに添付してください"
	case "config.usage":
		return "使用法: /config set <キー> <値>\n\nキー: model, vendor, endpoint, language, apikey [--vendor]\n\nエンドポイント: /config add-endpoint <名前> <ベースURL> [--protocol openai] [--apikey sk-xxx]\n             /config remove-endpoint <名前>\n\n"
	case "config.not_loaded":
		return "設定が読み込まれていません。\n\n"
	case "config.model_set":
		return "設定: model = %s\n\n"
	case "config.provider_set":
		return "設定: provider = %s\n\n"
	case "config.language_set":
		return "設定: language = %s\n\n"
	case "config.unknown_key":
		return "不明な設定キー: %s\n対応: model, provider, language\n\n"
	case "config.title":
		return "現在の設定:\n"
	case "status.title":
		return "ステータス"
	case "panel.update":
		return "更新"
	case "label.version":
		return "バージョン"
	case "label.latest":
		return "最新"
	case "update.sidebar_hint":
		return "新しいリリースが利用可能です。/update を実行してください。"
	case "update.up_to_date":
		return "最新です"
	case "update.available":
		return "更新が利用可能"
	case "update.current":
		return "現在: %s (最新: %s)"
	case "update.unknown":
		return "未確認"
	case "update.check_failed":
		return "確認失敗: %s"
	case "update.unavailable":
		return "このセッションではアップデートを利用できません。\n\n"
	case "update.preparing":
		return "アップデート準備中"
	case "update.failed":
		return "更新に失敗しました: %v"
	case "update.restart_failed":
		return "アップデートの準備が完了しましたが、再起動に失敗しました: %v\n\n"
	case "update.pm_hint.brew":
		return "アップデートがインストールされました。注意: ggcode は Homebrew 経由でインストールされています。\nHomebrew を同期するには `brew upgrade ggcode` を実行してください。\n\n"
	case "update.pm_hint.scoop":
		return "アップデートがインストールされました。注意: ggcode は Scoop 経由でインストールされています。\nScoop を同期するには `scoop update ggcode` を実行してください。\n\n"
	case "update.pm_hint.winget":
		return "アップデートがインストールされました。注意: ggcode は winget 経由でインストールされています。\nwinget を同期するには `winget upgrade ggcode` を実行してください。\n\n"
	case "update.pm_hint.snap":
		return "アップデートがインストールされました。注意: ggcode は Snap 経由でインストールされています。\nSnap を同期するには `sudo snap refresh ggcode` を実行してください。\n\n"
	case "update.other_installs":
		return "このシステムで他の ggcode インストールが検出されました:\n%s\n別の ggcode が PATH の先頭にある場合、そちらも更新するか PATH の順序を調整することを検討してください。\n\n"
	case "update.dual_scope":
		return "警告: ユーザーとシステム全体の両方に ggcode インストールが見つかりました:\n  ユーザー: %s\n  システム: %s\nPATH の競合が発生する可能性があります。設定 > アプリからどちらかをアンインストールすることを検討してください。\n\n"
	case "plugins.unavailable":
		return "プラグインマネージャーが利用できません。\n\n"
	case "plugins.none":
		return "プラグインが読み込まれていません"
	case "plugins.title":
		return "プラグイン"
	case "mcp.none":
		return "MCP サーバが設定されていません。\n\n"
	case "mcp.title":
		return "MCPサーバ"
	case "mcp.active_tools":
		return "アクティブツール"
	case "mcp.more":
		return "… %d件更多 • /mcp"
	case "image.usage":
		return "使用法: /image <ファイルパス> または /image paste\n"
	case "image.formats":
		return "対応形式: PNG, JPEG, GIF, WebP（最大20MB）\n\n"
	case "image.attached":
		return "画像を添付しました: %s\n"
	case "image.attached_hint":
		return "画像を含めるにはメッセージを送信するか、/image で別の画像を添付できます。\n\n"
	case "image.clipboard_failed":
		return "クリップボードから画像を貼り付けできませんでした: %v"
	case "image.clipboard_no_image_windows":
		return "クリップボードに画像が見つかりません。WindowsではCtrl+Vが端末に遮断されることがあります。Ctrl+Shift+Vまたは/image pasteを試してください。"
	case "agents.unavailable":
		return "サブエージェント管理が利用できません"
	case "agents.none":
		return "アクティブなサブエージェントがありません"
	case "agents.title":
		return "サブエージェント"
	case "agents.item":
		return "  %s [%s]%s - %s\n"
	case "agents.hint":
		return "タブでサブエージェントを選択、Enter で送信"
	case "agent.usage":
		return "%s: %d ターン, %d ツールコール, %s トークン使用, %s経過"
	case "agent.cancelled":
		return "エージェントがキャンセルされました"
	case "agent.cancel_failed":
		return "エージェントのキャンセルに失敗: %v"
	case "agent.not_found":
		return "エージェント %s が見つかりません"
	case "agent.title":
		return "エージェント"
	case "agent.result":
		return "サブエージェント %s が完了しました: %s"
	case "agent.error":
		return "エージェントエラー: %v"
	case "slash.help":
		return "ヘルプを表示"
	case "slash.sessions":
		return "セッション一覧を表示"
	case "slash.resume":
		return "セッションを復元"
	case "slash.export":
		return "セッションをエクスポート"
	case "slash.model":
		return "モデルを表示/切り替え"
	case "slash.provider":
		return "プロバイダを管理"
	case "slash.clear":
		return "会話履歴をクリア"
	case "slash.mcp":
		return "MCPサーバを表示"
	case "slash.memory":
		return "メモリを表示"
	case "slash.undo":
		return "最後の編集を取り消す"
	case "slash.files":
		return "ファイルブラウザを開く"
	case "slash.checkpoints":
		return "チェックポイント一覧を表示"
	case "slash.allow":
		return "ツールを常に許可"
	case "slash.plugins":
		return "プラグイン一覧を表示"
	case "slash.image":
		return "画像を添付"
	case "slash.init":
		return "GGCODE.md を生成"
	case "slash.harness":
		return "ハーネスコマンドを実行"
	case "slash.lang":
		return "言語を切り替え"
	case "slash.skills":
		return "スキル一覧を表示"
	case "slash.exit":
		return "ggcode を終了"
	case "slash.compact":
		return "コンテキストを圧縮"
	case "slash.todo":
		return "TODOリストを表示"
	case "slash.bug":
		return "バグレポートを表示"
	case "slash.config":
		return "設定を表示"
	case "slash.qq":
		return "QQチャネルバインディング管理"
	case "slash.telegram":
		return "Telegramチャネルバインディング管理"
	case "slash.pc":
		return "PCチャネルバインディング管理"
	case "slash.discord":
		return "Discordチャネルバインディングを管理"
	case "slash.feishu":
		return "Feishuチャネルバインディングを管理"
	case "slash.slack":
		return "Slackチャネルバインディングを管理"
	case "slash.dingtalk":
		return "DingTalkチャネルバインディングを管理"
	case "slash.wechat":
		return "WeChatチャネルバインディングを管理"
	case "slash.wecom":
		return "WeCom（エンタープライズWeChat）チャネルバインディングを管理"
	case "slash.mattermost":
		return "Mattermostチャネルバインディングを管理"
	case "slash.matrix":
		return "Matrixチャネルバインディングを管理"
	case "slash.signal":
		return "Signalチャネルバインディングを管理"
	case "slash.irc":
		return "IRCチャネルバインディングを管理"
	case "slash.nostr":
		return "Nostrチャネルバインディングを管理"
	case "slash.twitch":
		return "Twitchチャネルバインディングを管理"
	case "slash.whatsapp":
		return "WhatsAppチャネルバインディングを管理"
	case "slash.impersonate":
		return "シェルプロンプト表示用にCLIツールを偽装"
	case "slash.knight":
		return "自律バックグラウンドエージェントを管理"
	case "slash.stream":
		return "ストリーミング出力モードを設定"
	case "slash.diff":
		return "git diff を表示"
	case "slash.hooks":
		return "フックを表示"
	case "slash.cost":
		return "コスト統計を表示"
	case "slash.review":
		return "AIコードレビュー"
	case "slash.copy":
		return "会話をコピー"
	case "slash.context":
		return "コンテキスト情報を表示"
	case "slash.im":
		return "IMアダプタを管理"
	case "panel.qq.directory":
		return "ディレクトリ"
	case "panel.qq.runtime":
		return "ランタイム"
	case "panel.qq.bots":
		return "QQボット"
	case "panel.qq.created":
		return "作成済み: %d"
	case "panel.qq.bound":
		return "バインド済み: %d"
	case "panel.qq.available":
		return "利用可能: %d"
	case "panel.qq.current_binding":
		return "現在のバインド"
	case "panel.qq.none":
		return "（なし）"
	case "panel.qq.default":
		return "（デフォルト）"
	case "panel.qq.adapter":
		return "アダプタ: %s"
	case "panel.qq.target":
		return "ターゲット: %s"
	case "panel.qq.channel":
		return "チャネル: %s"
	case "panel.qq.bot_list":
		return "QQ Bot リスト"
	case "panel.qq.no_bots":
		return "QQ Botが設定されていません。"
	case "panel.qq.entry.available":
		return "利用可能"
	case "panel.qq.entry.bound":
		return "バインド済み"
	case "panel.qq.entry.active":
		return "アクティブ"
	case "panel.qq.entry.bound_other":
		return "バインド: %s"
	case "panel.qq.entry.muted":
		return "ミュート"
	case "panel.qq.details":
		return "詳細"
	case "panel.qq.status":
		return "ステータス: %s"
	case "panel.qq.transport":
		return "トランスポート: %s"
	case "panel.qq.bound_directory":
		return "バインドディレクトリ: %s"
	case "panel.qq.current_directory_target":
		return "現在のディレクトリターゲット: %s"
	case "panel.qq.current_directory_channel":
		return "現在のディレクトリチャネル: %s"
	case "panel.qq.waiting_for_pairing":
		return "（ペアリング待ち）"
	case "panel.qq.last_error":
		return "最終エラー: %s"
	case "panel.qq.occupied_by":
		return "占有: %s"
	case "panel.qq.create":
		return "作成"
	case "panel.qq.bot_input":
		return "QQボット: %s"
	case "panel.qq.create_format":
		return "形式: <bot-id> <appid> <appsecret>"
	case "panel.qq.create_example":
		return "例: qq-main 123456 secret-value"
	case "panel.qq.create_hint":
		return "Enter でBot作成 • Esc でキャンセル"
	case "panel.qq.actions_hint":
		return "j/k 移動 • Enter または b でBotバインド • c でチャネルバインド • x でチャネルバインド解除 • u でBotバインド解除 • i でBot作成 • Esc で閉じる"
	case "panel.qq.bind_channel":
		return "チャネルをバインド"
	case "panel.qq.scan_hint":
		return "QRコードをスキャンし、Botを追加して、メッセージを送信してペアリングを開始してください。"
	case "panel.qq.qr_code":
		return "QRコード:"
	case "panel.qq.share_link":
		return "共有リンク:"
	case "panel.qq.message.no_bot":
		return "利用可能なQQ Botがありません。"
	case "panel.qq.message.bound_success":
		return "QQ Botを現在のワークスペースにバインドしました。c を押してチャネルバインドQRコードを生成してください。"
	case "panel.qq.message.share_generated":
		return "QQ共有リンクを生成しました。QRコードをスキャンし、Botを追加して、メッセージを送信してペアリングを開始してください。"
	case "panel.qq.message.unbound":
		return "QQチャネルのバインドを解除しました。"
	case "panel.qq.message.cleared":
		return "現在のワークスペースのQQチャネル認証をクリアしました。"
	case "panel.qq.message.added_bot":
		return "QQ Bot %s を追加しました。"
	case "panel.qq.error.config_unavailable":
		return "設定が利用できません"
	case "panel.qq.error.config_format":
		return "QQ Bot設定の形式: <bot-id> <appid> <appsecret>"
	case "panel.qq.error.adapter_required":
		return "QQアダプタ名が必要です"
	case "panel.qq.error.not_configured":
		return "QQ Bot %q は設定されていません"
	case "panel.qq.error.disabled":
		return "QQ Bot %q は無効です"
	case "panel.qq.error.not_qq_adapter":
		return "アダプタ %q はQQ Botではありません"
	case "panel.qq.error.not_online":
		return "QQ Bot %q はオンラインではありません"
	case "panel.qq.error.not_online_detail":
		return "QQ Bot %q はオンラインではありません: %s"
	case "panel.qq.runtime.available":
		return "利用可能"
	case "panel.qq.runtime.disabled":
		return "無効（im.enabled: true を設定してggcodeを再起動）"
	case "panel.qq.runtime.not_started":
		return "未起動（ggcodeを再起動してIMランタイムを初期化）"
	case "panel.qq.status.not_started":
		return "未起動"
	case "panel.qq.status.unknown":
		return "不明"
	case "slash.status":
		return "ステータスを表示"
	case "slash.update":
		return "更新を確認"
	case "slash.cron":
		return "定期タスクを管理"
	case "slash.branch":
		return "ブランチ情報を表示"
	case "slash.chat":
		return "チャットモード"
	case "slash.edit":
		return "編集モード"
	case "slash.inspector":
		return "インスペクタパネルを切替"
	case "slash.mode":
		return "権限モードを表示/切り替え"
	case "slash.nick":
		return "ニックネームを設定"
	case "slash.reflect":
		return "実行を振り返り"
	case "slash.regenerate":
		return "最後の応答を再生成"
	case "slash.restart":
		return "エージェントを再起動"
	case "slash.retry":
		return "最後のリクエストを再試行"
	case "slash.rules":
		return "ラチェットルールを表示"
	case "slash.share":
		return "セッションを共有"
	case "slash.stats":
		return "統計を表示"
	case "slash.tmux":
		return "tmuxパネル"
	case "slash.tunnel":
		return "トンネルを管理"
	case "slash.unshare":
		return "共有を解除"
	case "regenerate.busy":
		return "エージェント実行中は再生成できません。Ctrl+C でキャンセルしてください。"
	case "regenerate.no_agent":
		return "エージェントが初期化されていません。"
	case "regenerate.no_context":
		return "コンテキストマネージャが利用できません。"
	case "regenerate.no_response":
		return "再生成する応答がありません。"
	case "branch.busy":
		return "エージェント実行中はブランチできません。Ctrl+C でキャンセルしてください。"
	case "branch.no_session":
		return "ブランチするアクティブセッションがありません。"
	case "branch.empty":
		return "セッションにブランチするメッセージがありません。"
	case "branch.save_failed":
		return "ブランチセッションの作成に失敗しました: %v"
	case "branch.success":
		return "新しいセッション %s にブランチしました（元: %s）。元のセッションは保持されます。"
	case "help.text":
		return `利用可能なコマンド:

セッション & 履歴:
  /help, /?          このヘルプメッセージを表示
  /sessions          保存済みセッション一覧を表示
  /resume <id>       以前のセッションを復元
  /export <id>       セッションをMarkdownファイルにエクスポート
  /clear             会話履歴をクリア
  /compact           会話履歴を圧縮（手動）
  /undo              最後のファイル編集を取り消す（チェックポイントロールバック）
  /checkpoints       ファイル編集チェックポイント一覧を表示
  /regenerate        最後の応答を破棄して再生成（エイリアス: /regen）
  /branch            現在の会話を新しいセッションにフォーク（エイリアス: /fork）

モデル & プロバイダ:
  /model [name]      モデルパネルを開く、または直接切り替え
  /provider [vendor] プロバイダマネージャを開く
  /mode <mode>       エージェントモードを設定 (supervised|plan|auto|bypass|autopilot)

開発:
  /diff [opts]       git diff をチャットに表示 (--cached, --stat, <file>)
  /review [opts]     現在の変更のAIコードレビュー (--cached, --staged)
  /copy              最後の応答をクリップボードにコピー
  /cost              セッショントークン使用量と推定コストを表示
  /context           コンテキストウィンドウ使用量の内訳を表示
  /hooks             設定済みフックを表示
  /init              現在のプロジェクトから GGCODE.md を生成
  /harness ...       ハーネスコントロールプレーンコマンドを実行
  /todo              TODOリストを表示
  /todo clear        TODOリストをクリア

統合:
  /im                統合IMチャネルパネルを開く
  /mcp               接続済みMCPサーバとツールを表示
  /plugins           読み込み済みプラグインとツールを一覧表示
  /skills            利用可能なスキルを表示
  /memory            読み込み済みメモリファイルを表示
  /agents            サブエージェント一覧を表示
  /cron <sub>        定期ジョブを管理 (list|get|pause|resume|pauseall|resumeall)

システム:
  /lang [code]       インターフェース言語を選択/切り替え
  /config            現在の設定を表示
  /config set <k> <v> 設定値を設定
  /status            現在のステータスを表示
  /update            ggcode を最新版に更新
  /restart           ggcode を再起動（最新バイナリを取得）
  /bug               診断情報付きでバグを報告
  /exit, /quit       ggcode を終了

キーボードショートカット:
  Tab                補完または承認選択をサイクル
  Shift+Tab          補完を逆サイクル、または権限モードを切替
  Ctrl+R             サイドバーを切替
  Ctrl+N/P           新規/前のセッション
  Ctrl+T             トンネルを開く（モバイル共有）
  Enter              メッセージ送信 / 現在の選択を適用
  Esc                補完をキャンセル / アイドルシェルモードを終了
  Up/Down            コマンド履歴を表示（または補完）
  PgUp/PgDn          会話出力をスクロール
  Ctrl+C             現在の操作をキャンセル、入力をクリア、もう一度で終了
  Ctrl+D             即座に終了
  Ctrl+A / Ctrl+E    行の先頭 / 末尾にカーソル移動
  Ctrl+K             カーソルから行末まで削除
  Ctrl+U             行頭からカーソルまで削除
  Ctrl+W             カーソル前の単語を削除
  Ctrl+Backspace     最後に添付した画像を削除
  Shift+Enter        改行を挿入（tmuxでは Ctrl+J または Alt+Enter）
  $ / !              シェルモードに入る
  #                  LAN Chat クイック送信モードに入る

マウス:
  Option+ドラッグ / Shift+ドラッグ  テキストを選択してコピー（アプリのマウスキャプチャをバイパス）`
	case "command.harness_usage":
		return "使用法: /harness <init|check|queue|tasks|run|rerun|run-queued|monitor|contexts|inbox|review|promote|release|gc|doctor> ... (release は rollouts|advance|pause|resume|abort|approve|reject をサポート)"
	case "command.harness_queue_usage":
		return "使用法: /harness queue <目標>"
	case "command.harness_run_usage":
		return "使用法: /harness run <目標>"
	case "command.harness_rerun_usage":
		return "使用法: /harness rerun <タスクID>"
	case "command.skill_agent_only":
		return "スキル %s はエージェントのみ実行できます。"
	case "command.harness_owner_promoted":
		return "オーナー %s のハーネスタスク %d 件をプロモートしました。"
	case "command.harness_review_approved":
		return "ハーネスタスク %s を承認しました。"
	case "command.harness_review_rejected":
		return "ハーネスタスク %s を拒否しました。"
	case "command.harness_promoted_many":
		return "ハーネスタスク %d 件をプロモートしました。"
	case "command.harness_promoted_one":
		return "ハーネスタスク %s をプロモートしました。"
	case "command.harness_task_queued_detail":
		return "ハーネスタスク %s をキューに追加しました。\n- 目標: %s"
	case "command.harness_tasks_empty":
		return "記録されたハーネスタスクがありません。"
	case "command.harness_run_start":
		return "追跡ハーネス実行を開始しています...\n/harness monitor またはタスク/モニタビューでライブ状態を確認してください。"
	case "command.harness_rerun_start":
		return "追跡ハーネス再実行を開始しています...\n/harness monitor またはタスク/モニタビューでライブ状態を確認してください。"
	case "command.harness_rerun_invalid_status":
		return "ハーネスタスク %s は %s です。失敗したタスクのみ再実行できます。"
	case "command.harness_status_starting_run":
		return "ハーネス実行を開始中..."
	case "command.harness_status_starting_rerun":
		return "ハーネス再実行を開始中..."
	case "command.harness_spinner_running":
		return "ハーネス実行中"
	case "command.harness_cancelled":
		return "ハーネス実行がキャンセルされました。"
	case "tunnel.stopped":
		return "トンネルを停止しました"
	case "tunnel.not_active":
		return "アクティブな共有セッションがありません"
	case "tunnel.mobile_connected":
		return "モバイルクライアントが接続されました"
	case "config.save_scope_global":
		return "保存先 → グローバル"
	case "config.save_scope_instance":
		return "保存先 → インスタンス"
	case "config.save_scope_instance_new":
		return "保存先 → インスタンス（新規設定が作成されます）"
	case "config.instance_unavailable":
		return "このワークスペースではインスタンス設定が利用できません"
	case "config.scope_instance":
		return "インスタンス"
	case "config.scope_global":
		return "グローバル"
	case "config.save_target_new_hint":
		return " （保存時に新規設定が作成されます）"
	case "config.save_target_line":
		return " 保存先: %s%s  [Ctrl+T 切替]"
	case "shell.empty":
		return "シェルコマンドが空です"
	case "lanchat.unavailable":
		return "LAN Chat は利用できません"
	case "reflect.no_agent":
		return "エージェントが初期化されていません。"
	case "reflect.no_workdir":
		return "作業ディレクトリが設定されていません。"
	case "reflect.no_memory":
		return "このディレクトリのプロジェクトメモリが利用できません。"
	case "reflect.load_failed":
		return "インサイトの読み込みに失敗しました: %v"
	case "reflect.empty":
		return "実行インサイトがまだありません。3回以上のツール呼び出しまたはファイル編集を含むエージェント実行後に自動生成されます。"
	case "reflect.title":
		return "## 蓄積された実行インサイト\n\n"
	case "reflect.memory_location":
		return "メモリの場所: %s\n"
	case "knight.unavailable":
		return "Knight は利用できません"
	case "pairing.rejected":
		return "現在のペアリングリクエストは拒否されました。続行するには再開始してください。"
	case "pairing.blacklisted":
		return "複数回の拒否により、このチャネルはブラックリストに登録されました。"

	default:
		if v, ok := lookupModuleCatalog(LangEnglish, key); ok {
			return v
		}
		return key
	}
}
