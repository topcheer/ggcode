import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

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

class AppShell extends ConsumerWidget {
  const AppShell({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final connState = ref.watch(connectionProvider);
    final askUser = ref.watch(askUserProvider);
    final isConnected = connState.status == ConnectionStatus.connected;

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

    if (!isConnected) {
      return const ConnectScreen();
    }

    return const ChatScreen();
  }
}
