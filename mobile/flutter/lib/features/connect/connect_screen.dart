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

  @override
  Widget build(BuildContext context) {
    final connState = ref.watch(connectionProvider);
    final history = ref.watch(_historyProvider);
    final isConnecting = connState.status == ConnectionStatus.connecting;
    final errorMsg = connState.error;

    if (_showScanner) {
      return Scaffold(
        backgroundColor: AppColors.background,
        body: SafeArea(
          child: Column(
            children: [
              // Top bar
              Padding(
                padding: const EdgeInsets.all(8),
                child: Row(
                  children: [
                    IconButton(
                      icon:
                          const Icon(Icons.close, color: AppColors.textPrimary),
                      onPressed: () => setState(() => _showScanner = false),
                    ),
                    Text(
                      t('connect.scan_qr'),
                      style: const TextStyle(
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
                padding: const EdgeInsets.all(16),
                child: Text(
                  'Point the camera at the QR code shown in GGCode Desktop',
                  style: const TextStyle(
                      color: AppColors.textSecondary, fontSize: 13),
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
                  gradient: const LinearGradient(
                    colors: [AppColors.surfaceElevated, AppColors.surface],
                    begin: Alignment.topLeft,
                    end: Alignment.bottomRight,
                  ),
                  borderRadius: BorderRadius.circular(24),
                  border: Border.all(color: AppColors.borderStrong),
                  boxShadow: AppShadows.panel,
                ),
                child: const Center(
                  child: Text(
                    'GG',
                    style: TextStyle(
                      color: AppColors.accent,
                      fontSize: 32,
                      fontWeight: FontWeight.bold,
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 16),
              Text(
                t('app.title'),
                style: const TextStyle(
                  color: AppColors.textPrimary,
                  fontSize: 24,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                t('connect.scan_hint'),
                style: const TextStyle(
                    color: AppColors.textSecondary, fontSize: 14),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 24),

              // Scan QR button (prominent)
              SizedBox(
                width: double.infinity,
                height: 56,
                child: OutlinedButton.icon(
                  onPressed: () => setState(() => _showScanner = true),
                  icon: const Icon(Icons.qr_code_scanner, size: 28),
                  label: Text(t('connect.scan_qr'),
                      style: const TextStyle(fontSize: 16)),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: AppColors.accent,
                    backgroundColor: AppColors.surface,
                    side: const BorderSide(color: AppColors.accent, width: 1.5),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(AppRadii.md),
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 20),

              // Divider with "or"
              Row(
                children: [
                  const Expanded(child: Divider(color: AppColors.border)),
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 12),
                    child: Text('or',
                        style: const TextStyle(
                            color: AppColors.textMuted, fontSize: 12)),
                  ),
                  const Expanded(child: Divider(color: AppColors.border)),
                ],
              ),
              const SizedBox(height: 20),

              // URL input
              TextField(
                controller: _urlController,
                style:
                    const TextStyle(color: AppColors.textPrimary, fontSize: 14),
                decoration: InputDecoration(
                  hintText: 'ws://host:port/ws?token=xxx',
                  hintStyle: const TextStyle(color: AppColors.textMuted),
                  filled: true,
                  fillColor: AppColors.surface,
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(AppRadii.md),
                    borderSide: BorderSide.none,
                  ),
                  prefixIcon:
                      const Icon(Icons.link, color: AppColors.textSecondary),
                  suffixIcon: _urlController.text.isNotEmpty
                      ? IconButton(
                          icon: const Icon(Icons.clear,
                              color: AppColors.textSecondary),
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

              // Error message
              if (errorMsg != null) ...[
                const SizedBox(height: 12),
                Container(
                  width: double.infinity,
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: AppColors.danger.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(AppRadii.sm),
                    border: Border.all(
                        color: AppColors.danger.withValues(alpha: 0.25)),
                  ),
                  child: Text(
                    errorMsg,
                    style:
                        const TextStyle(color: AppColors.danger, fontSize: 12),
                  ),
                ),
              ],

              // History
              if (history.isNotEmpty) ...[
                const SizedBox(height: 24),
                Align(
                  alignment: Alignment.centerLeft,
                  child: Text(
                    t('connect.recent_connections'),
                    style: const TextStyle(
                      color: AppColors.textSecondary,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                const SizedBox(height: 8),
                ...history.map((url) => ListTile(
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(AppRadii.sm),
                      ),
                      tileColor: AppColors.surface.withValues(alpha: 0.7),
                      contentPadding:
                          const EdgeInsets.symmetric(horizontal: 12),
                      dense: true,
                      leading: const Icon(Icons.history,
                          color: AppColors.textMuted, size: 18),
                      title: Text(
                        url,
                        style: const TextStyle(
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
