import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../core/providers/session_provider.dart';

final _historyProvider = StateProvider<List<String>>((ref) => []);
final _urlProvider = StateProvider<String>((ref) => '');

class ConnectScreen extends ConsumerStatefulWidget {
  const ConnectScreen({super.key});

  @override
  ConsumerState<ConnectScreen> createState() => _ConnectScreenState();
}

class _ConnectScreenState extends ConsumerState<ConnectScreen> {
  final _urlController = TextEditingController();

  @override
  void initState() {
    super.initState();
    _loadHistory();
  }

  Future<void> _loadHistory() async {
    final prefs = await SharedPreferences.getInstance();
    final history = prefs.getStringList('tunnel_history') ?? [];
    ref.read(_historyProvider.notifier).state = history;
  }

  Future<void> _saveToHistory(String url) async {
    final prefs = await SharedPreferences.getInstance();
    final history = prefs.getStringList('tunnel_history') ?? [];
    if (!history.contains(url)) {
      history.insert(0, url);
      if (history.length > 10) history.removeLast();
      await prefs.setStringList('tunnel_history', history);
      ref.read(_historyProvider.notifier).state = history;
    }
  }

  void _connect() {
    final url = _urlController.text.trim();
    if (url.isEmpty) return;
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
    final errorMsg = connState.error ?? (connState.status == ConnectionStatus.disconnected ? null : null);

    return Scaffold(
      body: SafeArea(
        child: Padding(
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
                'Connect to your GGCode agent',
                style: TextStyle(color: Colors.white.withOpacity(0.5), fontSize: 14),
              ),
              const SizedBox(height: 32),

              // URL input
              TextField(
                controller: _urlController,
                style: const TextStyle(color: Colors.white, fontSize: 14),
                decoration: InputDecoration(
                  hintText: 'ws://host:port/ws?token=xxx',
                  hintStyle: TextStyle(color: Colors.white.withOpacity(0.3)),
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
              const SizedBox(height: 16),

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
                    color: Colors.red.withOpacity(0.1),
                    borderRadius: BorderRadius.circular(8),
                    border: Border.all(color: Colors.red.withOpacity(0.3)),
                  ),
                  child: Text(
                    errorMsg,
                    style: const TextStyle(color: Colors.redAccent, fontSize: 12),
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
                      color: Colors.white.withOpacity(0.5),
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                const SizedBox(height: 8),
                ...history.map((url) => ListTile(
                      contentPadding: const EdgeInsets.symmetric(horizontal: 8),
                      dense: true,
                      leading: const Icon(Icons.history, color: Colors.white38, size: 18),
                      title: Text(
                        url,
                        style: const TextStyle(color: Colors.white70, fontSize: 12),
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
