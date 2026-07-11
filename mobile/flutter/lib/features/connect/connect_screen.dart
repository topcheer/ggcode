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

class _ConnectScreenState extends ConsumerState<ConnectScreen> {
  final _urlController = TextEditingController();
  bool _showScanner = false;
  List<StoredConnection> _connections = [];

  @override
  void initState() {
    super.initState();
    _loadConnections();
  }

  Future<void> _loadConnections() async {
    final store = ConnectionStore.instance;
    await store.load();
    if (!mounted) return;
    setState(() {
      // Show non-failed connections first, sorted by most recent
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
    super.dispose();
  }

  String _progressTitle(TunnelConnectionState state) {
    final sync = state.relaySync;
    if (sync == null) {
      return t('connect.connecting');
    }
    if (sync.stalled) {
      return t('relay_sync.stalled_title');
    }
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
    if (sync == null) {
      return t('connect.connecting_detail');
    }
    if (sync.stalled) {
      return t(
        'relay_sync.stalled_detail',
        args: {'count': (sync.remainingReplayCount ?? 0).toString()},
      );
    }
    switch (sync.phase) {
      case RelaySyncPhase.restoringLocal:
        return t('relay_sync.restoring_detail');
      case RelaySyncPhase.waitingHost:
        return t(
          sync.recoveryState == 'pending'
              ? 'relay_sync.pending_detail'
              : sync.hasLocalState
                  ? 'relay_sync.waiting_host_with_local_detail'
                  : 'relay_sync.waiting_host_detail',
        );
      case RelaySyncPhase.waiting:
        return t(sync.hasLocalState
            ? 'relay_sync.waiting_with_local_detail'
            : 'relay_sync.waiting_detail');
      case RelaySyncPhase.replaying:
        return t(
          'relay_sync.replaying_detail',
          args: {'count': (sync.remainingReplayCount ?? 0).toString()},
        );
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
    // Reload connections list when connection status changes (connected/disconnected)
    ref.listen<TunnelConnectionState>(connectionProvider, (prev, next) {
      if (prev?.status != next.status) {
        _loadConnections();
      }
    });
    // Watch cache for session title updates
    ref.watch(workspaceCacheProvider);
    final isConnecting = connState.status == ConnectionStatus.connecting;
    final errorMsg = connState.error;
    final showProgress = isConnecting || connState.relaySync != null;

    if (_showScanner) {
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
                      icon: Icon(Icons.close, color: AppColors.textPrimary),
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
                  style:
                      TextStyle(color: AppColors.textSecondary, fontSize: 13),
                  textAlign: TextAlign.center,
                ),
              ),
            ],
          ),
        ),
      );
    }

    return Scaffold(
      backgroundColor: AppColors.background,
      body: SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 32),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // Logo area — centered
              Center(
                child: Column(
                  children: [
                    Container(
                      width: 88,
                      height: 88,
                      decoration: BoxDecoration(
                        gradient: LinearGradient(
                          colors: [
                            AppColors.surfaceElevated,
                            AppColors.surface
                          ],
                          begin: Alignment.topLeft,
                          end: Alignment.bottomRight,
                        ),
                        borderRadius: BorderRadius.circular(24),
                        border: Border.all(color: AppColors.borderStrong),
                        boxShadow: AppShadows.panel,
                      ),
                      child: ClipRRect(
                        borderRadius: BorderRadius.circular(20),
                        child: Image.asset(
                          'assets/icon.png',
                          width: 72,
                          height: 72,
                          fit: BoxFit.contain,
                        ),
                      ),
                    ),
                    SizedBox(height: 16),
                    Text(
                      t('app.title'),
                      style: TextStyle(
                        color: AppColors.textPrimary,
                        fontSize: 24,
                        fontWeight: FontWeight.bold,
                      ),
                    ),
                    SizedBox(height: 8),
                    Text(
                      t('connect.scan_hint'),
                      style: TextStyle(
                          color: AppColors.textSecondary, fontSize: 14),
                      textAlign: TextAlign.center,
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 24),

              // Scan QR button (prominent)
              SizedBox(
                width: double.infinity,
                height: 56,
                child: OutlinedButton.icon(
                  onPressed: () => setState(() => _showScanner = true),
                  icon: Icon(Icons.qr_code_scanner, size: 28),
                  label: Text(t('connect.scan_qr'),
                      style: TextStyle(fontSize: 16)),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: AppColors.accent,
                    backgroundColor: AppColors.surface,
                    side: BorderSide(color: AppColors.accent, width: 1.5),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(AppRadii.md),
                    ),
                  ),
                ),
              ),
              SizedBox(height: 20),

              // Divider with "or"
              Row(
                children: [
                  Expanded(child: Divider(color: AppColors.border)),
                  Padding(
                    padding: EdgeInsets.symmetric(horizontal: 12),
                    child: Text('or',
                        style: TextStyle(
                            color: AppColors.textMuted, fontSize: 12)),
                  ),
                  Expanded(child: Divider(color: AppColors.border)),
                ],
              ),
              SizedBox(height: 20),

              // URL input
              TextField(
                controller: _urlController,
                style: TextStyle(color: AppColors.textPrimary, fontSize: 14),
                decoration: InputDecoration(
                  hintText: 'wss://host/ws?token=xxx',
                  hintStyle: TextStyle(color: AppColors.textMuted),
                  filled: true,
                  fillColor: AppColors.surface,
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(AppRadii.md),
                    borderSide: BorderSide.none,
                  ),
                  prefixIcon: Icon(Icons.link, color: AppColors.textSecondary),
                  suffixIcon: _urlController.text.isNotEmpty
                      ? IconButton(
                          icon:
                              Icon(Icons.clear, color: AppColors.textSecondary),
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
              const SizedBox(height: 12),

              // Connect button
              SizedBox(
                width: double.infinity,
                height: 48,
                child: FilledButton(
                  onPressed: isConnecting ? null : _connect,
                  style: FilledButton.styleFrom(
                    backgroundColor: AppColors.accent,
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(AppRadii.md),
                    ),
                  ),
                  child: isConnecting
                      ? const SizedBox(
                          width: 20,
                          height: 20,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: Colors.white,
                          ),
                        )
                      : Text(t('connect.button_connect')),
                ),
              ),

              if (showProgress) ...[
                const SizedBox(height: 12),
                Container(
                  width: double.infinity,
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: AppColors.accent.withValues(alpha: 0.08),
                    borderRadius: BorderRadius.circular(AppRadii.sm),
                    border: Border.all(
                      color: AppColors.accent.withValues(alpha: 0.18),
                    ),
                  ),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: AppColors.accent,
                        ),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              _progressTitle(connState),
                              style: TextStyle(
                                color: AppColors.textPrimary,
                                fontSize: 13,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                            const SizedBox(height: 4),
                            Text(
                              _progressDetail(connState),
                              style: TextStyle(
                                color: AppColors.textSecondary,
                                fontSize: 12,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ],
                  ),
                ),
              ],

              // Error message
              if (errorMsg != null) ...[
                SizedBox(height: 12),
                Container(
                  width: double.infinity,
                  padding: EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: AppColors.danger.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(AppRadii.sm),
                    border: Border.all(
                        color: AppColors.danger.withValues(alpha: 0.25)),
                  ),
                  child: Text(
                    errorMsg,
                    style: TextStyle(color: AppColors.danger, fontSize: 12),
                  ),
                ),
              ],

              // Recent workspaces — workspace-organized instead of raw URLs
              if (_connections.isNotEmpty) ...[
                SizedBox(height: 32),
                Text(
                  'Recent Workspaces',
                  style: TextStyle(
                    color: AppColors.textSecondary,
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                SizedBox(height: 8),
                ...(_connections.map((conn) {
                  // Check workspace cache for session title
                  final cacheState = ref.read(workspaceCacheProvider);
                  String sessionTitle = '';
                  if (conn.sessionId != null && conn.sessionId!.isNotEmpty) {
                    final session = cacheState.sessions[conn.sessionId];
                    if (session != null && session.title.isNotEmpty) {
                      sessionTitle = session.title;
                    }
                  }
                  // Determine display info — prefer session title, then
                  // workspace name, then 'Unknown'
                  String name = sessionTitle.isNotEmpty
                      ? sessionTitle
                      : (conn.displayName?.isNotEmpty == true
                          ? conn.displayName!
                          : (conn.workspacePath?.isNotEmpty == true
                              ? conn.workspacePath!.split('/').last
                              : 'Unknown'));
                  final subtitle = [
                    if (sessionTitle.isEmpty &&
                        conn.displayName?.isNotEmpty == true)
                      conn.displayName,
                    if (conn.providerName != null &&
                        conn.providerName!.isNotEmpty)
                      conn.providerName,
                  ].join(' · ');
                  final timeStr = _formatTime(conn.lastConnectedAt);
                  // Live status: only when session is fully ready
                  // (WebSocket connected AND room exists AND session active).
                  // conn.alive is a stale snapshot from last run — not reliable.
                  final isLive = connState.sessionReady &&
                      conn.url == connState.url;

                  return Padding(
                    padding: const EdgeInsets.only(bottom: 8),
                    child: Material(
                      color: AppColors.surface.withValues(alpha: 0.7),
                      borderRadius: BorderRadius.circular(AppRadii.sm),
                      child: InkWell(
                        borderRadius: BorderRadius.circular(AppRadii.sm),
                        onTap: isConnecting ? null : () => _reconnect(conn),
                        child: Padding(
                          padding: const EdgeInsets.symmetric(
                              horizontal: 14, vertical: 12),
                          child: Row(
                            children: [
                              // Workspace icon
                              Container(
                                width: 36,
                                height: 36,
                                decoration: BoxDecoration(
                                  color: AppColors.accent.withValues(alpha: 0.12),
                                  borderRadius: BorderRadius.circular(8),
                                ),
                                child: Icon(
                                  Icons.folder_outlined,
                                  color: AppColors.accent,
                                  size: 20,
                                ),
                              ),
                              SizedBox(width: 12),
                              // Workspace name + subtitle
                              Expanded(
                                child: Column(
                                  crossAxisAlignment:
                                      CrossAxisAlignment.start,
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
                              // Status + time
                              Column(
                                crossAxisAlignment: CrossAxisAlignment.end,
                                children: [
                                  if (isLive)
                                    Container(
                                      padding: EdgeInsets.symmetric(
                                          horizontal: 6, vertical: 2),
                                      decoration: BoxDecoration(
                                        color: AppColors.success
                                            .withValues(alpha: 0.15),
                                        borderRadius:
                                            BorderRadius.circular(4),
                                      ),
                                      child: Text(
                                        t('connect.live_badge'),
                                        style: TextStyle(
                                          color: AppColors.success,
                                          fontSize: 10,
                                          fontWeight: FontWeight.w700,
                                        ),
                                      ),
                                    )
                                  else
                                    Text(
                                      timeStr,
                                      style: TextStyle(
                                        color: AppColors.textMuted,
                                        fontSize: 11,
                                      ),
                                    ),
                                  if (isLive && timeStr.isNotEmpty)
                                    SizedBox(height: 2),
                                  if (isLive && timeStr.isNotEmpty)
                                    Text(
                                      timeStr,
                                      style: TextStyle(
                                        color: AppColors.textMuted,
                                        fontSize: 11,
                                      ),
                                    ),
                                ],
                              ),
                              SizedBox(width: 4),
                              Icon(
                                Icons.chevron_right,
                                color: AppColors.textMuted,
                                size: 20,
                              ),
                            ],
                          ),
                        ),
                      ),
                    ),
                  );
                })),
              ],
            ],
          ),
        ),
      ),
    );
  }
}
