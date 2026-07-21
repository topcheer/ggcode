import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../core/qr_scanner.dart';

import '../../core/l10n/app_localizations.dart';
import '../../core/providers/connection_store.dart';
import '../../core/providers/session_provider.dart';
import '../../core/theme/app_theme.dart';

class ConnectScreen extends ConsumerStatefulWidget {
  const ConnectScreen({super.key});

  @override
  ConsumerState<ConnectScreen> createState() => _ConnectScreenState();
}

class _ConnectScreenState extends ConsumerState<ConnectScreen>
    with TickerProviderStateMixin {
  final _urlController = TextEditingController();
  bool _showScanner = false;
  List<StoredConnection> _connections = [];

  // Breathing animation for logo
  late AnimationController _breathController;
  late Animation<double> _breathAnimation;

  // Glow pulse for connecting state
  late AnimationController _pulseController;
  late Animation<double> _pulseAnimation;

  @override
  void initState() {
    super.initState();
    _loadConnections();

    _breathController = AnimationController(
      duration: const Duration(milliseconds: 2800),
      vsync: this,
    );
    _breathAnimation = Tween<double>(begin: 0.85, end: 1.0).animate(
      CurvedAnimation(parent: _breathController, curve: Curves.easeInOutSine),
    );
    _breathController.repeat(reverse: true);

    _pulseController = AnimationController(
      duration: const Duration(milliseconds: 1200),
      vsync: this,
    );
    _pulseAnimation = Tween<double>(begin: 0.3, end: 0.8).animate(
      CurvedAnimation(parent: _pulseController, curve: Curves.easeInOut),
    );
  }

  Future<void> _loadConnections() async {
    final store = ConnectionStore.instance;
    await store.load();
    if (!mounted) return;
    setState(() {
      _connections = store.all
          .where((c) => !c.permanentlyFailed)
          .toList()
        ..sort((a, b) => (b.lastConnectedAt ?? DateTime.fromMillisecondsSinceEpoch(0))
            .compareTo(a.lastConnectedAt ?? DateTime.fromMillisecondsSinceEpoch(0)));
    });
  }

  void _connect() {
    final url = _urlController.text.trim();
    if (url.isEmpty) return;
    ref.read(connectionProvider.notifier).connect(url);
  }

  void _handleQrCode(String code) {
    final url = normalizeTunnelUrl(code);
    setState(() {
      _showScanner = false;
      _urlController.text = url;
    });
    ref.read(connectionProvider.notifier).connect(url);
  }

  void _reconnect(StoredConnection conn) {
    ref.read(connectionProvider.notifier).connect(conn.url);
  }

  @override
  void dispose() {
    _urlController.dispose();
    _breathController.dispose();
    _pulseController.dispose();
    super.dispose();
  }

  String _progressTitle(TunnelConnectionState state) {
    final sync = state.relaySync;
    if (sync == null) return t('connect.connecting');
    if (sync.stalled) return t('relay_sync.stalled_title');
    switch (sync.phase) {
      case RelaySyncPhase.restoringLocal:
        return t('relay_sync.restoring_title');
      case RelaySyncPhase.waitingHost:
        return t(sync.recoveryState == 'pending'
            ? 'relay_sync.pending_title'
            : 'relay_sync.waiting_host_title');
      case RelaySyncPhase.waiting:
        return t('relay_sync.waiting_title');
      case RelaySyncPhase.replaying:
        return t(sync.resumeMode == 'full_history'
            ? 'relay_sync.full_history_title'
            : 'relay_sync.replaying_title');
      case RelaySyncPhase.snapshot:
        return t('relay_sync.snapshot_title');
    }
  }

  String _progressDetail(TunnelConnectionState state) {
    final sync = state.relaySync;
    if (sync == null) return t('connect.connecting_detail');
    if (sync.stalled) {
      return t('relay_sync.stalled_detail',
          args: {'count': (sync.remainingReplayCount ?? 0).toString()});
    }
    switch (sync.phase) {
      case RelaySyncPhase.restoringLocal:
        return t('relay_sync.restoring_detail');
      case RelaySyncPhase.waitingHost:
        return t(sync.recoveryState == 'pending'
            ? 'relay_sync.pending_detail'
            : sync.hasLocalState
                ? 'relay_sync.waiting_host_with_local_detail'
                : 'relay_sync.waiting_host_detail');
      case RelaySyncPhase.waiting:
        return t(sync.hasLocalState
            ? 'relay_sync.waiting_with_local_detail'
            : 'relay_sync.waiting_detail');
      case RelaySyncPhase.replaying:
        return t('relay_sync.replaying_detail',
            args: {'count': (sync.remainingReplayCount ?? 0).toString()});
      case RelaySyncPhase.snapshot:
        return t('relay_sync.snapshot_detail');
    }
  }

  String _formatTime(DateTime? dt) {
    if (dt == null) return '';
    final diff = DateTime.now().difference(dt);
    if (diff.inMinutes < 1) return 'just now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    if (diff.inHours < 24) return '${diff.inHours}h ago';
    return '${diff.inDays}d ago';
  }

  @override
  Widget build(BuildContext context) {
    final connState = ref.watch(connectionProvider);
    ref.listen<TunnelConnectionState>(connectionProvider, (prev, next) {
      if (prev?.status != next.status) {
        _loadConnections();
      }
    });
    ref.watch(workspaceCacheProvider);
    final isConnecting = connState.status == ConnectionStatus.connecting;
    final errorMsg = connState.error;
    final showProgress = isConnecting || connState.relaySync != null;

    // Pulse animation for connecting state
    if (showProgress && !_pulseController.isAnimating) {
      _pulseController.repeat(reverse: true);
    } else if (!showProgress && _pulseController.isAnimating) {
      _pulseController.stop();
    }

    if (_showScanner) {
      return _buildScanner();
    }

    return Scaffold(
      body: _buildGradientBackground(
        child: SafeArea(
          child: showProgress
              ? _buildConnectingView(connState, errorMsg)
              : SingleChildScrollView(
                  padding: const EdgeInsets.symmetric(horizontal: 28, vertical: 0),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      SizedBox(height: MediaQuery.of(context).size.height * 0.08),

                      // ─── Logo + Brand ───
                      _buildLogoSection(),

                      SizedBox(height: 48),

                      // ─── QR Scan (primary CTA) ───
                      _buildQrButton(),

                      SizedBox(height: 24),

                      // ─── Divider ───
                      _buildDivider(),

                      SizedBox(height: 24),

                      // ─── Manual URL input ───
                      _buildUrlInput(isConnecting),

                      if (errorMsg != null) ...[
                        SizedBox(height: 16),
                        _buildErrorBox(errorMsg),
                      ],

                      // ─── Recent connections ───
                      if (_connections.isNotEmpty) ...[
                        SizedBox(height: 40),
                        _buildRecentHeader(),
                        SizedBox(height: 12),
                        ...(_connections.map((conn) => _buildConnectionCard(conn, connState))),
                      ],

                      SizedBox(height: 40),
                    ],
                  ),
                ),
        ),
      ),
    );
  }

  // ═══════════════════════════════════════════════════════
  // Background
  // ═══════════════════════════════════════════════════════

  Widget _buildGradientBackground({required Widget child}) {
    return Stack(
      children: [
        // Base gradient
        Container(
          decoration: BoxDecoration(
            gradient: LinearGradient(
              begin: Alignment.topCenter,
              end: Alignment.bottomCenter,
              colors: [
                AppColors.background,
                AppColors.backgroundElevated,
              ],
            ),
          ),
        ),
        // Radial glow at top
        Positioned(
          top: -120,
          left: 0,
          right: 0,
          child: Container(
            height: 400,
            decoration: BoxDecoration(
              gradient: RadialGradient(
                center: Alignment.center,
                radius: 0.8,
                colors: [
                  AppColors.accent.withValues(alpha: 0.08),
                  AppColors.accentSoft.withValues(alpha: 0.03),
                  Colors.transparent,
                ],
              ),
            ),
          ),
        ),
        // Content
        Positioned.fill(child: child),
      ],
    );
  }

  // ═══════════════════════════════════════════════════════
  // Logo Section
  // ═══════════════════════════════════════════════════════

  Widget _buildLogoSection() {
    return Center(
      child: Column(
        children: [
          // Animated logo with glow
          AnimatedBuilder(
            animation: _breathAnimation,
            builder: (context, child) {
              return Transform.scale(
                scale: _breathAnimation.value,
                child: Container(
                  width: 96,
                  height: 96,
                  decoration: BoxDecoration(
                    borderRadius: BorderRadius.circular(28),
                    gradient: LinearGradient(
                      colors: [
                        AppColors.surfaceElevated,
                        AppColors.surface,
                      ],
                      begin: Alignment.topLeft,
                      end: Alignment.bottomRight,
                    ),
                    border: Border.all(
                      color: AppColors.borderStrong.withValues(alpha: 0.6),
                    ),
                    boxShadow: [
                      BoxShadow(
                        color: AppColors.accent.withValues(alpha: 0.12),
                        blurRadius: 32,
                        spreadRadius: 2,
                        offset: Offset(0, 8),
                      ),
                      ...AppShadows.panel,
                    ],
                  ),
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(24),
                    child: Image.asset(
                      'assets/icon.png',
                      width: 72,
                      height: 72,
                      fit: BoxFit.contain,
                    ),
                  ),
                ),
              );
            },
          ),
          SizedBox(height: 20),
          // App name
          Text(
            t('app.title'),
            style: TextStyle(
              color: AppColors.textPrimary,
              fontSize: 28,
              fontWeight: FontWeight.w700,
              letterSpacing: -0.5,
            ),
          ),
          SizedBox(height: 8),
          // Tagline
          Text(
            t('connect.scan_hint'),
            style: TextStyle(
              color: AppColors.textSecondary,
              fontSize: 14,
              height: 1.4,
            ),
            textAlign: TextAlign.center,
          ),
        ],
      ),
    );
  }

  // ═══════════════════════════════════════════════════════
  // QR Scan Button
  // ═══════════════════════════════════════════════════════

  Widget _buildQrButton() {
    return Container(
      width: double.infinity,
      height: 56,
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(AppRadii.md),
        color: AppColors.surfaceElevated,
        border: Border.all(
          color: AppColors.borderStrong.withValues(alpha: 0.6),
          width: 1,
        ),
      ),
      child: Material(
        color: Colors.transparent,
        borderRadius: BorderRadius.circular(AppRadii.md),
        child: InkWell(
          borderRadius: BorderRadius.circular(AppRadii.md),
          onTap: () => setState(() => _showScanner = true),
          child: Center(
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.qr_code_scanner, color: AppColors.accent, size: 24),
                SizedBox(width: 10),
                Text(
                  t('connect.scan_qr'),
                  style: TextStyle(
                    color: AppColors.textPrimary,
                    fontSize: 15,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  // ═══════════════════════════════════════════════════════
  // Divider
  // ═══════════════════════════════════════════════════════

  Widget _buildDivider() {
    return Row(
      children: [
        Expanded(
          child: Container(
            height: 1,
            decoration: BoxDecoration(
              gradient: LinearGradient(
                colors: [
                  Colors.transparent,
                  AppColors.border,
                ],
              ),
            ),
          ),
        ),
        Padding(
          padding: EdgeInsets.symmetric(horizontal: 16),
          child: Text(
            'or',
            style: TextStyle(
              color: AppColors.textMuted,
              fontSize: 12,
              fontWeight: FontWeight.w500,
              letterSpacing: 1,
            ),
          ),
        ),
        Expanded(
          child: Container(
            height: 1,
            decoration: BoxDecoration(
              gradient: LinearGradient(
                colors: [
                  AppColors.border,
                  Colors.transparent,
                ],
              ),
            ),
          ),
        ),
      ],
    );
  }

  // ═══════════════════════════════════════════════════════
  // URL Input
  // ═══════════════════════════════════════════════════════

  Widget _buildUrlInput(bool isConnecting) {
    return Column(
      children: [
        Container(
          decoration: BoxDecoration(
            color: AppColors.surface.withValues(alpha: 0.6),
            borderRadius: BorderRadius.circular(AppRadii.md),
            border: Border.all(color: AppColors.border.withValues(alpha: 0.5)),
          ),
          child: TextField(
            controller: _urlController,
            style: TextStyle(color: AppColors.textPrimary, fontSize: 14),
            decoration: InputDecoration(
              hintText: 'wss://host/ws?token=xxx',
              hintStyle: TextStyle(color: AppColors.textMuted, fontSize: 13),
              border: InputBorder.none,
              contentPadding: EdgeInsets.symmetric(horizontal: 16, vertical: 16),
              prefixIcon: Icon(Icons.link, color: AppColors.textSecondary, size: 20),
              suffixIcon: _urlController.text.isNotEmpty
                  ? IconButton(
                      icon: Icon(Icons.clear_rounded, color: AppColors.textSecondary, size: 20),
                      onPressed: () {
                        _urlController.clear();
                        setState(() {});
                      },
                    )
                  : null,
            ),
            onChanged: (v) => setState(() {}),
            onSubmitted: (_) => _connect(),
          ),
        ),
        SizedBox(height: 12),
        SizedBox(
          width: double.infinity,
          height: 50,
          child: FilledButton(
            onPressed: isConnecting ? null : _connect,
            style: FilledButton.styleFrom(
              backgroundColor: AppColors.surfaceElevated,
              foregroundColor: AppColors.textPrimary,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppRadii.md),
              ),
              side: BorderSide(color: AppColors.borderStrong.withValues(alpha: 0.4)),
            ),
            child: Text(
              t('connect.button_connect'),
              style: TextStyle(fontSize: 15, fontWeight: FontWeight.w600),
            ),
          ),
        ),
      ],
    );
  }

  // ═══════════════════════════════════════════════════════
  // Error Box
  // ═══════════════════════════════════════════════════════

  Widget _buildErrorBox(String errorMsg) {
    return Container(
      width: double.infinity,
      padding: EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      decoration: BoxDecoration(
        color: AppColors.danger.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(AppRadii.sm),
        border: Border.all(color: AppColors.danger.withValues(alpha: 0.2)),
      ),
      child: Row(
        children: [
          Icon(Icons.error_outline, color: AppColors.danger, size: 16),
          SizedBox(width: 8),
          Expanded(
            child: Text(
              errorMsg,
              style: TextStyle(color: AppColors.danger, fontSize: 12, height: 1.3),
            ),
          ),
        ],
      ),
    );
  }

  // ═══════════════════════════════════════════════════════
  // Recent Connections Header
  // ═══════════════════════════════════════════════════════

  Widget _buildRecentHeader() {
    return Row(
      children: [
        Text(
          'Recent',
          style: TextStyle(
            color: AppColors.textSecondary,
            fontSize: 13,
            fontWeight: FontWeight.w600,
            letterSpacing: 0.5,
          ),
        ),
        SizedBox(width: 8),
        Expanded(
          child: Container(height: 1, color: AppColors.border.withValues(alpha: 0.3)),
        ),
      ],
    );
  }

  // ═══════════════════════════════════════════════════════
  // Connection Card
  // ═══════════════════════════════════════════════════════

  Widget _buildConnectionCard(StoredConnection conn, TunnelConnectionState connState) {
    final cacheState = ref.read(workspaceCacheProvider);
    String sessionTitle = '';
    if (conn.sessionId != null && conn.sessionId!.isNotEmpty) {
      final session = cacheState.sessions[conn.sessionId];
      if (session != null && session.title.isNotEmpty) {
        sessionTitle = session.title;
      }
    }
    String name = sessionTitle.isNotEmpty
        ? sessionTitle
        : (conn.displayName?.isNotEmpty == true
            ? conn.displayName!
            : (conn.workspacePath?.isNotEmpty == true
                ? conn.workspacePath!.split('/').last
                : 'Unknown'));
    final subtitle = [
      if (sessionTitle.isEmpty && conn.displayName?.isNotEmpty == true)
        conn.displayName,
      if (conn.providerName != null && conn.providerName!.isNotEmpty)
        conn.providerName,
    ].join(' · ');
    final timeStr = _formatTime(conn.lastConnectedAt);
    final isLive = connState.sessionReady && conn.url == connState.url;

    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Material(
        color: AppColors.surface.withValues(alpha: 0.5),
        borderRadius: BorderRadius.circular(AppRadii.sm),
        child: InkWell(
          borderRadius: BorderRadius.circular(AppRadii.sm),
          onTap: connState.status == ConnectionStatus.connecting
              ? null
              : () => _reconnect(conn),
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 14),
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(AppRadii.sm),
              border: Border.all(
                color: isLive
                    ? AppColors.success.withValues(alpha: 0.2)
                    : AppColors.border.withValues(alpha: 0.3),
              ),
            ),
            child: Row(
              children: [
                // Workspace icon with live indicator
                Stack(
                  children: [
                    Container(
                      width: 38,
                      height: 38,
                      decoration: BoxDecoration(
                        color: isLive
                            ? AppColors.success.withValues(alpha: 0.1)
                            : AppColors.accent.withValues(alpha: 0.1),
                        borderRadius: BorderRadius.circular(10),
                      ),
                      child: Icon(
                        isLive ? Icons.terminal : Icons.folder_outlined,
                        color: isLive ? AppColors.success : AppColors.accent,
                        size: 20,
                      ),
                    ),
                    if (isLive)
                      Positioned(
                        top: 0,
                        right: 0,
                        child: Container(
                          width: 8,
                          height: 8,
                          decoration: BoxDecoration(
                            color: AppColors.success,
                            shape: BoxShape.circle,
                            border: Border.all(
                              color: AppColors.background,
                              width: 1.5,
                            ),
                          ),
                        ),
                      ),
                  ],
                ),
                SizedBox(width: 12),
                // Name + subtitle
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        name,
                        style: TextStyle(
                          color: AppColors.textPrimary,
                          fontSize: 14,
                          fontWeight: FontWeight.w500,
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                      if (subtitle.isNotEmpty) ...[
                        SizedBox(height: 2),
                        Text(
                          subtitle,
                          style: TextStyle(
                            color: AppColors.textMuted,
                            fontSize: 11,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ],
                    ],
                  ),
                ),
                // Status
                if (isLive)
                  Container(
                    padding: EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                    decoration: BoxDecoration(
                      color: AppColors.success.withValues(alpha: 0.12),
                      borderRadius: BorderRadius.circular(6),
                    ),
                    child: Text(
                      t('connect.live_badge'),
                      style: TextStyle(
                        color: AppColors.success,
                        fontSize: 10,
                        fontWeight: FontWeight.w700,
                        letterSpacing: 0.5,
                      ),
                    ),
                  )
                else if (timeStr.isNotEmpty)
                  Text(
                    timeStr,
                    style: TextStyle(
                      color: AppColors.textMuted,
                      fontSize: 11,
                    ),
                  ),
                SizedBox(width: 4),
                Icon(
                  Icons.chevron_right_rounded,
                  color: AppColors.textMuted,
                  size: 20,
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  // ═══════════════════════════════════════════════════════
  // Connecting View (immersive)
  // ═══════════════════════════════════════════════════════

  Widget _buildConnectingView(TunnelConnectionState connState, String? errorMsg) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 40),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            // Pulsing orb
            AnimatedBuilder(
              animation: _pulseAnimation,
              builder: (context, child) {
                return SizedBox(
                  width: 120,
                  height: 120,
                  child: Stack(
                    alignment: Alignment.center,
                    children: [
                      // Outer ring
                      Container(
                        width: 120,
                        height: 120,
                        decoration: BoxDecoration(
                          shape: BoxShape.circle,
                          border: Border.all(
                            color: AppColors.accent.withValues(alpha: _pulseAnimation.value * 0.3),
                            width: 2,
                          ),
                        ),
                      ),
                      // Middle ring
                      Container(
                        width: 90,
                        height: 90,
                        decoration: BoxDecoration(
                          shape: BoxShape.circle,
                          border: Border.all(
                            color: AppColors.accent.withValues(alpha: _pulseAnimation.value * 0.5),
                            width: 1.5,
                          ),
                        ),
                      ),
                      // Inner glow
                      Container(
                        width: 60,
                        height: 60,
                        decoration: BoxDecoration(
                          shape: BoxShape.circle,
                          gradient: RadialGradient(
                            colors: [
                              AppColors.accent.withValues(alpha: _pulseAnimation.value * 0.4),
                              AppColors.accentSoft.withValues(alpha: _pulseAnimation.value * 0.15),
                              Colors.transparent,
                            ],
                          ),
                        ),
                      ),
                      // Center icon
                      SizedBox(
                        width: 24,
                        height: 24,
                        child: CircularProgressIndicator(
                          strokeWidth: 2.5,
                          color: AppColors.accent,
                        ),
                      ),
                    ],
                  ),
                );
              },
            ),
            SizedBox(height: 40),
            // Title
            Text(
              _progressTitle(connState),
              style: TextStyle(
                color: AppColors.textPrimary,
                fontSize: 18,
                fontWeight: FontWeight.w600,
                letterSpacing: -0.2,
              ),
              textAlign: TextAlign.center,
            ),
            SizedBox(height: 10),
            // Detail
            Text(
              _progressDetail(connState),
              style: TextStyle(
                color: AppColors.textSecondary,
                fontSize: 14,
                height: 1.4,
              ),
              textAlign: TextAlign.center,
            ),
            // Error message (e.g., relay restart countdown)
            if (errorMsg != null && errorMsg.isNotEmpty) ...[
              SizedBox(height: 12),
              Text(
                errorMsg,
                style: TextStyle(
                  color: Colors.orange,
                  fontSize: 13,
                  height: 1.3,
                ),
                textAlign: TextAlign.center,
              ),
            ],
          ],
        ),
      ),
    );
  }

  // ═══════════════════════════════════════════════════════
  // QR Scanner
  // ═══════════════════════════════════════════════════════

  Widget _buildScanner() {
    return Scaffold(
      backgroundColor: AppColors.background,
      body: SafeArea(
        child: Column(
          children: [
            // Top bar
            Padding(
              padding: EdgeInsets.all(8),
              child: Row(
                children: [
                  IconButton(
                    icon: Icon(Icons.close_rounded, color: AppColors.textPrimary),
                    onPressed: () => setState(() => _showScanner = false),
                  ),
                  Text(
                    t('connect.scan_qr'),
                    style: TextStyle(
                        color: AppColors.textPrimary,
                        fontSize: 18,
                        fontWeight: FontWeight.w600),
                  ),
                ],
              ),
            ),
            // Scanner
            Expanded(
              child: buildQrScanner(
                onDetect: _handleQrCode,
              ),
            ),
            Padding(
              padding: EdgeInsets.all(16),
              child: Text(
                'Point the camera at the QR code shown in GGCode Desktop',
                style: TextStyle(color: AppColors.textSecondary, fontSize: 13),
                textAlign: TextAlign.center,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
