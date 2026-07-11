package tui

// viCatalog returns the Vietnamese translation for the given key.
// Keys not yet translated fall through to enCatalog.
func viCatalog(key string) string {
	switch key {
	case "workspace.tagline":
		return "không gian làm việc AI geek"
	case "header.terminal_native":
		return "AI lập trình gốc terminal"
	case "session.ephemeral":
		return "tạm thời"
	case "agents.idle":
		return "chờ"
	case "agents.running":
		return "%d đang chạy"
	case "cron.firing":
		return "Cron job đã kích hoạt"
	case "activity.idle":
		return "chờ"
	case "panel.conversation":
		return "Hội thoại"
	case "panel.composer":
		return "Soạn thảo"
	case "panel.composer_locked":
		return "Soạn thảo đã khóa"
	case "panel.commands":
		return "Lệnh:"
	case "panel.files":
		return "Tệp:"
	case "panel.agent_status":
		return "Trạng thái agent"
	case "panel.mode_policy":
		return "Chính sách chế độ"
	case "panel.session_usage":
		return "Sử dụng phiên"
	case "panel.metrics":
		return "Chỉ số"
	case "panel.context":
		return "Ngữ cảnh"
	case "panel.im":
		return "IM"
	case "panel.mcp":
		return "MCP"
	case "panel.mcp.install_spec_required":
		return "Nhập đặc tả cài đặt trước."
	case "panel.mcp.installing_server":
		return "Đang cài đặt MCP server..."
	case "panel.mcp.reconnect_unavailable":
		return "Kết nối lại không khả dụng trong phiên này."
	case "panel.mcp.reconnecting":
		return "Đang kết nối lại %s..."
	case "panel.mcp.reconnect_failed":
		return "Không thể kết nối lại %s."
	case "panel.mcp.uninstalling":
		return "Đang gỡ cài đặt %s..."
	case "panel.startup":
		return "Đang khởi tạo"
	case "panel.approval_required":
		return "Cần phê duyệt"
	case "panel.bypass_approval":
		return "Phê duyệt chế độ bypass"
	case "panel.review_file_change":
		return "Xem thay đổi tệp"
	case "label.vendor":
		return "nhà cung cấp"
	case "label.endpoint":
		return "endpoint"
	case "label.model":
		return "mô hình"
	case "label.mode":
		return "chế độ"
	case "label.session":
		return "phiên"
	case "label.agents":
		return "tác tử"
	case "label.cwd":
		return "thư mục hiện tại"
	case "label.branch":
		return "nhánh"
	case "label.context":
		return "ngữ cảnh"
	case "label.skills":
		return "kỹ năng"
	case "label.activity":
		return "hoạt động"
	case "label.window":
		return "cửa sổ"
	case "label.usage":
		return "sử dụng"
	case "label.compact":
		return "nén"
	case "label.total":
		return "tổng"
	case "label.cost":
		return "chi phí ước tính"
	case "label.approval_policy":
		return "phê duyệt"
	case "label.tool_policy":
		return "công cụ"
	case "label.agent_policy":
		return "tác tử"
	case "label.tool":
		return "công cụ"
	case "label.input":
		return "đầu vào"
	case "label.output":
		return "đầu ra"
	case "label.cache_read":
		return "đọc cache"
	case "label.cache_write":
		return "ghi cache"
	case "label.cache_hit":
		return "trúng cache"
	case "label.turns":
		return "lượt"
	case "label.avg_ttft":
		return "ttft tb"
	case "label.p95_ttft":
		return "ttft p95"
	case "label.avg_duration":
		return "tg tb"
	case "label.p95_duration":
		return "tg p95"
	case "label.avg_think":
		return "suy nghĩ tb"
	case "label.fail_rate":
		return "tỷ lệ lỗi"
	case "label.slow_tools":
		return "công cụ chậm"
	case "label.recent_turns":
		return "lượt gần đây"
	case "label.file":
		return "tệp"
	case "label.directory":
		return "thư mục"
	case "context.unavailable":
		return "Chưa có dữ liệu ngữ cảnh"
	case "metrics.empty":
		return "Chưa có chỉ số"
	case "im.none":
		return "Chưa cấu hình adapter nào"
	case "im.summary":
		return "%d adapter • %d hoạt động tốt"
	case "im.more":
		return "+%d nữa (/qq)"
	case "im.runtime.available":
		return "runtime khả dụng"
	case "im.runtime.disabled":
		return "đã tắt"
	case "im.runtime.not_started":
		return "đã bật • khởi động lại để khởi tạo"
	case "im.status.not_started":
		return "chưa bắt đầu"
	case "context.until_compact":
		return "còn lại"
	case "empty.ask":
		return "Yêu cầu tái cấu trúc, sửa lỗi, giải thích, hoặc kiểm thử."
	case "empty.tips":
		return "Mẹo: dùng @path để thêm tệp, /? để xem trợ giúp, Shift+Tab để đổi chế độ."
	case "startup.banner":
		return "Đang chuẩn bị giao diện terminal và lọc nhiễu khởi động. Bạn có thể gõ ngay; banner này sẽ biến mất khi khởi động hoàn tất."
	case "harness.views":
		return "Chế độ xem"
	case "harness.items":
		return "Mục"
	case "harness.action":
		return "Hành động"
	case "harness.details":
		return "Chi tiết"
	case "harness.none":
		return "(không có)"
	case "harness.unknown":
		return "không xác định"
	case "harness.unscoped":
		return "không phạm vi"
	case "harness.unavailable":
		return "Harness không khả dụng"
	case "harness.unavailable_intro":
		return "Bắt đầu tại đây trong dự án hiện có:"
	case "harness.unavailable_step_init":
		return "  1. Nhấn Enter hoặc i để khởi tạo harness"
	case "harness.unavailable_step_refresh":
		return "  2. Nhấn r để làm mới khi khởi tạo xong"
	case "harness.section.init":
		return "Khởi tạo"
	case "harness.section.check":
		return "Kiểm tra"
	case "harness.section.doctor":
		return "Doctor"
	case "harness.section.monitor":
		return "Giám sát"
	case "harness.section.gc":
		return "GC"
	case "harness.section.contexts":
		return "Ngữ cảnh"
	case "harness.section.tasks":
		return "Tác vụ"
	case "harness.section.queue":
		return "Hàng đợi"
	case "harness.section.run":
		return "Chạy"
	case "harness.section.run_queued":
		return "Chạy hàng đợi"
	case "harness.section.inbox":
		return "Hộp thư"
	case "harness.section.review":
		return "Duyệt"
	case "harness.section.promote":
		return "Thăng cấp"
	case "harness.section.release":
		return "Phát hành"
	case "harness.section.rollouts":
		return "Triển khai"
	case "harness.hints.unavailable":
		return "Enter/i khởi tạo harness • r làm mới • Esc đóng"
	case "harness.hints.move":
		return "j/k di chuyển"
	case "harness.hints.tab":
		return "Tab chuyển"
	case "harness.hints.refresh":
		return "r làm mới"
	case "harness.hints.close":
		return "Esc đóng"
	case "harness.hints.check":
		return "Enter chạy kiểm tra"
	case "harness.hints.monitor":
		return "Enter làm mới ảnh chụp"
	case "harness.hints.gc":
		return "Enter chạy gc"
	case "harness.hints.type_goal":
		return "nhập mục tiêu"
	case "harness.hints.queue":
		return "Enter thêm vào hàng đợi"
	case "harness.hints.run":
		return "Enter chạy"
	case "harness.hints.focus_input":
		return "Tab tập trung đầu vào"
	case "harness.hints.rerun":
		return "Enter chạy lại lỗi"
	case "harness.hints.next":
		return "Enter tiếp theo"
	case "harness.hints.all":
		return "a tất cả"
	case "harness.hints.retry_failed":
		return "f thử lại lỗi"
	case "harness.hints.resume":
		return "s tiếp tục"
	case "harness.hints.promote_owner":
		return "p thăng cấp chủ sở hữu"
	case "harness.hints.retry_owner":
		return "f thử lại chủ sở hữu"
	case "harness.hints.approve":
		return "Enter phê duyệt"
	case "harness.hints.reject":
		return "x từ chối"
	case "harness.hints.promote":
		return "Enter thăng cấp"
	case "harness.hints.apply_batch":
		return "Enter áp dụng lô"
	case "harness.hints.advance":
		return "Enter tiến lên"
	case "harness.hints.approve_gate":
		return "g phê duyệt gate"
	case "harness.hints.pause_resume":
		return "p tạm dừng/tiếp tục"
	case "harness.hints.abort":
		return "x hủy bỏ"
	case "harness.hint.primary.check":
		return "Nhấn Enter để chạy kiểm tra."
	case "harness.hint.primary.monitor":
		return "Nhấn Enter để làm mới ảnh chụp giám sát."
	case "harness.hint.primary.gc":
		return "Nhấn Enter để chạy thu gom rác."
	case "harness.hint.primary.queue":
		return "Nhập mục tiêu, sau đó nhấn Enter để thêm vào hàng đợi."
	case "harness.hint.primary.run":
		return "Nhập mục tiêu, sau đó nhấn Enter để bắt đầu chạy."
	case "harness.hint.primary.tasks":
		return "Nhấn Enter để chạy lại tác vụ lỗi đã chọn."
	case "harness.hint.primary.run_queued":
		return "Nhấn Enter cho tác vụ tiếp theo; a chạy tất cả; f thử lại lỗi; s tiếp tục gián đoạn."
	case "harness.hint.primary.inbox":
		return "Nhấn p để thăng cấp chủ sở hữu này hoặc f để thử lại chủ sở hữu này."
	case "harness.hint.primary.review":
		return "Nhấn Enter để phê duyệt hoặc x để từ chối."
	case "harness.hint.primary.promote":
		return "Nhấn Enter để thăng cấp tác vụ đã chọn."
	case "harness.hint.primary.release":
		return "Nhấn Enter để áp dụng lô phát hành hiện tại."
	case "harness.hint.primary.rollouts":
		return "Nhấn Enter để tiến lên; g phê duyệt gate; p tạm dừng/tiếp tục; x hủy bỏ."
	case "harness.hint.primary.none":
		return "Không cần đầu vào nội tuyến cho phần này."
	case "harness.message.read_only":
		return "Bảng harness chỉ đọc khi một lượt chạy khác đang hoạt động."
	case "harness.message.monitor_refreshed":
		return "Giám sát harness đã làm mới."
	case "harness.message.rerun_failed_only":
		return "Tác vụ harness %s là %s; chỉ tác vụ lỗi mới có thể chạy lại."
	case "harness.message.review_approved":
		return "Đã phê duyệt review cho %s"
	case "harness.message.review_rejected":
		return "Đã từ chối review cho %s"
	case "harness.message.promoted":
		return "Đã thăng cấp %s"
	case "harness.message.no_release_tasks":
		return "Không có tác vụ harness sẵn sàng phát hành."
	case "harness.message.release_applied":
		return "Đã áp dụng lô phát hành %s"
	case "harness.message.no_rollouts":
		return "Không tìm thấy triển khai đã lưu."
	case "harness.message.rollout_advanced":
		return "Đã tiến triển khai %s"
	case "harness.message.owner_promoted":
		return "Đã thăng cấp %d tác vụ cho %s"
	case "harness.message.owner_retried":
		return "Đã thử lại tác vụ lỗi cho %s"
	case "harness.message.gate_approved":
		return "Đã phê duyệt gate tiếp theo cho %s"
	case "harness.message.rollout_resumed":
		return "Đã tiếp tục triển khai %s"
	case "harness.message.rollout_paused":
		return "Đã tạm dừng triển khai %s"
	case "harness.message.rollout_aborted":
		return "Đã hủy bỏ triển khai %s"
	case "harness.message.check_passed":
		return "Kiểm tra harness đã vượt qua."
	case "harness.message.check_failed":
		return "Kiểm tra harness phát hiện vấn đề."
	case "harness.message.gc_complete":
		return "Harness gc hoàn tất."
	case "harness.message.queue_goal_required":
		return "Nhập mục tiêu hàng đợi trong đầu vào bảng trước."
	case "harness.message.queued":
		return "Đã thêm tác vụ harness %s vào hàng đợi"
	case "harness.activity.status":
		return "Harness trạng thái: %s"
	case "harness.log.phase":
		return "Giai đoạn"
	case "harness.log.worker":
		return "Worker"
	case "harness.tool.read_file":
		return "Đọc tệp"
	case "harness.tool.write_file":
		return "Ghi tệp"
	case "harness.tool.browse_files":
		return "Duyệt tệp"
	case "harness.tool.search_code":
		return "Tìm mã"
	case "harness.tool.run_command":
		return "Chạy lệnh"
	case "harness.tool.fetch_web_page":
		return "Tải trang web"
	case "harness.tool.run_subagent":
		return "Chạy sub-agent"
	case "harness.tool.update_task_state":
		return "Cập nhật trạng thái tác vụ"
	case "harness.message.run_goal_required":
		return "Nhập mục tiêu chạy trong đầu vào bảng trước."
	case "harness.message.no_queued_executed":
		return "Không có tác vụ hàng đợi nào được thực thi."
	case "harness.message.queue_retried":
		return "Đã thử lại %d tác vụ hàng đợi lỗi."
	case "harness.message.queue_resumed":
		return "Đã tiếp tục %d tác vụ hàng đợi bị gián đoạn."
	case "harness.message.queue_ran":
		return "Đã chạy %d tác vụ hàng đợi."
	case "harness.preview.not_initialized":
		return "Harness chưa được khởi tạo trong dự án này.\n\nNhấn Enter hoặc i để chạy harness init trong kho hiện tại."
	case "harness.preview.check":
		return "Chạy kiểm tra harness cho dự án hiện tại.\n\nEnter: chạy kiểm tra tệp/nội dung/ngữ cảnh bắt buộc và các lệnh xác thực đã cấu hình."
	case "harness.preview.gc":
		return "Chạy thu gom rác harness.\n\nEnter: lưu trữ tác vụ cũ, bỏ tác vụ bị chặn/đang chạy cũ, dọn log cũ và xóa worktree mồ côi."
	case "harness.preview.queue_help":
		return "Nhập mục tiêu harness tại đây, sau đó nhấn Enter để thêm vào hàng đợi."
	case "harness.preview.run_help":
		return "Nhập mục tiêu harness tại đây, sau đó nhấn Enter để bắt đầu chạy."
	case "harness.preview.run_queued":
		return "Trạng thái hàng đợi:\nhàng đợi=%d đang chạy=%d bị chặn=%d lỗi=%d\n\nEnter chạy tác vụ tiếp theo.\na chạy tất cả.\nf thử lại lỗi.\ns tiếp tục gián đoạn."
	case "harness.preview.no_owner":
		return "Chưa chọn chủ sở hữu harness."
	case "harness.preview.no_context":
		return "Chưa chọn ngữ cảnh harness."
	case "harness.preview.no_task":
		return "Chưa chọn tác vụ harness."
	case "harness.preview.project_not_initialized":
		return "Harness chưa được khởi tạo trong dự án này."
	case "harness.preview.project_initialized":
		return "Harness đã được khởi tạo."
	case "harness.preview.project_help":
		return "Dùng /harness để duyệt và vận hành control plane."
	case "harness.preview.no_doctor":
		return "Không có báo cáo harness doctor."
	case "harness.preview.monitor_unavailable":
		return "Giám sát harness không khả dụng."
	case "harness.label.context_title":
		return "Ngữ cảnh"
	case "harness.label.owner_title":
		return "Chủ sở hữu"
	case "harness.label.id":
		return "id"
	case "harness.label.status":
		return "trạng thái"
	case "harness.label.goal":
		return "mục tiêu"
	case "harness.label.attempts":
		return "lần thử"
	case "harness.label.depends_on":
		return "phụ_thuộc"
	case "harness.label.context":
		return "ngữ cảnh"
	case "harness.label.workspace":
		return "không gian làm việc"
	case "harness.label.branch":
		return "nhánh"
	case "harness.label.worker":
		return "worker"
	case "harness.label.progress":
		return "tiến độ"
	case "harness.label.verification":
		return "xác minh"
	case "harness.label.changed_files":
		return "tệp_thay_đổi"
	case "harness.label.delivery_report":
		return "báo_cáo_giao_hàng"
	case "harness.label.delivery_report_human":
		return "báo cáo giao hàng"
	case "harness.label.log":
		return "log"
	case "harness.label.review":
		return "duyệt"
	case "harness.label.review_notes":
		return "ghi_chú_duyệt"
	case "harness.label.promotion":
		return "thăng_cấp"
	case "harness.label.promotion_notes":
		return "ghi_chú_thăng_cấp"
	case "harness.label.release_batch":
		return "lô_phát_hành"
	case "harness.label.release_batch_human":
		return "lô phát hành"
	case "harness.label.release_notes":
		return "ghi_chú_phát_hành"
	case "harness.label.error":
		return "lỗi"
	case "harness.label.name":
		return "tên"
	case "harness.label.description":
		return "mô tả"
	case "harness.label.owner":
		return "chủ sở hữu"
	case "harness.label.commands":
		return "lệnh"
	case "harness.label.tasks":
		return "tác vụ"
	case "harness.label.rollouts":
		return "triển khai"
	case "harness.label.gates":
		return "cổng"
	case "harness.label.latest":
		return "mới nhất"
	case "harness.label.repo":
		return "kho"
	case "harness.label.config":
		return "cấu hình"
	case "harness.label.project":
		return "dự án"
	case "harness.label.structure":
		return "cấu trúc"
	case "harness.label.contexts":
		return "ngữ cảnh"
	case "harness.label.workers":
		return "worker"
	case "harness.label.workflow":
		return "luồng công việc"
	case "harness.label.quality":
		return "chất lượng"
	case "harness.label.worktrees":
		return "worktree"
	case "harness.label.snapshot":
		return "ảnh chụp"
	case "harness.label.events":
		return "sự kiện"
	case "harness.label.target":
		return "mục tiêu"
	case "harness.label.review_ready":
		return "sẵn_sàng_duyệt"
	case "harness.label.promotion_ready":
		return "sẵn_sàng_thăng_cấp"
	case "harness.label.retryable":
		return "có_thể_thử_lại"
	case "harness.task_title":
		return "Tác vụ harness"
	case "harness.doctor_title":
		return "Harness doctor"
	case "harness.monitor_title":
		return "Giám sát harness"
	case "harness.latest_task":
		return "Tác vụ mới nhất"
	case "harness.latest_event":
		return "Sự kiện mới nhất"
	case "harness.focus":
		return "Tập trung"
	case "harness.status.ok":
		return "ok"
	case "harness.status.needs_attention":
		return "cần chú ý"
	case "harness.group.review":
		return "duyệt"
	case "harness.group.promotion":
		return "thăng cấp"
	case "harness.group.retry":
		return "thử lại"
	case "harness.review_ready_short":
		return "duyệt"
	case "harness.promote_ready_short":
		return "thăng cấp"
	case "harness.tasks_count":
		return "tác vụ"
	case "harness.input_empty":
		return "(ô nhập trống)"
	case "harness.no_waves":
		return "không có wave"
	case "harness.mixed":
		return "kết hợp"
	case "hint.autocomplete":
		return "Tab/Shift+Tab xoay vòng • Enter áp dụng • Esc đóng"
	case "hint.mention":
		return "@ đính kèm tệp/thư mục • Tab/Shift+Tab xoay vòng • Enter áp dụng"
	case "hint.mode":
		return "chế độ"
	case "mode.approval.ask":
		return "hỏi khi cần"
	case "mode.approval.none":
		return "không điểm dừng phê duyệt"
	case "mode.approval.critical":
		return "chỉ quan trọng"
	case "mode.tools.rules":
		return "theo quy tắc công cụ"
	case "mode.tools.readonly":
		return "chỉ đọc"
	case "mode.tools.safe":
		return "chỉ thao tác an toàn"
	case "mode.tools.open":
		return "gần như tất cả công cụ"
	case "mode.agent.waits":
		return "chờ bạn"
	case "mode.agent.autocontinue":
		return "tiếp tục tự động"
	case "hint.enter_send":
		return "Enter gửi"
	case "hint.ctrlv_image":
		return "Ctrl+V / Ctrl+Shift+V dán ảnh"
	case "hint.ctrlr_sidebar":
		return "Ctrl+R thanh bên"
	case "hint.help":
		return "/? trợ giúp"
	case "hint.add_context":
		return "@ thêm ngữ cảnh"
	case "hint.scroll":
		return "PgUp/PgDn cuộn"
	case "hint.shift_tab_mode":
		return "Shift+Tab chế độ"
	case "hint.ctrlc_cancel":
		return "Ctrl+C hủy"
	case "hint.ctrlc_exit":
		return "Ctrl+C xóa/thoát"
	case "hint.image_attached":
		return "đã đính kèm ảnh"
	case "hint.image_attached_count":
		return "đã đính kèm %d ảnh"
	case "hint.follow_panel":
		return "Ctrl+N theo dõi"
	case "hint.unfollow_panel":
		return "Ctrl+N bỏ theo dõi"
	case "queued.count":
		return "%d trong hàng đợi"
	case "queued.output":
		return "[đã xếp hàng %d đang chờ]\n\n"
	case "interrupt.delivered":
		return "[đã gửi đến lượt chạy hoạt động; đang sửa đổi kế hoạch]\n"
	case "status.thinking":
		return "Đang suy nghĩ..."
	case "status.writing":
		return "Đang viết..."
	case "status.cancelling":
		return "Đang hủy..."
	case "status.compacting":
		return "Đang nén ngữ cảnh..."
	case "status.compacted":
		return "[hội thoại đã được nén]"
	case "reasoning.effort.status":
		return "Mức suy nghĩ: %s"
	case "reasoning.effort.set":
		return "Mức suy nghĩ đã đặt thành %s cho phiên này"
	case "reasoning.effort.unsupported.status":
		return "Mức suy nghĩ không được hỗ trợ bởi nhà cung cấp hiện tại"
	case "reasoning.effort.unsupported":
		return "Mức suy nghĩ không được hỗ trợ bởi nhà cung cấp hiện tại"
	case "follow.loading":
		return "  Đang tải chế độ xem theo dõi..."
	case "follow.active_agent":
		return "Đang theo dõi agent %s — đầu vào tạm dừng. Nhấn Esc để quay lại."
	case "follow.active_teammate":
		return "Đang theo dõi teammate %s — đầu vào tạm dừng. Nhấn Esc để quay lại."
	case "follow.status_running":
		return "đang chạy"
	case "follow.status_done":
		return "hoàn thành"
	case "follow.more":
		return "  +%d nữa"
	case "follow.hint":
		return "  ↑↓←→ chuyển  Esc đóng"
	case "status.tools_used":
		return "đã dùng %d công cụ"
	case "tool.done":
		return "hoàn thành"
	case "tool.failed":
		return "lỗi"
	case "tool.no_output":
		return "không đầu ra"
	case "tool.output":
		return "đầu ra"
	case "tool.content":
		return "nội dung"
	case "tool.match":
		return "khớp"
	case "tool.matches":
		return "khớp"
	case "tool.entry":
		return "mục"
	case "tool.result":
		return "kết quả"
	case "approval.rejected":
		return "  Đã từ chối.\n"
	case "approval.allow":
		return "Cho phép"
	case "approval.allow_always":
		return "Luôn cho phép"
	case "approval.deny":
		return "Từ chối"
	case "approval.accept":
		return "Chấp nhận"
	case "approval.reject":
		return "Từ chối"
	case "exit.confirm":
		return "Nhấn Ctrl-C lần nữa để thoát.\n\n"
	case "cancel.confirm":
		return "Nhấn Ctrl-C hoặc Esc lần nữa để hủy agent đang chạy.\n\n"
	case "interrupted":
		return "[bị ngắt]\n\n"
	case "lang.current":
		return "Ngôn ngữ hiện tại: %s\nDùng /lang để chọn tương tác, hoặc /lang <en|zh-CN> để chuyển trực tiếp.\n%s\n\n"
	case "lang.invalid":
		return "Ngôn ngữ không hỗ trợ: %s\n%s\n\n"
	case "lang.switch":
		return "Đã chuyển ngôn ngữ sang: %s\n\n"
	case "lang.selection.current":
		return " Hiện tại: %s"
	case "lang.selection.hint":
		return " Tab/j/k di chuyển • Enter xác nhận • e/z phím tắt • Esc hủy"
	case "lang.first_use.title":
		return "Chọn ngôn ngữ ưu tiên của bạn"
	case "lang.first_use.body":
		return " Chọn ngôn ngữ bạn muốn ggcode sử dụng từ nay."
	case "lang.first_use.hint":
		return " Tab/j/k di chuyển • Enter xác nhận • e/z phím tắt"
	case "mode.current":
		return "Chế độ hiện tại: %s\nCách dùng: /mode <supervised|plan|auto|bypass|autopilot>\n  supervised  Hỏi khi công cụ không có quy tắc rõ ràng\n  plan        Khám phá chỉ đọc; từ chối ghi và lệnh\n  auto        Cho phép thao tác an toàn, từ chối nguy hiểm\n  bypass      Cho phép gần như tất cả; chỉ dừng ở hành động quan trọng\n  autopilot   bypass + tiếp tục khi mô hình hỏi lại\n\n"
	case "mode.persist_failed":
		return "Không thể lưu tùy chọn chế độ: %v"
	case "input.placeholder":
		return "Nhập tin nhắn... ($ shell, # chat)"
	case "panel.model_filter.prompt":
		return "Lọc> "
	case "panel.model_filter.placeholder":
		return "nhập để lọc mô hình"
	case "panel.model_list.none":
		return "(không có)"
	case "panel.model_list.no_matches":
		return "(không khớp)"
	case "panel.model_list.showing":
		return "đang hiển thị %d/%d mô hình"
	case "panel.model_list.hidden_above":
		return "%d ở trên"
	case "panel.model_list.hidden_more":
		return "%d nữa"
	case "panel.provider.vendors":
		return "Nhà cung cấp"
	case "panel.provider.endpoints":
		return "Endpoint"
	case "panel.provider.models":
		return "Mô hình"
	case "panel.provider.active_draft":
		return "Bản nháp hoạt động"
	case "panel.provider.protocol":
		return "Giao thức"
	case "panel.provider.protocol.unknown":
		return "(không xác định)"
	case "panel.provider.auth":
		return "Xác thực"
	case "panel.provider.env_var":
		return "Biến môi trường"
	case "panel.provider.api_key":
		return "API key"
	case "panel.provider.api_key.missing":
		return "thiếu"
	case "panel.provider.api_key.configured":
		return "đã cấu hình"
	case "panel.provider.auth.connected":
		return "đã kết nối"
	case "panel.provider.auth.not_connected":
		return "chưa kết nối"
	case "panel.provider.base_url":
		return "Base URL"
	case "panel.provider.base_url.not_set":
		return "(chưa đặt)"
	case "panel.provider.enterprise_url":
		return "Enterprise URL"
	case "panel.provider.tags":
		return "Thẻ"
	case "panel.provider.model.set_with_m":
		return "(đặt bằng m)"
	case "panel.provider.edit":
		return "Sửa"
	case "panel.provider.edit.vendor_api_key":
		return "api key nhà cung cấp"
	case "panel.provider.edit.endpoint_api_key":
		return "api key endpoint"
	case "panel.provider.edit.endpoint_base_url":
		return "base url endpoint"
	case "panel.provider.edit.custom_model":
		return "mô hình tùy chỉnh"
	case "panel.provider.edit.new_endpoint_name":
		return "tên endpoint mới"
	case "panel.provider.hint.edit":
		return "Enter lưu • Esc hủy"
	case "panel.provider.hint.main":
		return "Tab/Shift+Tab đổi tập trung • j/k di chuyển • / tập trung lọc • Enter hoặc s áp dụng • a key nhà cung cấp • u key endpoint • b base URL • m mô hình tùy chỉnh • e thêm endpoint • Esc đóng"
	case "panel.provider.hint.copilot":
		return "GitHub Copilot: l đăng nhập • x đăng xuất • b sửa enterprise domain"
	case "panel.provider.saved":
		return "Đã lưu."
	case "panel.provider.saved_activated":
		return "Đã lưu và kích hoạt."
	case "panel.provider.login.starting":
		return "Đang bắt đầu đăng nhập GitHub Copilot..."
	case "panel.provider.login.instructions":
		return "Mở %s và nhập mã %s. Đang chờ ủy quyền..."
	case "panel.provider.login.copied":
		return "Mã thiết bị đã sao chép vào clipboard."
	case "panel.provider.login.copy_failed":
		return "Sao chép mã thiết bị thất bại: %s"
	case "panel.provider.login.browser_opened":
		return "Đã mở trang xác minh trong trình duyệt."
	case "panel.provider.login.browser_failed":
		return "Mở trang xác minh thất bại: %s"
	case "panel.provider.login.success":
		return "Đã kết nối GitHub Copilot."
	case "panel.provider.login.failed":
		return "Đăng nhập GitHub Copilot thất bại: %s"
	case "panel.provider.logout.success":
		return "Đã ngắt kết nối GitHub Copilot."
	case "panel.provider.refreshing_vendor":
		return "Đang làm mới mô hình cho %s..."
	case "panel.provider.refresh.save_failed":
		return "Đã làm mới mô hình, nhưng lưu cấu hình thất bại: %s"
	case "panel.provider.refresh.partial":
		return "Đã làm mới %d endpoint, khám phá %d mô hình. Một số endpoint lỗi: %v"
	case "panel.provider.refresh.success":
		return "Đã làm mới %d endpoint, khám phá %d mô hình."
	case "panel.provider.refresh.failed":
		return "Làm mới mô hình thất bại: %s"
	case "panel.provider.refresh.none":
		return "Không có endpoint nào có thể làm mới cho nhà cung cấp này."
	case "panel.model.models":
		return "Mô hình"
	case "panel.model.vendor":
		return "Nhà cung cấp"
	case "panel.model.endpoint":
		return "Endpoint"
	case "panel.model.protocol":
		return "Giao thức"
	case "panel.model.source":
		return "Nguồn"
	case "panel.model.source.builtin":
		return "tích hợp sẵn"
	case "panel.model.source.remote":
		return "từ xa"
	case "panel.model.refreshing":
		return "Đang làm mới mô hình mới nhất..."
	case "panel.model.hint.main":
		return "j/k di chuyển • Enter hoặc s áp dụng • w cửa sổ ngữ cảnh • o max token • r làm mới • / lọc • Esc đóng"
	case "panel.model.hint.edit":
		return "Enter lưu • Esc hủy (0 hoặc trống = tự động, hậu tố K/M/G OK ví dụ 256k)"
	case "panel.model.context_window":
		return "Cửa sổ ngữ cảnh"
	case "panel.model.max_tokens":
		return "Token đầu ra tối đa"
	case "panel.model.edit":
		return "Sửa"
	case "panel.model.saved_runtime_inactive":
		return "Đã lưu cấu hình, nhưng runtime hiện tại vẫn không hoạt động: %s"
	case "panel.model.context_applied":
		return "Đã áp dụng context_window=%d, max_tokens=%d (đã lưu)"
	case "panel.model.context_cleared":
		return "Đặt lại về tự động phát hiện (đã lưu)"
	case "panel.model.switched":
		return "Đã chuyển mô hình sang %s."
	case "panel.model.refresh.save_failed":
		return "Đã làm mới mô hình, nhưng lưu cấu hình thất bại: %s"
	case "panel.model.refresh.builtin_reason":
		return "Đang dùng mô hình tích hợp: %s"
	case "panel.model.refresh.remote_loaded":
		return "Đã tải %d mô hình từ xa."
	case "panel.model.refresh.builtin_loaded":
		return "Đã tải mô hình tích hợp."
	case "command.unknown":
		return "Lệnh không xác định: %s\n"
	case "command.retry_empty":
		return "Không có nội dung gửi trước để thử lại."
	case "command.retry_busy":
		return "Agent đang bận. Đợi lượt chạy hiện tại hoàn tất trước khi thử lại."
	case "command.edit_empty":
		return "Không có nội dung gửi trước để sửa."
	case "command.edit_busy":
		return "Agent đang bận. Đợi lượt chạy hiện tại hoàn tất trước khi sửa."
	case "command.edit_ready":
		return "Đã tải nội dung gửi cuối — sửa và nhấn Enter để gửi."
	case "command.help_hint":
		return "Gõ /help để xem lệnh có sẵn\n\n"
	case "command.usage.allow":
		return "Cách dùng: /allow <tên-công-cụ>\n\n"
	case "command.usage.resume":
		return "Cách dùng: /resume <id-phiên>\n\n"
	case "command.usage.export":
		return "Cách dùng: /export <id-phiên>\n\n"
	case "init.resolve_failed":
		return "Không thể giải quyết mục tiêu init: %v\n\n"
	case "init.generate_failed":
		return "Không thể tạo nội dung GGCODE.md: %v\n\n"
	case "init.collecting":
		return "Đang thu thập kiến thức dự án..."
	case "init.prompt.title":
		return "Khởi tạo dự án"
	case "init.prompt.body":
		return "Không tìm thấy GGCODE.md trong dự án này. Tạo một tệp để giúp agent hiểu quy ước codebase của bạn?"
	case "init.prompt.yes":
		return "Tạo"
	case "init.prompt.no":
		return "Bỏ qua"
	case "init.prompt.hint":
		return " y = tạo GGCODE.md • n/Esc = bỏ qua"
	case "command.model_switched":
		return "Đã chuyển mô hình sang: %s (nhà cung cấp: %s)\n\n"
	case "command.model_failed":
		return "Không thể chuyển mô hình: %v\n\n"
	case "command.model_current":
		return "Mô hình hiện tại: %s (nhà cung cấp: %s)\nMô hình có sẵn: %s\nDùng /model để mở bảng mô hình hoặc /model <tên-mô-hình> để chuyển trực tiếp.\n\n"
	case "command.provider_unknown":
		return "Nhà cung cấp không xác định: %s (có sẵn: %v)\n\n"
	case "command.provider_switched":
		return "Đã chuyển nhà cung cấp sang: %s (mô hình: %s)\n\n"
	case "command.provider_failed":
		return "Không thể cập nhật lựa chọn nhà cung cấp: %v\n\n"
	case "command.provider_current":
		return "Nhà cung cấp hiện tại: %s (endpoint: %s, mô hình: %s)\nNhà cung cấp có sẵn: %s\nEndpoint có sẵn: %s\nCách dùng: /provider [nhà cung cấp] [endpoint]\n\n"
	case "command.allow_set":
		return "✓ %s hiện được luôn cho phép\n\n"
	case "command.custom":
		return "Lệnh tùy chỉnh /%s:\n"
	case "command.mention_error":
		return "Lỗi mở rộng mention: %v"
	case "session.list_failed":
		return "Lỗi liệt kê phiên: %v\n\n"
	case "session.untitled":
		return "không tiêu đề"
	case "session.store_missing":
		return "Kho phiên chưa được cấu hình.\n\n"
	case "session.none":
		return "Không tìm thấy phiên nào.\n\n"
	case "session.list.title":
		return "Phiên:\n\n"
	case "session.list.item":
		return "  %d. %s  %s  (%s)\n"
	case "session.list.hint":
		return "\nDùng /resume <id> để tiếp tục phiên\n\n"
	case "session.new":
		return "Phiên mới: %s\n\n"
	case "session.resume":
		return "Đã tiếp tục phiên: %s — %s (%d tin nhắn)\n\n"
	case "session.resume_failed":
		return "Không thể tiếp tục phiên %s: %v\n\n"
	case "session.resume_fallback":
		return "Đang tạo phiên mới thay thế.\n\n"
	case "session.export_failed":
		return "Lỗi xuất phiên: %v\n\n"
	case "session.write_failed":
		return "Lỗi ghi tệp: %v\n\n"
	case "session.exported":
		return "Đã xuất phiên %s sang %s\n\n"
	case "checkpoint.disabled":
		return "Checkpoint chưa được bật.\n\n"
	case "checkpoint.undo_failed":
		return "Hoàn tác thất bại: %v\n\n"
	case "checkpoint.undid":
		return "Đã hoàn tác %s trên %s (checkpoint %s)\n"
	case "checkpoint.none":
		return "Không có checkpoint.\n\n"
	case "files.disabled":
		return "Checkpoint chưa được bật.\n\n"
	case "files.none":
		return "Không có tệp nào được agent sửa trong phiên này.\n\n"
	case "files.title":
		return "Tệp đã được agent sửa (%d tệp, %d lần sửa):\n\n"
	case "files.item":
		return "  %s  %d lần sửa  cuối: %s%s\n"
	case "files.hint":
		return "\nDùng /undo để hoàn tác lần sửa gần nhất, /checkpoints để xem chi tiết.\n\n"
	case "checkpoint.list.title":
		return "Checkpoint (%d):\n\n"
	case "checkpoint.list.item":
		return "  %d. %s  %s  %s  %s\n"
	case "checkpoint.list.hint":
		return "\nDùng /undo để hoàn tác lần sửa gần nhất.\n\n"
	case "memory.auto_unavailable":
		return "Auto memory chưa được khởi tạo.\n\n"
	case "memory.list_failed":
		return "Lỗi liệt kê bộ nhớ: %v\n\n"
	case "memory.none":
		return "Chưa có auto memory nào được lưu.\n\n"
	case "memory.auto_title":
		return "Bộ nhớ tự động:\n"
	case "memory.clear_failed":
		return "Lỗi xóa bộ nhớ: %v\n\n"
	case "memory.cleared":
		return "Đã xóa tất cả auto memory.\n\n"
	case "memory.title":
		return "Bộ nhớ:\n"
	case "memory.project":
		return "Bộ nhớ dự án:\n"
	case "memory.project_none":
		return "  Không có tệp bộ nhớ dự án nào được tải.\n"
	case "memory.auto":
		return "Bộ nhớ tự động:\n"
	case "memory.auto_none":
		return "  Không có auto memory nào được tải.\n"
	case "memory.usage":
		return "\nCách dùng: /memory [list|clear]\n\n"
	case "compact.unavailable":
		return "Context manager không khả dụng.\n\n"
	case "compact.failed":
		return "Nén thất bại: %v\n\n"
	case "compact.done":
		return "Lịch sử hội thoại đã được nén.\n\n"
	case "compact.done_with_stats":
		return "Lịch sử hội thoại đã được nén (%d → %d token).\n\n"
	case "todo.cleared":
		return "Danh sách todo đã được xóa.\n\n"
	case "todo.clear_failed":
		return "Lỗi xóa todo: %v\n\n"
	case "todo.none":
		return "Không tìm thấy danh sách todo. Dùng công cụ todo_write để tạo.\n\n"
	case "todo.read_failed":
		return "Lỗi đọc todo: %v\n\n"
	case "todo.parse_failed":
		return "Lỗi phân tích todo: %v\n\n"
	case "todo.title":
		return "Danh sách todo:\n%s\n\n"
	case "bug.title":
		return "=== Chẩn đoán báo cáo lỗi ===\n\n"
	case "bug.version":
		return "Phiên bản: %s\n"
	case "bug.os":
		return "HĐH: %s %s\n"
	case "bug.go":
		return "Go: %s\n"
	case "bug.provider":
		return "Nhà cung cấp: %s\n"
	case "bug.model":
		return "Mô hình: %s\n"
	case "bug.session":
		return "Phiên: %s (%d tin nhắn)\n"
	case "bug.mcp":
		return "MCP server: %d\n"
	case "bug.last_error":
		return "Lỗi cuối: %s\n"
	case "bug.hint":
		return "\nVui lòng bao gồm thông tin này khi báo cáo lỗi.\n\n"
	case "config.usage":
		return "Cách dùng: /config set <khóa> <giá trị>\n\nKhóa: model, vendor, endpoint, language, apikey [--vendor]\n\nEndpoint: /config add-endpoint <tên> <base_url> [--protocol openai] [--apikey sk-xxx]\n          /config remove-endpoint <tên>\n\n"
	case "config.not_loaded":
		return "Cấu hình chưa được tải.\n\n"
	case "config.model_set":
		return "Cấu hình: model = %s\n\n"
	case "config.provider_set":
		return "Cấu hình: provider = %s\n\n"
	case "config.language_set":
		return "Cấu hình: language = %s\n\n"
	case "config.unknown_key":
		return "Khóa cấu hình không xác định: %s\nHỗ trợ: model, provider, language\n\n"
	case "config.title":
		return "Cấu hình hiện tại:\n"
	case "status.title":
		return "Trạng thái:\n"
	case "panel.update":
		return "Cập nhật"
	case "label.version":
		return "Phiên bản"
	case "label.latest":
		return "Mới nhất"
	case "update.sidebar_hint":
		return "Có phiên bản mới. Chạy /update."
	case "update.up_to_date":
		return "Bạn đang ở phiên bản mới nhất."
	case "update.available":
		return "có bản cập nhật: %s"
	case "update.current":
		return "hiện tại: %s (mới nhất: %s)"
	case "update.unknown":
		return "chưa kiểm tra"
	case "update.check_failed":
		return "kiểm tra thất bại: %s"
	case "update.unavailable":
		return "Cập nhật không khả dụng trong phiên này.\n\n"
	case "update.preparing":
		return "Đang chuẩn bị cập nhật"
	case "update.failed":
		return "Cập nhật thất bại: %v\n\n"
	case "update.restart_failed":
		return "Cập nhật đã chuẩn bị, nhưng khởi động lại thất bại: %v\n\n"
	case "update.pm_hint.brew":
		return "Đã cài đặt cập nhật. Lưu ý: ggcode được cài qua Homebrew.\nChạy `brew upgrade ggcode` để đồng bộ Homebrew.\n\n"
	case "update.pm_hint.scoop":
		return "Đã cài đặt cập nhật. Lưu ý: ggcode được cài qua Scoop.\nChạy `scoop update ggcode` để đồng bộ Scoop.\n\n"
	case "update.pm_hint.winget":
		return "Đã cài đặt cập nhật. Lưu ý: ggcode được cài qua winget.\nChạy `winget upgrade ggcode` để đồng bộ winget.\n\n"
	case "update.pm_hint.snap":
		return "Đã cài đặt cập nhật. Lưu ý: ggcode được cài qua Snap.\nChạy `sudo snap refresh ggcode` để đồng bộ Snap.\n\n"
	case "update.other_installs":
		return "Phát hiện cài đặt ggcode khác trên hệ thống:\n%s\nNếu ggcode khác xuất hiện đầu trong PATH, hãy cân nhắc cập nhật nó hoặc điều chỉnh thứ tự PATH.\n\n"
	case "update.dual_scope":
		return "Cảnh báo: Tìm thấy cả cài đặt ggcode người dùng và toàn hệ thống:\n  Người dùng: %s\n  Hệ thống: %s\nĐiều này có thể gây xung đột PATH. Cân nhắc gỡ một bản trong Settings > Apps.\n\n"
	case "plugins.unavailable":
		return "Trình quản lý plugin không khả dụng.\n\n"
	case "plugins.none":
		return "Không có plugin nào được tải.\n\n"
	case "plugins.title":
		return "Plugin:\n"
	case "mcp.none":
		return "Chưa cấu hình MCP server.\n\n"
	case "mcp.title":
		return "MCP Server:\n"
	case "mcp.active_tools":
		return "Công cụ hoạt động"
	case "mcp.more":
		return "… %d nữa • /mcp"
	case "image.usage":
		return "Cách dùng: /image <đường/dẫn/tệp.png> hoặc /image paste\n"
	case "image.formats":
		return "Định dạng hỗ trợ: PNG, JPEG, GIF, WebP (tối đa 20MB)\n\n"
	case "image.attached":
		return "Đã đính kèm ảnh: %s\n"
	case "image.attached_hint":
		return "Gửi tin nhắn để bao gồm ảnh, hoặc /image để đính kèm ảnh khác.\n\n"
	case "image.clipboard_failed":
		return "Không thể dán ảnh từ clipboard: %v"
	case "image.clipboard_no_image_windows":
		return "Không tìm thấy ảnh trong clipboard. Trên Windows, Ctrl+V thường bị terminal chặn. Thử Ctrl+Shift+V hoặc /image paste."
	case "agents.unavailable":
		return "Trình quản lý sub-agent chưa được cấu hình.\n\n"
	case "agents.none":
		return "Chưa tạo sub-agent nào.\nCách dùng: LLM có thể dùng công cụ spawn_agent để tạo sub-agent.\n\n"
	case "agents.title":
		return "%d sub-agent:\n"
	case "agents.item":
		return "  %s [%s]%s - %s\n"
	case "agents.hint":
		return "\nDùng /agent <id> để xem chi tiết, /agent cancel <id> để hủy.\n\n"
	case "agent.usage":
		return "Cách dùng: /agent <id> hoặc /agent cancel <id>\n\n"
	case "agent.cancelled":
		return "Đã hủy sub-agent %s\n\n"
	case "agent.cancel_failed":
		return "Không thể hủy %s (không tìm thấy hoặc không đang chạy)\n\n"
	case "agent.not_found":
		return "Không tìm thấy sub-agent %s\n\n"
	case "agent.title":
		return "Agent: %s\nTrạng thái: %s\nTác vụ: %s\n"
	case "agent.result":
		return "Kết quả: %s\n"
	case "agent.error":
		return "Lỗi: %v\n"
	case "slash.help":
		return "Hiện thông báo trợ giúp"
	case "slash.sessions":
		return "Liệt kê phiên đã lưu"
	case "slash.resume":
		return "Tiếp tục phiên trước đó"
	case "slash.export":
		return "Xuất phiên sang markdown"
	case "slash.model":
		return "Chuyển mô hình"
	case "slash.provider":
		return "Mở trình quản lý nhà cung cấp"
	case "slash.clear":
		return "Xóa hội thoại"
	case "slash.mcp":
		return "Hiện MCP server"
	case "slash.memory":
		return "Quản lý bộ nhớ"
	case "slash.undo":
		return "Hoàn tác lần sửa tệp cuối"
	case "slash.files":
		return "Hiện tệp đã sửa bởi agent"
	case "slash.checkpoints":
		return "Liệt kê checkpoint"
	case "slash.allow":
		return "Luôn cho phép một công cụ"
	case "slash.plugins":
		return "Liệt kê plugin đã tải"
	case "slash.image":
		return "Đính kèm ảnh"
	case "slash.init":
		return "Tạo GGCODE.md cho dự án"
	case "slash.harness":
		return "Chạy lệnh harness workflow"
	case "slash.lang":
		return "Chuyển ngôn ngữ giao diện"
	case "slash.skills":
		return "Duyệt kỹ năng có sẵn"
	case "slash.exit":
		return "Thoát ggcode"
	case "slash.compact":
		return "Nén lịch sử hội thoại"
	case "slash.todo":
		return "Xem/quản lý danh sách todo"
	case "slash.bug":
		return "Báo cáo lỗi"
	case "slash.config":
		return "Xem/sửa cấu hình"
	case "slash.qq":
		return "Quản lý liên kết kênh QQ"
	case "slash.telegram":
		return "Quản lý liên kết kênh Telegram"
	case "slash.pc":
		return "Quản lý liên kết kênh PC"
	case "slash.discord":
		return "Quản lý liên kết kênh Discord"
	case "slash.feishu":
		return "Quản lý liên kết kênh Feishu"
	case "slash.slack":
		return "Quản lý liên kết kênh Slack"
	case "slash.dingtalk":
		return "Quản lý liên kết kênh DingTalk"
	case "slash.wechat":
		return "Quản lý liên kết kênh WeChat"
	case "slash.wecom":
		return "Quản lý liên kết kênh WeCom (WeChat Doanh nghiệp)"
	case "slash.mattermost":
		return "Quản lý liên kết kênh Mattermost"
	case "slash.matrix":
		return "Quản lý liên kết kênh Matrix"
	case "slash.signal":
		return "Quản lý liên kết kênh Signal"
	case "slash.irc":
		return "Quản lý liên kết kênh IRC"
	case "slash.nostr":
		return "Quản lý liên kết kênh Nostr"
	case "slash.twitch":
		return "Quản lý liên kết kênh Twitch"
	case "slash.whatsapp":
		return "Quản lý liên kết kênh WhatsApp"
	case "slash.impersonate":
		return "Giả lập CLI tool cho hiển thị shell prompt"
	case "slash.knight":
		return "Quản lý agent nền tự động"
	case "slash.stream":
		return "Cấu hình chế độ đầu ra streaming"
	case "slash.diff":
		return "Hiện git diff trong chat (hỗ trợ --cached, <tệp>, --stat)"
	case "slash.hooks":
		return "Hiện hook đã cấu hình (tất cả sự kiện, loại, mẫu khớp)"
	case "slash.cost":
		return "Hiện sử dụng token và chi phí ước tính"
	case "slash.review":
		return "AI review thay đổi hiện tại (lỗi, bảo mật, race)"
	case "slash.copy":
		return "Sao chép phản hồi assistant cuối vào clipboard"
	case "slash.context":
		return "Hiện phân tích sử dụng cửa sổ ngữ cảnh (token, tin nhắn, dung lượng)"
	case "slash.im":
		return "Mở bảng kênh IM thống nhất"
	case "panel.qq.directory":
		return "Thư mục"
	case "panel.qq.runtime":
		return "Runtime"
	case "panel.qq.bots":
		return "QQ Bot"
	case "panel.qq.created":
		return "Đã tạo: %d"
	case "panel.qq.bound":
		return "Đã liên kết: %d"
	case "panel.qq.available":
		return "Khả dụng: %d"
	case "panel.qq.current_binding":
		return "Liên kết hiện tại"
	case "panel.qq.none":
		return "(không có)"
	case "panel.qq.default":
		return "(mặc định)"
	case "panel.qq.adapter":
		return "Adapter: %s"
	case "panel.qq.target":
		return "Mục tiêu: %s"
	case "panel.qq.channel":
		return "Kênh: %s"
	case "panel.qq.bot_list":
		return "Danh sách QQ Bot"
	case "panel.qq.no_bots":
		return "Chưa cấu hình QQ bot nào."
	case "panel.qq.entry.available":
		return "Khả dụng"
	case "panel.qq.entry.bound":
		return "Đã liên kết"
	case "panel.qq.entry.active":
		return "Hoạt động"
	case "panel.qq.entry.bound_other":
		return "Liên kết: %s"
	case "panel.qq.entry.muted":
		return "Đã tắt tiếng"
	case "panel.qq.details":
		return "Chi tiết"
	case "panel.qq.status":
		return "Trạng thái: %s"
	case "panel.qq.transport":
		return "Transport: %s"
	case "panel.qq.bound_directory":
		return "Thư mục đã liên kết: %s"
	case "panel.qq.current_directory_target":
		return "Mục tiêu thư mục hiện tại: %s"
	case "panel.qq.current_directory_channel":
		return "Kênh thư mục hiện tại: %s"
	case "panel.qq.waiting_for_pairing":
		return "(đang chờ ghép nối)"
	case "panel.qq.last_error":
		return "Lỗi cuối: %s"
	case "panel.qq.occupied_by":
		return "Đang chiếm bởi: %s"
	case "panel.qq.create":
		return "Tạo"
	case "panel.qq.bot_input":
		return "QQ Bot: %s"
	case "panel.qq.create_format":
		return "Định dạng: <bot-id> <appid> <appsecret>"
	case "panel.qq.create_example":
		return "Ví dụ: qq-main 123456 secret-value"
	case "panel.qq.create_hint":
		return "Enter tạo bot • Esc hủy"
	case "panel.qq.actions_hint":
		return "j/k di chuyển • Enter hoặc b liên kết bot • c liên kết kênh • x hủy liên kết kênh • u hủy liên kết bot • i tạo bot • Esc đóng"
	case "panel.qq.bind_channel":
		return "Liên kết kênh"
	case "panel.qq.scan_hint":
		return "Quét mã QR, thêm bot, sau đó gửi tin nhắn để bắt đầu ghép nối."
	case "panel.qq.qr_code":
		return "Mã QR:"
	case "panel.qq.share_link":
		return "Liên kết chia sẻ:"
	case "panel.qq.message.no_bot":
		return "Không có QQ bot khả dụng."
	case "panel.qq.message.bound_success":
		return "QQ bot đã liên kết với workspace hiện tại. Dùng c để tạo mã QR liên kết kênh."
	case "panel.qq.message.share_generated":
		return "Đã tạo liên kết chia sẻ QQ. Quét mã QR, thêm bot, sau đó gửi tin nhắn để bắt đầu ghép nối."
	case "panel.qq.message.unbound":
		return "Đã hủy liên kết kênh QQ."
	case "panel.qq.message.cleared":
		return "Ủy quyền kênh QQ đã được xóa cho workspace hiện tại."
	case "panel.qq.message.added_bot":
		return "Đã thêm QQ bot %s."
	case "panel.qq.error.config_unavailable":
		return "cấu hình không khả dụng"
	case "panel.qq.error.config_format":
		return "Cấu hình QQ bot phải là: <bot-id> <appid> <appsecret>"
	case "panel.qq.error.adapter_required":
		return "Tên QQ adapter là bắt buộc"
	case "panel.qq.error.not_configured":
		return "QQ bot %q chưa được cấu hình"
	case "panel.qq.error.disabled":
		return "QQ bot %q đã bị tắt"
	case "panel.qq.error.not_qq_adapter":
		return "adapter %q không phải là QQ bot"
	case "panel.qq.error.not_online":
		return "QQ bot %q không trực tuyến"
	case "panel.qq.error.not_online_detail":
		return "QQ bot %q không trực tuyến: %s"
	case "panel.qq.runtime.available":
		return "khả dụng"
	case "panel.qq.runtime.disabled":
		return "đã tắt (đặt im.enabled: true và khởi động lại ggcode)"
	case "panel.qq.runtime.not_started":
		return "chưa bắt đầu (khởi động lại ggcode để khởi tạo IM runtime)"
	case "panel.qq.status.not_started":
		return "chưa bắt đầu"
	case "panel.qq.status.unknown":
		return "không xác định"
	case "slash.status":
		return "Hiện trạng thái hiện tại"
	case "slash.update":
		return "Cập nhật ggcode"
	case "slash.cron":
		return "Quản lý cron job theo lịch (list, pause, resume, create)"
	case "slash.branch":
		return "Phân nhánh phiên hiện tại sang phiên mới (fork hội thoại)"
	case "slash.chat":
		return "Mở bảng LAN Chat"
	case "slash.edit":
		return "Sửa và gửi lại tin nhắn cuối"
	case "slash.inspector":
		return "Bật/tắt bảng inspector"
	case "slash.mode":
		return "Hiện hoặc chuyển chế độ quyền"
	case "slash.nick":
		return "Đặt biệt danh LAN Chat"
	case "slash.reflect":
		return "Chạy tự phản chiếu trên phiên hiện tại"
	case "slash.regenerate":
		return "Tạo lại phản hồi AI cuối (hủy và chạy lại)"
	case "slash.restart":
		return "Khởi động lại tiến trình ggcode"
	case "slash.retry":
		return "Thử lại lượt chạy agent lỗi cuối"
	case "slash.rules":
		return "Hiện ratchet rule đã học"
	case "slash.share":
		return "Chia sẻ phiên qua tunnel (mobile relay)"
	case "slash.stats":
		return "Hiện thống kê phiên (token, lượt lặp, công cụ)"
	case "slash.tmux":
		return "Mở menu quản lý tmux pane"
	case "slash.tunnel":
		return "Bật/tắt kết nối tunnel cho mobile relay"
	case "slash.unshare":
		return "Ngừng chia sẻ phiên qua tunnel"
	case "regenerate.busy":
		return "Không thể tạo lại khi agent đang chạy. Nhấn Ctrl+C để hủy trước."
	case "regenerate.no_agent":
		return "Agent chưa được khởi tạo."
	case "regenerate.no_context":
		return "Context manager không khả dụng."
	case "regenerate.no_response":
		return "Không có phản hồi assistant để tạo lại."
	case "branch.busy":
		return "Không thể phân nhánh khi agent đang chạy. Nhấn Ctrl+C để hủy trước."
	case "branch.no_session":
		return "Không có phiên hoạt động để phân nhánh."
	case "branch.empty":
		return "Phiên không có tin nhắn để phân nhánh."
	case "branch.save_failed":
		return "Không thể tạo phiên phân nhánh: %v"
	case "branch.success":
		return "Đã phân nhánh sang phiên mới %s (từ: %s). Phiên gốc được giữ nguyên."
	case "help.text":
		return `Lệnh có sẵn:

Phiên & Lịch sử:
  /help, /?          Hiện thông báo trợ giúp này
  /sessions          Liệt kê tất cả phiên đã lưu
  /resume <id>       Tiếp tục phiên trước đó
  /export <id>       Xuất phiên sang tệp markdown
  /clear             Xóa lịch sử hội thoại
  /compact           Nén lịch sử hội thoại (thủ công)
  /undo              Hoàn tác lần sửa tệp cuối (rollback checkpoint)
  /checkpoints       Liệt kê tất cả checkpoint sửa tệp
  /regenerate        Hủy phản hồi cuối và tạo lại (bí danh: /regen)
  /branch            Fork hội thoại hiện tại sang phiên mới (bí danh: /fork)

Mô hình & Nhà cung cấp:
  /model [tên]       Mở bảng mô hình hoặc chuyển trực tiếp
  /provider [vendor] Mở trình quản lý nhà cung cấp
  /mode <chế độ>     Đặt chế độ agent (supervised|plan|auto|bypass|autopilot)

Phát triển:
  /diff [tùy chọn]   Hiện git diff trong chat (--cached, --stat, <tệp>)
  /review [tùy chọn] AI review thay đổi hiện tại (--cached, --staged)
  /copy              Sao chép phản hồi assistant cuối vào clipboard
  /cost              Hiện sử dụng token và chi phí ước tính
  /context           Hiện phân tích cửa sổ ngữ cảnh
  /hooks             Hiện hook đã cấu hình
  /init              Tạo GGCODE.md từ dự án hiện tại
  /harness ...       Chạy lệnh control-plane harness
  /todo              Xem danh sách todo
  /todo clear        Xóa danh sách todo

Tích hợp:
  /im                Mở bảng kênh IM thống nhất
  /mcp               Hiện MCP server và công cụ đã kết nối
  /plugins           Liệt kê plugin đã tải và công cụ của chúng
  /skills            Duyệt kỹ năng có sẵn
  /memory            Hiện tệp bộ nhớ đã tải
  /agents            Liệt kê sub-agent
  /cron <sub>        Quản lý job theo lịch (list|get|pause|resume|pauseall|resumeall)

Cấu hình:
  /config            Hiện cấu hình hiện tại
  /config set <k> <v> Đặt giá trị cấu hình
  /status            Hiện trạng thái hiện tại
  /update            Cập nhật ggcode lên bản phát hành mới nhất
  /restart           Khởi động lại ggcode (lấy binary mới nhất)
  /bug               Báo cáo lỗi kèm chẩn đoán
  /exit, /quit       Thoát ggcode

Phím tắt:
  Tab                Xoay vòng autocomplete hoặc lựa chọn phê duyệt
  Shift+Tab          Xoay vòng ngược autocomplete, hoặc bật/tắt chế độ quyền
  Ctrl+R             Bật/tắt thanh bên
  Ctrl+N/P           Phiên mới/trước đó
  Ctrl+T             Mở tunnel (chia sẻ di động)
  Enter              Gửi tin nhắn / áp dụng lựa chọn hiện tại
  Esc                Hủy autocomplete / thoát chế độ shell chờ
  Lên/Xuống          Duyệt lịch sử lệnh (hoặc autocomplete)
  PgUp/PgDn          Cuộn đầu ra hội thoại
  Ctrl+C             Hủy hoạt động hiện tại, nếu không thì xóa đầu vào rồi nhấn lại để thoát
  Ctrl+D             Thoát ngay lập tức
  Ctrl+A / Ctrl+E    Di chuyển con trỏ về đầu / cuối dòng
  Ctrl+K             Xóa từ con trỏ đến cuối dòng
  Ctrl+U             Xóa từ đầu dòng đến con trỏ
  Ctrl+W             Xóa từ trước con trỏ
  Ctrl+Backspace     Xóa ảnh đính kèm cuối
  Shift+Enter        Chèn dòng mới (Ctrl+J hoặc Alt+Enter trong tmux)
  $ / !              Vào chế độ shell
  #                  Vào chế độ gửi nhanh LAN Chat

Chuột:
  Option+kéo / Shift+kéo  Chọn văn bản để sao chép (vượt qua bắt chuột của app)
  Con lăn chuột          Cuộn đầu ra hội thoại`
	case "command.harness_usage":
		return "Cách dùng: /harness <init|check|queue|tasks|run|rerun|run-queued|monitor|contexts|inbox|review|promote|release|gc|doctor> ... (release hỗ trợ rollouts|advance|pause|resume|abort|approve|reject)"
	case "command.harness_queue_usage":
		return "Cách dùng: /harness queue <mục tiêu>"
	case "command.harness_run_usage":
		return "Cách dùng: /harness run <mục tiêu>"
	case "command.harness_rerun_usage":
		return "Cách dùng: /harness rerun <task-id>"
	case "command.skill_agent_only":
		return "Kỹ năng %s chỉ có thể được gọi bởi agent."
	case "command.harness_owner_promoted":
		return "Đã thăng cấp %d tác vụ harness cho chủ sở hữu %s."
	case "command.harness_review_approved":
		return "Đã phê duyệt tác vụ harness %s."
	case "command.harness_review_rejected":
		return "Đã từ chối tác vụ harness %s."
	case "command.harness_promoted_many":
		return "Đã thăng cấp %d tác vụ harness."
	case "command.harness_promoted_one":
		return "Đã thăng cấp tác vụ harness %s."
	case "command.harness_task_queued_detail":
		return "Đã thêm tác vụ harness %s vào hàng đợi.\n- mục tiêu: %s"
	case "command.harness_tasks_empty":
		return "Không có tác vụ harness nào được ghi lại."
	case "command.harness_run_start":
		return "Đang bắt đầu lượt chạy harness có theo dõi...\nDùng /harness monitor hoặc chế độ xem Tasks/Monitor cho trạng thái trực tiếp."
	case "command.harness_rerun_start":
		return "Đang bắt đầu chạy lại harness có theo dõi...\nDùng /harness monitor hoặc chế độ xem Tasks/Monitor cho trạng thái trực tiếp."
	case "command.harness_rerun_invalid_status":
		return "Tác vụ harness %s là %s; chỉ tác vụ lỗi mới có thể chạy lại."
	case "command.harness_status_starting_run":
		return "Đang bắt đầu lượt chạy harness..."
	case "command.harness_status_starting_rerun":
		return "Đang bắt đầu chạy lại harness..."
	case "command.harness_spinner_running":
		return "Đang chạy harness"
	case "command.harness_cancelled":
		return "Đã hủy lượt chạy harness."
	case "tunnel.stopped":
		return "Tunnel đã dừng."
	case "tunnel.not_active":
		return "Không có phiên chia sẻ nào hoạt động."
	case "tunnel.mobile_connected":
		return "Đã kết nối mobile client."
	case "config.save_scope_global":
		return "Đích lưu → Toàn cục"
	case "config.save_scope_instance":
		return "Đích lưu → Instance"
	case "config.save_scope_instance_new":
		return "Đích lưu → Instance (cấu hình mới sẽ được tạo)"
	case "config.instance_unavailable":
		return "Cấu hình instance không khả dụng cho workspace này"
	case "config.scope_instance":
		return "Instance"
	case "config.scope_global":
		return "Toàn cục"
	case "config.save_target_new_hint":
		return " (cấu hình mới sẽ được tạo khi lưu)"
	case "config.save_target_line":
		return " Đích lưu: %s%s  [Ctrl+T chuyển đổi]"
	case "shell.empty":
		return "Lệnh shell trống."
	case "lanchat.unavailable":
		return "LAN Chat không khả dụng."
	case "reflect.no_agent":
		return "Agent chưa được khởi tạo."
	case "reflect.no_workdir":
		return "Thư mục làm việc chưa được đặt."
	case "reflect.no_memory":
		return "Bộ nhớ dự án không khả dụng cho thư mục này."
	case "reflect.load_failed":
		return "Không thể tải insight: %v"
	case "reflect.empty":
		return "Chưa có insight lượt chạy nào. Insight được tự động tạo sau mỗi lượt chạy agent với 3+ lượt gọi công cụ hoặc sửa tệp."
	case "reflect.title":
		return "## Insight lượt chạy tích lũy\n\n"
	case "reflect.memory_location":
		return "Vị trí bộ nhớ: %s\n"
	case "knight.unavailable":
		return "Knight không khả dụng"
	case "pairing.rejected":
		return "Yêu cầu ghép nối hiện tại đã bị từ chối. Vui lòng khởi tạo lại để tiếp tục."
	case "pairing.blacklisted":
		return "Kênh này đã bị đưa vào danh sách đen do nhiều lần từ chối."
	default:
		return enCatalog(key)
	}
}
