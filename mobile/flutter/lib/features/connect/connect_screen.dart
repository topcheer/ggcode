import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../core/l10n/app_localizations.dart';

import '../../core/providers/session_provider.dart';
import '../../core/theme/app_theme.dart';

class _SimpleNotifier<T> extends Notifier<T> {
  late T _value;
  late T Function() _init;

  @override
  T build() {
    _value = _init();
    return _value;
  }

  void set(T v) {
    _value = v;
    state = v;
  }
}

NotifierProvider<_SimpleNotifier<T>, T> _simpleProvider<T>(T Function() init) {
  return NotifierProvider<_SimpleNotifier<T>, T>(
    () {
      final n = _SimpleNotifier<T>();
      n._init = init;
      return n;
    },
  );
}

final _historyProvider = _simpleProvider<List<String>>(() => []);

class ConnectScreen extends ConsumerStatefulWidget {
  const ConnectScreen({super.key});

  @override
  ConsumerState<ConnectScreen> createState() => _ConnectScreenState();
}

class _ConnectScreenState extends ConsumerState<ConnectScreen> {
  final _urlController = TextEditingController();
  bool _showScanner = false;

  @override
  void initState() {
    super.initState();
    _loadHistory();
  }

  Future<void> _loadHistory() async {
    final history = await ConnectionNotifier.loadHistory();
    ref.read(_historyProvider.notifier).set(history);
  }

  Future<void> _saveToHistory(String url) async {
    final prefs = await SharedPreferences.getInstance();
    final history = prefs.getStringList('ggcode_history') ?? [];
    if (!history.contains(url)) {
      history.insert(0, url);
      if (history.length > 10) history.removeLast();
      await prefs.setStringList('ggcode_history', history);
      ref.read(_historyProvider.notifier).set(history);
    }
  }

  void _connect() {
    final url = _urlController.text.trim();
    if (url.isEmpty) return;
    _saveToHistory(url);
    ref.read(connectionProvider.notifier).connect(url);
  }

  void _handleQrCode(String code) {
    final url = normalizeTunnelUrl(code);

    setState(() {
      _showScanner = false;
      _urlController.text = url;
    });
    _saveToHistory(url);
    ref.read(connectionProvider.notifier).connect(url);
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

  @override
  Widget build(BuildContext context) {
    final connState = ref.watch(connectionProvider);
    final history = ref.watch(_historyProvider);
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
                child: MobileScanner(
                  controller: MobileScannerController(
                    facing: CameraFacing.back,
                    detectionSpeed: DetectionSpeed.normal,
                    torchEnabled: false,
                  ),
                  onDetect: (capture) {
                    if (capture.barcodes.isEmpty) return;
                    final rawValue = capture.barcodes.first.rawValue;
                    if (rawValue != null && rawValue.isNotEmpty) {
                      _handleQrCode(rawValue);
                    }
                  },
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
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              // Logo area
              Container(
                width: 88,
                height: 88,
                decoration: BoxDecoration(
                  gradient: LinearGradient(
                    colors: [AppColors.surfaceElevated, AppColors.surface],
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
                style: TextStyle(color: AppColors.textSecondary, fontSize: 14),
                textAlign: TextAlign.center,
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
                      : const Text('Connect'),
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

              // History
              if (history.isNotEmpty) ...[
                SizedBox(height: 24),
                Align(
                  alignment: Alignment.centerLeft,
                  child: Text(
                    t('connect.recent_connections'),
                    style: TextStyle(
                      color: AppColors.textSecondary,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                SizedBox(height: 8),
                ...history.map((url) => ListTile(
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(AppRadii.sm),
                      ),
                      tileColor: AppColors.surface.withValues(alpha: 0.7),
                      contentPadding: EdgeInsets.symmetric(horizontal: 12),
                      dense: true,
                      leading: Icon(Icons.history,
                          color: AppColors.textMuted, size: 18),
                      title: Text(
                        url,
                        style: TextStyle(
                            color: AppColors.textSecondary, fontSize: 12),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                      onTap: () {
                        _urlController.text = url;
                        setState(() {});
                      },
                    )),
              ],
            ],
          ),
        ),
      ),
    );
  }
}
