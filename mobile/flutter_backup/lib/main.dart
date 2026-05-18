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

class _AppShellState extends ConsumerState<AppShell> {
  StreamSubscription? _sub;

  @override
  void dispose() {
    _sub?.cancel();
    super.dispose();
  }

  void _listenToMessages(ConnectionState connState) {
    _sub?.cancel();

    final status = connState.status;
    if (status != ConnectionStatus.connected) return;

    // Re-acquire the service stream
    final notifier = ref.read(connectionProvider.notifier);
    final svc = notifier._service;
    if (svc == null) return;

    _sub = svc.messages.listen((msg) {
      final dispatcher = ref.read(messageDispatcherProvider);
      dispatcher(msg);
    });
  }

  @override
  Widget build(BuildContext context) {
    final connState = ref.watch(connectionProvider);
    final askUser = ref.watch(askUserProvider);
    final isConnected = connState.status == ConnectionStatus.connected;

    // Listen to connection changes
    ref.listen<ConnectionState>(connectionProvider, (prev, next) {
      _listenToMessages(next);
    });

    // Show ask_user questionnaire as a modal bottom sheet
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

    if (!isConnected) {
      return const ConnectScreen();
    }

    // Start listening if not already
    if (_sub == null) _listenToMessages(connState);

    return const ChatScreen();
  }
}
