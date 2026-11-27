import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'core/wakelock.dart';

import 'core/l10n/app_localizations.dart';
import 'core/models/protocol.dart' as proto;
import 'core/providers/session_provider.dart';
import 'core/theme/app_theme.dart';
import 'features/connect/connect_screen.dart';
import 'features/chat/chat_screen.dart';
import 'features/chat/ask_user_screen.dart';

const bool _demoMode = bool.fromEnvironment('DEMO', defaultValue: false);

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  // OS locale detection happens after runApp via loadLanguagePreference
  await loadTranslations(defaultLanguage);
  runApp(const ProviderScope(child: GGCodeApp()));
}

class GGCodeApp extends ConsumerWidget {
  const GGCodeApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Watch theme changes to trigger rebuild
    ref.watch(themeProvider);
    final theme = buildAppTheme();
    final dark = theme.colorScheme.brightness == Brightness.dark;
    // M18: the status bar icons never followed the theme (ConnectScreen has
    // no AppBar, so iOS rendered dark icons over the dark gradient).
    final overlay = dark
        ? SystemUiOverlayStyle.light.copyWith(
            statusBarColor: Colors.transparent,
            systemNavigationBarColor: AppColors.background,
            systemNavigationBarIconBrightness: Brightness.light,
          )
        : SystemUiOverlayStyle.dark.copyWith(
            statusBarColor: Colors.transparent,
            systemNavigationBarColor: AppColors.background,
            systemNavigationBarIconBrightness: Brightness.dark,
          );
    return MaterialApp(
      title: 'GGCode Mobile',
      debugShowCheckedModeBanner: false,
      theme: theme,
      builder: (context, child) => AnnotatedRegion<SystemUiOverlayStyle>(
        value: overlay,
        child: child ?? const SizedBox.shrink(),
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

class _AppShellState extends ConsumerState<AppShell>
    with WidgetsBindingObserver {
  StreamSubscription<TunnelConnectionState>? _connSub;
  bool _wasConnectedBeforeBackground = false;
  bool _hasConnected = false;
  bool _bootstrapReconnectIssued = false;
  bool _bootstrapSelectionEvaluated = false;

  // M3: `captureLiveProjection` used to run on EVERY chat provider change
  // (one per streaming chunk), copying the whole message list and writing
  // workspaceCacheProvider state - a second full rebuild of the chat list per
  // chunk. The disk flush is already debounced at 350ms; mirror that here.
  Timer? _projectionTimer;

  static const _projectionDebounce = Duration(milliseconds: 350);

  void _flushProjectionNow() {
    _projectionTimer?.cancel();
    _projectionTimer = null;
    _captureProjection();
  }

  void _captureProjection() {
    if (_demoMode) return;
    ref
        .read(workspaceCacheProvider.notifier)
        .captureLiveProjection(
          messages: ref.read(chatProvider),
          subagents: ref.read(subagentProvider),
          sessionInfo: ref.read(sessionInfoProvider),
          agentStatus: ref.read(agentStatusProvider),
          agentStatusMessage: ref.read(agentStatusMessageProvider),
          lastEventId: ref.read(connectionProvider.notifier).lastAppliedEventId,
          authoritative: ref
              .read(connectionProvider.notifier)
              .canPersistLiveProjection,
          sessionUrl: ref.read(connectionProvider.notifier).liveSessionUrl,
        );
  }

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    Future.microtask(
      () => ref.read(workspaceCacheProvider.notifier).initialize(),
    );
    Future.microtask(
      () => ref.read(themeProvider.notifier).loadThemePreference(),
    );
    Future.microtask(() => loadLanguagePreference(ref));

    // Demo mode: inject sample messages for screenshots
    if (_demoMode) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final notifier = ref.read(chatProvider.notifier);
        final now = DateTime.now();
        notifier.addUserMessage('帮我重构 main.go 里的 agent loop，把流式处理和工具执行拆成独立函数');
        notifier.set([
          ChatMessage(
            id: 'u1',
            isUser: true,
            text: '帮我重构 main.go 里的 agent loop，把流式处理和工具执行拆成独立函数',
            time: now,
          ),
          ChatMessage(
            id: 'a1',
            text: '我来帮你重构。先看一下当前的代码结构：',
            time: now.add(Duration(seconds: 1)),
          ),
          ChatMessage(
            id: 't1',
            toolName: 'Read',
            toolDetail: 'Read: internal/agent/agent.go',
            toolId: 'tool1',
            time: now.add(Duration(seconds: 2)),
          ),
          ChatMessage(
            id: 't1r',
            toolName: 'Read',
            toolDetail: 'Read: internal/agent/agent.go',
            toolResult: '// agent.go — 847 lines\npackage agent\n\nfunc (a *Agent) Run(ctx context.Context) error {\n\t// Main agent loop\n}\n\nfunc (a *Agent) RunStream(ctx context.Context) error {\n\t// Streaming mode\n}',
            toolId: 'tool1',
            time: now.add(Duration(seconds: 3)),
          ),
          ChatMessage(
            id: 't2',
            toolName: 'Grep',
            toolDetail: 'Grep: func.*agent.*Stream in internal/agent/',
            toolId: 'tool2',
            time: now.add(Duration(seconds: 4)),
          ),
          ChatMessage(
            id: 'a2',
            text: '代码结构分析完毕。我的重构方案：\n\n1. **拆分 `RunStream`** — 把流式处理提取到 `streamHandler`\n2. **独立工具执行器** — `toolExecutor` 负责工具调用和结果收集\n3. **上下文传递优化** — 用 `PipelineCtx` 替代全局状态\n\n开始重构？',
            time: now.add(Duration(seconds: 5)),
          ),
        ]);
      });
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _connSub?.cancel();
    _projectionTimer?.cancel();
    wakelockDisable();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    super.didChangeAppLifecycleState(state);

    final connState = ref.read(connectionProvider);
    final notifier = ref.read(connectionProvider.notifier);
    void flushWorkspaceCache() {
      if (_demoMode) return;
      // Pending (debounced) projection must land before the disk flush.
      _flushProjectionNow();
      unawaited(ref.read(workspaceCacheProvider.notifier).flushNow());
    }

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
          } else {
            // Connected — but iOS may have frozen with the socket torn
            // down before the close callback ran (#1874 case 2): probe
            // liveness and reconnect if the state is stale.
            notifier.verifyLivenessOnResume();
          }
          // If connected, nothing to do
        }
        break;

      case AppLifecycleState.paused:
        // App going to background — remember connection state
        _wasConnectedBeforeBackground =
            connState.status == ConnectionStatus.connected;
        debugPrint('[app] Paused: wasConnected=$_wasConnectedBeforeBackground');
        flushWorkspaceCache();
        break;

      case AppLifecycleState.inactive:
      case AppLifecycleState.hidden:
      case AppLifecycleState.detached:
        flushWorkspaceCache();
        break;
    }
  }

  @override
  Widget build(BuildContext context) {
    final workspaceCache = ref.watch(workspaceCacheProvider);
    final connState = ref.watch(connectionProvider);

    // Manage wakelock based on connection state
    ref.listen<TunnelConnectionState>(connectionProvider, (prev, next) {
      if (next.status == ConnectionStatus.connected) {
        wakelockEnable();
      } else if (prev?.status == ConnectionStatus.connected) {
        wakelockDisable();
      }
    });

    // Show ask_user questionnaire as modal bottom sheet
    ref.listen<AskUserInfo?>(askUserProvider, (prev, next) {
      if (next != null && prev == null) {
        FocusManager.instance.primaryFocus?.unfocus();
        showModalBottomSheet(
          context: context,
          isScrollControlled: true,
          backgroundColor: Colors.transparent,
          builder: (_) => const AskUserScreen(),
        ).whenComplete(() {
          // #2816: drag-down / barrier tap pops the route without clearing
          // askUserProvider (the submit/cancel paths clear it BEFORE the
          // pop, so a surviving question here means gesture dismiss).
          // Treat it as cancel — otherwise this sheet's open edge
          // (prev==null && next!=null) never fires again and the next
          // questionnaire is silently invisible to mobile users.
          final pending = ref.read(askUserProvider);
          if (pending != null) {
            cancelPendingAskUser(ref, pending.id);
          }
        });
      }
    });

    // Track first successful connection
    ref.listen<TunnelConnectionState>(connectionProvider, (prev, next) {
      final connectedUsable =
          next.sessionReady ||
          (next.status == ConnectionStatus.connected && next.relaySync == null);
      if (connectedUsable && !_hasConnected) {
        setState(() {
          _hasConnected = true;
        });
        return;
      }
      final leftSession =
          next.status == ConnectionStatus.disconnected &&
          (next.url == null || next.url!.isEmpty);
      if (leftSession && _hasConnected) {
        setState(() {
          _hasConnected = false;
        });
      }
    });

    void persistLiveProjection() {
      if (_demoMode) return;
      _projectionTimer ??= Timer(_projectionDebounce, () {
        _projectionTimer = null;
        _captureProjection();
      });
    }

    ref.listen<List<ChatMessage>>(chatProvider, (prev, next) {
      persistLiveProjection();
    });
    ref.listen<Map<String, SubagentInfo>>(subagentProvider, (prev, next) {
      persistLiveProjection();
    });
    ref.listen<proto.SessionInfoData?>(sessionInfoProvider, (prev, next) {
      persistLiveProjection();
    });
    ref.listen<String>(agentStatusProvider, (prev, next) {
      persistLiveProjection();
    });
    ref.listen<String>(agentStatusMessageProvider, (prev, next) {
      persistLiveProjection();
    });

    if (!_demoMode &&
        workspaceCache.initialized &&
        !_bootstrapSelectionEvaluated) {
      _bootstrapSelectionEvaluated = true;
      final selectedSessionId = workspaceCache.selectedSessionId;
      final selectedWorkspaceKey = workspaceCache.selectedWorkspaceKey;
      if (((selectedSessionId != null && selectedSessionId.isNotEmpty) ||
              (selectedWorkspaceKey != null &&
                  selectedWorkspaceKey.isNotEmpty)) &&
          !_bootstrapReconnectIssued) {
        _bootstrapReconnectIssued = true;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          // Restore the foreground (selected) session
          ref.read(connectionProvider.notifier).restoreSelectedWorkspace();
          // Restore all other cached sessions as background connections
          ref
              .read(backgroundConnectionProvider.notifier)
              .connectAllCachedSessions();
        });
      }
    }

    if (!_demoMode && !workspaceCache.initialized) {
      return const Scaffold(body: Center(child: CircularProgressIndicator()));
    }

    // Show ConnectScreen only before first successful connection.
    // Once connected, always show ChatScreen (connection status shown in AppBar).
    final hasSelectedSession =
        workspaceCache.selectedSessionId != null &&
        workspaceCache.selectedSessionId!.isNotEmpty;
    final connectedUsable =
        connState.sessionReady ||
        (connState.status == ConnectionStatus.connected &&
            connState.relaySync == null);
    final showConnect =
        !_hasConnected && !_demoMode && !hasSelectedSession && !connectedUsable;
    // M5: fade-through between Connect and Chat (was an instant swap).
    return AnimatedSwitcher(
      duration: AppMotion.duration(context, AppMotion.emphasis),
      switchInCurve: AppMotion.decelerate,
      switchOutCurve: AppMotion.accelerate,
      transitionBuilder: (child, anim) => FadeTransition(
        opacity: anim,
        child: ScaleTransition(
          scale: Tween(begin: 0.985, end: 1.0).animate(anim),
          child: child,
        ),
      ),
      child: showConnect
          ? const ConnectScreen(key: ValueKey('connect'))
          : const ChatScreen(key: ValueKey('chat')),
    );
  }
}
