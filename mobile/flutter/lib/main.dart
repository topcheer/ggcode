import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'core/providers/session_provider.dart';
import 'core/models/protocol.dart' as proto;
import 'features/connect/connect_screen.dart';
import 'features/chat/chat_screen.dart';

void main() {
  runApp(const ProviderScope(child: GGCodeApp()));
}

class GGCodeApp extends ConsumerWidget {
  const GGCodeApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return MaterialApp(
      title: 'GGCode Mobile',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        colorSchemeSeed: const Color(0xFF6750A4),
        brightness: Brightness.dark,
        useMaterial3: true,
        textTheme: const TextTheme(
          bodyMedium: TextStyle(fontSize: 15),
          bodySmall: TextStyle(fontSize: 13),
        ),
      ),
      home: const _AppRoot(),
    );
  }
}

class _AppRoot extends ConsumerStatefulWidget {
  const _AppRoot();

  @override
  ConsumerState<_AppRoot> createState() => _AppRootState();
}

class _AppRootState extends ConsumerState<_AppRoot> {
  StreamSubscription<ConnectionStatus>? _statusSub;
  StreamSubscription<proto.WsMessage>? _msgSub;
  bool _isConnected = false;

  @override
  void initState() {
    super.initState();
    _setupListeners();
  }

  void _setupListeners() {
    final service = ref.read(connectionProvider);

    // Listen to connection status changes
    _statusSub = service.statusStream.listen((status) {
      final wasConnected = _isConnected;
      _isConnected = status == ConnectionStatus.connected;

      if (wasConnected && !_isConnected) {
        if (mounted) {
          ref.read(chatProvider.notifier).clear();
          ref.read(sessionInfoProvider.notifier).state = null;
          ref.read(pendingApprovalProvider.notifier).state = null;
          ref.read(agentStatusProvider.notifier).state =
              proto.AgentStatus(status: 'idle', message: '');
          Navigator.of(context).pushAndRemoveUntil(
            MaterialPageRoute(builder: (_) => const ConnectScreen()),
            (route) => false,
          );
        }
      }
    });

    // Subscribe to messages
    _msgSub = service.messageStream.listen((msg) {
      _handleMessage(msg);
    });
  }

  void _handleMessage(proto.WsMessage msg) {
    final data = msg.data ?? {};

    switch (msg.type) {
      case 'connected':
        // Connection confirmed
        break;

      case 'session_info':
        final info = proto.SessionInfo.fromData(data);
        ref.read(sessionInfoProvider.notifier).state = info;
        ref.read(currentModeProvider.notifier).state = info.mode;
        break;

      case 'text':
        final chunk = proto.TextChunk.fromData(data);
        final chatNotifier = ref.read(chatProvider.notifier);
        final buffers = ref.read(chatProvider).streamingBuffers;
        if (!buffers.containsKey(chunk.id)) {
          chatNotifier.startAgentMessage(chunk.id);
        }
        chatNotifier.appendTextChunk(chunk.id, chunk.chunk);
        if (chunk.done) {
          chatNotifier.finishAgentMessage(chunk.id);
        }
        break;

      case 'text_done':
        final id = data['id'] as String? ?? '';
        ref.read(chatProvider.notifier).finishAgentMessage(id);
        break;

      case 'status':
        final status = proto.AgentStatus.fromData(data);
        ref.read(agentStatusProvider.notifier).state = status;
        break;

      case 'tool_call':
        final tc = proto.ToolCall.fromData(data);
        ref.read(chatProvider.notifier).addToolCall(tc);
        break;

      case 'tool_result':
        final tr = proto.ToolResult.fromData(data);
        ref.read(chatProvider.notifier).addToolResult(tr);
        break;

      case 'approval_request':
        final ar = proto.ApprovalRequest.fromData(data);
        ref.read(pendingApprovalProvider.notifier).state = ar;
        break;

      case 'error':
        final err = proto.ErrorEvent.fromData(data);
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(
              content: Text('Error: ${err.message}'),
              backgroundColor: Colors.red.shade800,
            ),
          );
        }
        break;
    }
  }

  @override
  void dispose() {
    _statusSub?.cancel();
    _msgSub?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final service = ref.watch(connectionProvider);
    // Rebuild when status changes by watching the stream
    ref.watch(connectionStatusProvider);

    if (service.status == ConnectionStatus.connected) {
      return const ChatScreen();
    }
    return const ConnectScreen();
  }
}
