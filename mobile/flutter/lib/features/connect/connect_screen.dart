import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import '../../core/providers/session_provider.dart';

class ConnectScreen extends ConsumerStatefulWidget {
  const ConnectScreen({super.key});

  @override
  ConsumerState<ConnectScreen> createState() => _ConnectScreenState();
}

class _ConnectScreenState extends ConsumerState<ConnectScreen> {
  final _urlController = TextEditingController();
  bool _isScanning = false;
  bool _isConnecting = false;

  @override
  void dispose() {
    _urlController.dispose();
    super.dispose();
  }

  Future<void> _connect(String url) async {
    if (url.trim().isEmpty) return;

    setState(() => _isConnecting = true);
    final service = ref.read(connectionProvider);
    await service.connect(url.trim());

    // Save to history
    ref.read(connectionHistoryProvider.notifier).add(url.trim());

    if (mounted) {
      setState(() => _isConnecting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final history = ref.watch(connectionHistoryProvider);
    final theme = Theme.of(context);

    return Scaffold(
      body: SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(24),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const SizedBox(height: 40),

              // App logo
              Center(
                child: Image.asset(
                  'assets/icon.png',
                  width: 120,
                  height: 120,
                  errorBuilder: (_, __, ___) => Container(
                    width: 120,
                    height: 120,
                    decoration: BoxDecoration(
                      color: theme.colorScheme.primaryContainer,
                      borderRadius: BorderRadius.circular(24),
                    ),
                    child: Icon(
                      Icons.code,
                      size: 60,
                      color: theme.colorScheme.onPrimaryContainer,
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 16),
              Text(
                'GGCode Mobile',
                textAlign: TextAlign.center,
                style: theme.textTheme.headlineMedium?.copyWith(
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: 4),
              Text(
                'Connect to your AI coding agent',
                textAlign: TextAlign.center,
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
              const SizedBox(height: 40),

              // QR Scanner toggle
              if (_isScanning) ...[
                Container(
                  height: 280,
                  decoration: BoxDecoration(
                    borderRadius: BorderRadius.circular(16),
                    border: Border.all(
                      color: theme.colorScheme.outlineVariant,
                    ),
                  ),
                  clipBehavior: Clip.antiAlias,
                  child: MobileScanner(
                    onDetect: (capture) {
                      final barcode = capture.barcodes.firstOrNull;
                      if (barcode != null && barcode.rawValue != null) {
                        _urlController.text = barcode.rawValue!;
                        setState(() => _isScanning = false);
                        _connect(barcode.rawValue!);
                      }
                    },
                  ),
                ),
                const SizedBox(height: 12),
                OutlinedButton.icon(
                  onPressed: () => setState(() => _isScanning = false),
                  icon: const Icon(Icons.close),
                  label: const Text('Close Scanner'),
                ),
              ] else ...[
                OutlinedButton.icon(
                  onPressed: () => setState(() => _isScanning = true),
                  icon: const Icon(Icons.qr_code_scanner),
                  label: const Text('Scan QR Code'),
                  style: OutlinedButton.styleFrom(
                    padding: const EdgeInsets.symmetric(vertical: 16),
                  ),
                ),
              ],

              const SizedBox(height: 24),

              // Divider with text
              Row(children: [
                const Expanded(child: Divider()),
                Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 16),
                  child: Text(
                    'OR ENTER URL',
                    style: theme.textTheme.labelSmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                ),
                const Expanded(child: Divider()),
              ]),

              const SizedBox(height: 16),

              // URL input
              TextField(
                controller: _urlController,
                decoration: InputDecoration(
                  hintText: 'wss://abc123.lhr.life/ws?token=...',
                  prefixIcon: const Icon(Icons.link),
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(12),
                  ),
                  filled: true,
                ),
                keyboardType: TextInputType.url,
                onSubmitted: (v) => _connect(v),
              ),
              const SizedBox(height: 16),

              // Connect button
              FilledButton(
                onPressed: _isConnecting
                    ? null
                    : () => _connect(_urlController.text),
                style: FilledButton.styleFrom(
                  padding: const EdgeInsets.symmetric(vertical: 16),
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(12),
                  ),
                ),
                child: _isConnecting
                    ? const SizedBox(
                        height: 20,
                        width: 20,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('Connect'),
              ),

              // Connection history
              if (history.isNotEmpty) ...[
                const SizedBox(height: 32),
                Row(
                  mainAxisAlignment: MainAxisAlignment.spaceBetween,
                  children: [
                    Text(
                      'Recent Connections',
                      style: theme.textTheme.titleSmall,
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                ...history.map((url) => Card(
                      margin: const EdgeInsets.only(bottom: 8),
                      child: ListTile(
                        dense: true,
                        leading: const Icon(Icons.history, size: 20),
                        title: Text(
                          url,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: theme.textTheme.bodySmall,
                        ),
                        trailing: IconButton(
                          icon: const Icon(Icons.close, size: 18),
                          onPressed: () {
                            ref
                                .read(connectionHistoryProvider.notifier)
                                .remove(url);
                          },
                        ),
                        onTap: () {
                          _urlController.text = url;
                          _connect(url);
                        },
                      ),
                    )),
              ],
            ],
          ),
        ),
      ),
    );
  }
}
