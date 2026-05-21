import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:wakelock_plus/wakelock_plus.dart';

import 'core/providers/session_provider.dart';
import 'features/connect/connect_screen.dart';
import 'features/chat/chat_screen.dart';
import 'features/chat/ask_user_screen.dart';

const bool _demoMode = bool.fromEnvironment('DEMO', defaultValue: false);



void main() {
  runApp(const ProviderScope(child: GGCodeApp()));
}

class GGCodeApp extends StatelessWidget {
  const GGCodeApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'GGCode Mobile',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        colorScheme: const ColorScheme.dark(
          primary: Colors.blueAccent,
          surface: Color(0xFF0D0D14),
          onSurface: Colors.white,
        ),
        scaffoldBackgroundColor: const Color(0xFF0D0D14),
        appBarTheme: const AppBarTheme(
          backgroundColor: Color(0xFF0D0D14),
          elevation: 0,
          iconTheme: IconThemeData(color: Colors.white),
          titleTextStyle: TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.w600),
        ),
        useMaterial3: true,
      ),
      home: const AppShell(),
    );
  }
}

class AppShell extends ConsumerStatefulWidget {
  const AppShell({super.key});

  @override
  ConsumerState<AppShell> createState() => _AppShellState();
}

class _AppShellState extends ConsumerState<AppShell> with WidgetsBindingObserver {
  StreamSubscription<TunnelConnectionState>? _connSub;
  bool _wasConnectedBeforeBackground = false;
  bool _hasConnected = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);

    // Demo mode: inject sample messages for screenshots
    if (_demoMode) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final notifier = ref.read(chatProvider.notifier);
        final now = DateTime.now();
        notifier.addUserMessage('帮我重构 main.go 里的 agent loop，把流式处理和工具执行拆成独立函数');
        notifier.state = [
          ChatMessage(id: 'u1', isUser: true, text: '帮我重构 main.go 里的 agent loop，把流式处理和工具执行拆成独立函数', time: now),
          ChatMessage(id: 'a1', text: '我来帮你重构。先看一下当前的代码结构：', time: now.add(Duration(seconds: 1))),
          ChatMessage(id: 't1', toolName: 'Read', toolDetail: 'Read: internal/agent/agent.go', toolId: 'tool1', time: now.add(Duration(seconds: 2))),
          ChatMessage(id: 't1r', toolName: 'Read', toolDetail: 'Read: internal/agent/agent.go', toolResult: '// agent.go — 847 lines\npackage agent\n\nfunc (a *Agent) Run(ctx context.Context) error {\n\t// Main agent loop\n}\n\nfunc (a *Agent) RunStream(ctx context.Context) error {\n\t// Streaming mode\n}', toolId: 'tool1', time: now.add(Duration(seconds: 3))),
          ChatMessage(id: 't2', toolName: 'Grep', toolDetail: 'Grep: func.*agent.*Stream in internal/agent/', toolId: 'tool2', time: now.add(Duration(seconds: 4))),
          ChatMessage(id: 'a2', text: '代码结构分析完毕。我的重构方案：\n\n1. **拆分 `RunStream`** — 把流式处理提取到 `streamHandler`\n2. **独立工具执行器** — `toolExecutor` 负责工具调用和结果收集\n3. **上下文传递优化** — 用 `PipelineCtx` 替代全局状态\n\n开始重构？', time: now.add(Duration(seconds: 5))),
        ];
      });
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _connSub?.cancel();
    WakelockPlus.disable();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    super.didChangeAppLifecycleState(state);

    final connState = ref.read(connectionProvider);
    final notifier = ref.read(connectionProvider.notifier);

    switch (state) {
      case AppLifecycleState.resumed:
        // App came back to foreground — check if we need to reconnect
        if (_wasConnectedBeforeBackground) {
          final currentStatus = ref.read(connectionProvider).status;
          if (currentStatus == ConnectionStatus.disconnected) {
            // Connection died while backgrounded — reconnect
            debugPrint('[app] Resumed: reconnecting...');
            notifier.reconnect();
          } else if (currentStatus == ConnectionStatus.connecting) {
            // Still trying — let it finish
          }
          // If connected, nothing to do
        }
        break;

      case AppLifecycleState.paused:
        // App going to background — remember connection state
        _wasConnectedBeforeBackground = connState.status == ConnectionStatus.connected;
        debugPrint('[app] Paused: wasConnected=$_wasConnectedBeforeBackground');
        break;

      case AppLifecycleState.inactive:
      case AppLifecycleState.hidden:
      case AppLifecycleState.detached:
        break;
    }
  }

  @override
  Widget build(BuildContext context) {
    final askUser = ref.watch(askUserProvider);

    // Manage wakelock based on connection state
    ref.listen<TunnelConnectionState>(connectionProvider, (prev, next) {
      if (next.status == ConnectionStatus.connected) {
        WakelockPlus.enable();
      } else if (prev?.status == ConnectionStatus.connected) {
        WakelockPlus.disable();
      }
    });

    // Show ask_user questionnaire as modal bottom sheet
    ref.listen<AskUserInfo?>(askUserProvider, (prev, next) {
      if (next != null && prev == null) {
        showModalBottomSheet(
          context: context,
          isScrollControlled: true,
          backgroundColor: Colors.transparent,
          builder: (_) => const AskUserScreen(),
        );
      }
    });

    // Track first successful connection
    ref.listen<TunnelConnectionState>(connectionProvider, (prev, next) {
      if (next.status == ConnectionStatus.connected && !_hasConnected) {
        setState(() { _hasConnected = true; });
      }
    });

    // Show ConnectScreen only before first successful connection.
    // Once connected, always show ChatScreen (connection status shown in AppBar).
    if (!_hasConnected && !_demoMode) {
      return const ConnectScreen();
    }

    return const ChatScreen();
  }
}
