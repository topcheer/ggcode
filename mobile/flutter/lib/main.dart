import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:wakelock_plus/wakelock_plus.dart';

import 'core/providers/session_provider.dart';
import 'features/connect/connect_screen.dart';
import 'features/chat/chat_screen.dart';
import 'features/chat/ask_user_screen.dart';

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

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
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
    final connState = ref.watch(connectionProvider);
    final askUser = ref.watch(askUserProvider);
    final isConnected = connState.status == ConnectionStatus.connected;

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

    // Show dialog when server disconnects (only if NOT caused by app backgrounding)
    ref.listen<TunnelConnectionState>(connectionProvider, (prev, next) {
      if (prev?.status == ConnectionStatus.connected &&
          next.status == ConnectionStatus.disconnected) {
        // Small delay to skip disconnects caused by app backgrounding
        Future.delayed(const Duration(milliseconds: 500), () {
          if (!mounted) return;
          // If already reconnected, don't show dialog
          if (ref.read(connectionProvider).status != ConnectionStatus.disconnected) return;
          showDialog(
            context: context,
            barrierDismissible: false,
            builder: (ctx) => AlertDialog(
              backgroundColor: const Color(0xFF1A1A2E),
              title: const Text('连接已断开', style: TextStyle(color: Colors.white)),
              content: const Text(
                '服务端已离线，请返回扫码页面重新连接。',
                style: TextStyle(color: Colors.white70),
              ),
              actions: [
                TextButton(
                  onPressed: () {
                    Navigator.of(ctx).pop();
                  },
                  child: const Text('确定', style: TextStyle(color: Colors.blueAccent)),
                ),
              ],
            ),
          );
        });
      }
    });

    if (!isConnected) {
      return const ConnectScreen();
    }

    return const ChatScreen();
  }
}
