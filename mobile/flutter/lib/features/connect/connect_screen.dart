import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../core/providers/session_provider.dart';

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
        body: SafeArea(
          child: Column(
            children: [
              // Top bar
              Padding(
                padding: const EdgeInsets.all(8),
                child: Row(
                  children: [
                    IconButton(
                      icon: const Icon(Icons.close, color: Colors.white),
                      onPressed: () => setState(() => _showScanner = false),
                    ),
                    const Text(
                      'Scan QR Code',
                      style: TextStyle(
                          color: Colors.white,
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
                  style: TextStyle(
                      color: Colors.white.withValues(alpha: 0.5), fontSize: 13),
                  textAlign: TextAlign.center,
                ),
              ),
            ],
          ),
        ),
      );
    }

    return Scaffold(
      body: SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              // Logo area
              Container(
                width: 80,
                height: 80,
                decoration: BoxDecoration(
                  color: const Color(0xFF1A1A2E),
                  borderRadius: BorderRadius.circular(20),
                ),
                child: const Center(
                  child: Text(
                    'GG',
                    style: TextStyle(
                      color: Colors.blueAccent,
                      fontSize: 32,
                      fontWeight: FontWeight.bold,
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 16),
              const Text(
                'GGCode Mobile',
                style: TextStyle(
                  color: Colors.white,
                  fontSize: 24,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                'Scan QR code or enter URL to connect',
                style: TextStyle(
                    color: Colors.white.withValues(alpha: 0.5), fontSize: 14),
              ),
              const SizedBox(height: 24),

              // Scan QR button (prominent)
              SizedBox(
                width: double.infinity,
                height: 56,
                child: OutlinedButton.icon(
                  onPressed: () => setState(() => _showScanner = true),
                  icon: const Icon(Icons.qr_code_scanner, size: 28),
                  label: const Text('Scan QR Code',
                      style: TextStyle(fontSize: 16)),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: Colors.blueAccent,
                    side:
                        const BorderSide(color: Colors.blueAccent, width: 1.5),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(12),
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 20),

              // Divider with "or"
              Row(
                children: [
                  Expanded(
                      child:
                          Divider(color: Colors.white.withValues(alpha: 0.15))),
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 12),
                    child: Text('or',
                        style: TextStyle(
                            color: Colors.white.withValues(alpha: 0.3),
                            fontSize: 12)),
                  ),
                  Expanded(
                      child:
                          Divider(color: Colors.white.withValues(alpha: 0.15))),
                ],
              ),
              const SizedBox(height: 20),

              // URL input
              TextField(
                controller: _urlController,
                style: const TextStyle(color: Colors.white, fontSize: 14),
                decoration: InputDecoration(
                  hintText: 'ws://host:port/ws?token=xxx',
                  hintStyle:
                      TextStyle(color: Colors.white.withValues(alpha: 0.3)),
                  filled: true,
                  fillColor: const Color(0xFF1A1A2E),
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(12),
                    borderSide: BorderSide.none,
                  ),
                  prefixIcon: const Icon(Icons.link, color: Colors.white54),
                  suffixIcon: _urlController.text.isNotEmpty
                      ? IconButton(
                          icon: const Icon(Icons.clear, color: Colors.white54),
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
                    backgroundColor: Colors.blueAccent,
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(12),
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
                    color: Colors.red.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(8),
                    border:
                        Border.all(color: Colors.red.withValues(alpha: 0.3)),
                  ),
                  child: Text(
                    errorMsg,
                    style:
                        const TextStyle(color: Colors.redAccent, fontSize: 12),
                  ),
                ),
              ],

              // History
              if (history.isNotEmpty) ...[
                const SizedBox(height: 24),
                Align(
                  alignment: Alignment.centerLeft,
                  child: Text(
                    'Recent Connections',
                    style: TextStyle(
                      color: Colors.white.withValues(alpha: 0.5),
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                const SizedBox(height: 8),
                ...history.map((url) => ListTile(
                      contentPadding: const EdgeInsets.symmetric(horizontal: 8),
                      dense: true,
                      leading: const Icon(Icons.history,
                          color: Colors.white38, size: 18),
                      title: Text(
                        url,
                        style: const TextStyle(
                            color: Colors.white70, fontSize: 12),
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
