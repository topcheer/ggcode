import 'dart:async';
import 'dart:convert';
import 'dart:developer';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../connection_service.dart';
export '../connection_service.dart' show ConnectionStatus, normalizeTunnelUrl;
import 'session_context.dart';
import '../l10n/app_localizations.dart';
import '../models/protocol.dart' as proto;
import '../theme/app_theme.dart';

import 'chat_provider.dart';
import 'connection_store.dart';
import 'background_connection_manager.dart';
import 'ui_providers.dart';
import 'workspace_cache.dart';

final connectionProvider =
    NotifierProvider<ConnectionNotifier, TunnelConnectionState>(
  ConnectionNotifier.new,
);

const _noTunnelStateChange = Object();

enum RelaySyncPhase {
  restoringLocal,
  waitingHost,
  waiting,
  replaying,
  snapshot
}

class RelaySyncState {
  final RelaySyncPhase phase;
  final int? remainingReplayCount;
  final String? resumeMode;
  final String? recoveryState;
  final bool hasLocalState;
  final bool stalled;

  const RelaySyncState({
    required this.phase,
    this.remainingReplayCount,
    this.resumeMode,
    this.recoveryState,
    this.hasLocalState = false,
    this.stalled = false,
  });

  RelaySyncState copyWith({
    RelaySyncPhase? phase,
    Object? remainingReplayCount = _noTunnelStateChange,
    Object? resumeMode = _noTunnelStateChange,
    Object? recoveryState = _noTunnelStateChange,
    bool? hasLocalState,
    bool? stalled,
  }) {
    return RelaySyncState(
      phase: phase ?? this.phase,
      remainingReplayCount:
          identical(remainingReplayCount, _noTunnelStateChange)
              ? this.remainingReplayCount
              : remainingReplayCount as int?,
      resumeMode: identical(resumeMode, _noTunnelStateChange)
          ? this.resumeMode
          : resumeMode as String?,
      recoveryState: identical(recoveryState, _noTunnelStateChange)
          ? this.recoveryState
          : recoveryState as String?,
      hasLocalState: hasLocalState ?? this.hasLocalState,
      stalled: stalled ?? this.stalled,
    );
  }
}

class TunnelConnectionState {
  final ConnectionStatus status;
  final String? url;
  final String? error;
  final RelaySyncState? relaySync;
  final bool sessionReady;

  TunnelConnectionState({
    required this.status,
    this.url,
    this.error,
    this.relaySync,
    this.sessionReady = false,
  });

  TunnelConnectionState copyWith(
          {ConnectionStatus? status,
          Object? url = _noTunnelStateChange,
          Object? error = _noTunnelStateChange,
          Object? relaySync = _noTunnelStateChange,
          bool? sessionReady}) =>
      TunnelConnectionState(
        status: status ?? this.status,
        url: identical(url, _noTunnelStateChange) ? this.url : url as String?,
        error: identical(error, _noTunnelStateChange)
            ? this.error
            : error as String?,
        relaySync: identical(relaySync, _noTunnelStateChange)
            ? this.relaySync
            : relaySync as RelaySyncState?,
        sessionReady: sessionReady ?? this.sessionReady,
      );
}

class ConnectionNotifier extends Notifier<TunnelConnectionState> {
  ConnectionService? service;

  /// Single source of truth for per-connection mutable state.
  /// Replaced with a fresh instance on every new QR scan.
  SessionContext _ctx = SessionContext();

  // Delegates for backwards compat with existing code
  String get _clientId => _ctx.clientId;
  set _clientId(String v) => _ctx.clientId = v;
  String get _sessionId => _ctx.sessionId;
  set _sessionId(String v) => _ctx.sessionId = v;
  String get _lastAppliedEventId => _ctx.lastAppliedEventId;
  set _lastAppliedEventId(String v) => _ctx.lastAppliedEventId = v;
  String get _lastDurableEventId => _ctx.lastDurableEventId;
  set _lastDurableEventId(String v) => _ctx.lastDurableEventId = v;
  String get _resumeOverrideEventId => _ctx.resumeOverrideEventId;
  set _resumeOverrideEventId(String v) => _ctx.resumeOverrideEventId = v;
  String get _liveUrl => _ctx.liveUrl;
  set _liveUrl(String v) => _ctx.liveUrl = v;
  bool get _hasAuthoritativeProjection => _ctx.hasAuthoritativeProjection;
  set _hasAuthoritativeProjection(bool v) => _ctx.hasAuthoritativeProjection = v;
  int get _relayAuthorityEpoch => _ctx.relayAuthorityEpoch;
  set _relayAuthorityEpoch(int v) => _ctx.relayAuthorityEpoch = v;
  List<String> get _recentEventIds => _ctx.recentEventIds;
  Set<String> get _recentEventSet => _ctx.recentEventSet;
  bool get _awaitingSnapshotProjection => _ctx.awaitingSnapshotProjection;
  set _awaitingSnapshotProjection(bool v) => _ctx.awaitingSnapshotProjection = v;
  Timer? get _snapshotProjectionTimeout => _ctx.snapshotProjectionTimeout;
  set _snapshotProjectionTimeout(Timer? v) => _ctx.snapshotProjectionTimeout = v;
  bool get _resumeCompleted => _ctx.resumeCompleted;
  set _resumeCompleted(bool v) => _ctx.resumeCompleted = v;
  int get _pendingReplayCount => _ctx.pendingReplayCount;
  set _pendingReplayCount(int v) => _ctx.pendingReplayCount = v;
  String get _pendingResumeMode => _ctx.pendingResumeMode;
  set _pendingResumeMode(String v) => _ctx.pendingResumeMode = v;
  int? get _pendingActiveSessionBarrierOrdinal => _ctx.pendingActiveSessionBarrierOrdinal;
  set _pendingActiveSessionBarrierOrdinal(int? v) => _ctx.pendingActiveSessionBarrierOrdinal = v;
  String get _pendingActiveSessionBarrierEventId => _ctx.pendingActiveSessionBarrierEventId;
  set _pendingActiveSessionBarrierEventId(String v) => _ctx.pendingActiveSessionBarrierEventId = v;
  bool get _gapRecoveryScheduled => _ctx.gapRecoveryScheduled;
  set _gapRecoveryScheduled(bool v) => _ctx.gapRecoveryScheduled = v;
  bool get _gapRecoveryDeferred => _ctx.gapRecoveryDeferred;
  set _gapRecoveryDeferred(bool v) => _ctx.gapRecoveryDeferred = v;
  int get _gapRecoveryAttemptCount => _ctx.gapRecoveryAttemptCount;
  set _gapRecoveryAttemptCount(int v) => _ctx.gapRecoveryAttemptCount = v;

  /// The active connection's persistent state (resume cursor, workspace info).
  /// Null for background connections — each has its own ConnectionService.
  StoredConnection? _currentConnection;
  final ConnectionStore _connectionStore = ConnectionStore.instance;
  Future<void>? _connectInFlight;
  String? _connectInFlightUrl;
  int _connectionGeneration = 0;
  final List<StreamSubscription<dynamic>> _serviceSubscriptions =
      <StreamSubscription<dynamic>>[];
  final Map<String, Timer> _subagentCleanupTimers = <String, Timer>{};
  Timer? _relaySyncTimeout;

  @override
  TunnelConnectionState build() {
    final cache = ref.read(workspaceCacheProvider.notifier);
    cache.onDurableSnapshotPersisted = _handleDurableSnapshotPersisted;
    ref.onDispose(() {
      cache.onDurableSnapshotPersisted = null;
      _disposeNotifier();
    });
    return TunnelConnectionState(status: ConnectionStatus.disconnected);
  }

  String get currentSessionId => _sessionId;
  String get lastAppliedEventId => _lastAppliedEventId;
  String get liveSessionUrl => _liveUrl;
  ConnectionStore get connectionStore => _connectionStore;
  /// Current active connection's stored state (workspace info, etc.)
  StoredConnection? get currentConnection => _currentConnection;
  List<String> get recentEventIds => List.unmodifiable(_recentEventIds);
  bool get canPersistLiveProjection =>
      _hasAuthoritativeProjection &&
      !_awaitingSnapshotProjection &&
      _sessionId.isNotEmpty;

  Future<void> restoreSelectedWorkspace() async {
    final cache = ref.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    final cacheState = ref.read(workspaceCacheProvider);
    final selectedSessionId = cacheState.selectedSessionId;
    debugPrint('[connection] restoreSelectedWorkspace: selected=$selectedSessionId sessions=${cacheState.sessions.length}');
    if (selectedSessionId != null && selectedSessionId.isNotEmpty) {
      final session = cache.sessionForId(selectedSessionId);
      if (session != null && session.workspaceKey.isNotEmpty) {
        debugPrint('[connection] restoreSelectedWorkspace: found session in cache, connecting workspace=${session.workspaceKey}');
        await connectWorkspace(session.workspaceKey, clearState: false);
        _restoreCachedAgentStatus(sessionId: selectedSessionId);
        return;
      }
      // Session not in cache yet (hasn't been registered via registerLiveSession).
      // Try to connect directly from ConnectionStore URL.
      await _connectionStore.load();
      final conn = _connectionStore.findBySessionId(selectedSessionId);
      if (conn != null && conn.url.isNotEmpty) {
        debugPrint('[connection] restoreSelectedWorkspace: session not in cache, connecting directly from store url');
        await _connectImpl(
          conn.url,
          clearState: false,
        );
        return;
      }
      debugPrint('[connection] restoreSelectedWorkspace: no URL found for selected session, clearing selection');
    }
    final workspaceKey = cacheState.selectedWorkspaceKey;
    if (workspaceKey == null || workspaceKey.isEmpty) return;
    _restoreCachedAgentStatus(workspaceKey: workspaceKey);
    await connectWorkspace(workspaceKey, clearState: false);
  }

  Future<void> connectWorkspace(String workspaceKey,
      {bool clearState = true}) async {
    final cache = ref.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    // Prefer URL from ConnectionStore (has renew_token) over workspace_cache
    // (may have expired auth_ticket). ConnectionStore is updated by
    // metadataStream listener whenever relay issues a new renew_token.
    var url = cache.urlForWorkspace(workspaceKey);
    try {
      final store = ConnectionStore.instance;
      await store.load();
      final conn = store.all
          .where((c) =>
              c.workspacePath != null &&
              c.workspacePath!.isNotEmpty &&
              base64Url.encode(utf8.encode(c.workspacePath!)).replaceAll('=', '') == workspaceKey &&
              !c.permanentlyFailed)
          .toList()
        ..sort((a, b) => (b.lastConnectedAt ?? DateTime.fromMillisecondsSinceEpoch(0))
            .compareTo(a.lastConnectedAt ?? DateTime.fromMillisecondsSinceEpoch(0)));
      if (conn.isNotEmpty && conn.first.url.isNotEmpty) {
        url = conn.first.url;
        debugPrint('[connection] connectWorkspace: using ConnectionStore URL for workspace=$workspaceKey');
      }
    } catch (e) {
      debugPrint('[connection] connectWorkspace: ConnectionStore lookup failed: $e');
    }
    if (url == null || url.isEmpty) return;
    _restoreCachedAgentStatus(workspaceKey: workspaceKey);
    await connect(url, clearState: clearState);
  }

  Future<void> connectScannedCode(String rawCode,
      {bool clearState = true}) async {
    final url = normalizeTunnelUrl(rawCode);
    if (url.isEmpty) return;
    await connect(url, clearState: clearState);
  }

  ConnectionService createConnectionService(
          ShareConnectionDescriptor descriptor) =>
      ConnectionService(descriptor: descriptor);

  Future<void> connect(String url,
      {bool clearState = true, bool force = false}) async {
    debugPrint('[connection] connect() called url=$url clearState=$clearState');
    url = normalizeTunnelUrl(url);
    final activeUrl = normalizeTunnelUrl(state.url ?? '');
    if (!force &&
        service != null &&
        activeUrl == url &&
        (state.status == ConnectionStatus.connecting ||
            state.status == ConnectionStatus.connected)) {
      return _connectInFlight ?? Future<void>.value();
    }
    if (_connectInFlight != null && _connectInFlightUrl == url) {
      return _connectInFlight!;
    }
    final future = _connectImpl(url, clearState: clearState);
    _connectInFlight = future;
    _connectInFlightUrl = url;
    try {
      await future;
    } finally {
      if (identical(_connectInFlight, future)) {
        _connectInFlight = null;
        _connectInFlightUrl = null;
      }
    }
  }

  Future<void> _connectImpl(String url, {required bool clearState}) async {
    final generation = _nextConnectionGeneration();
    debugPrint('[connection] _connectImpl gen=$generation url=$url');
    final cache = ref.read(workspaceCacheProvider.notifier);
    await cache.initialize();
    if (!_isConnectionGenerationCurrent(generation)) {
      debugPrint('[connection] _connectImpl gen=$generation STALE — aborted');
      return;
    }
    cache.setPendingUrl(url);
    if (!_isConnectionGenerationCurrent(generation)) return;

    // Demote previous connection to background (keeps WebSocket alive)
    // instead of disconnecting it outright.
    demoteToBackground();

    var descriptor = ShareConnectionDescriptor.parse(url);
    state = state.copyWith(
        status: ConnectionStatus.connecting,
        url: url,  // Store full URL (with auth) for correct dedup and reconnection
        error: null,
        relaySync: null,
        sessionReady: false);

    // Snapshot current UI state before loading persisted values.
    final uiSessionId = _sessionId;
    final uiHasContent = ref.read(chatProvider).isNotEmpty;

    // Use StoredConnection for per-connection resume state.
    // New scan (clearState=true): create fresh connection, empty resume state.
    // Reconnect (clearState=false): look up existing by roomId.
    await _connectionStore.load();
    StoredConnection? conn = _connectionStore.findByRoomId(descriptor.roomId);
    // ALWAYS prefer renew_token URL from ConnectionStore, regardless of clearState.
    // The workspace_cache URL may have an expired auth_ticket (15min TTL).
    // renew_token has 30-day TTL.
    if (conn != null && conn.url.contains('renew_token') && !url.contains('renew_token')) {
      debugPrint('[connection] using ConnectionStore URL with renew_token for room=${descriptor.roomId} (clearState=$clearState)');
      url = conn.url;
      descriptor = ShareConnectionDescriptor.parse(url);
    }
    if (clearState && (conn == null || conn.url != url)) {
      // New connection from user scan — create fresh.
      conn = StoredConnection.forUrl(url, descriptor.roomId, active: true);
      await _connectionStore.add(conn);
    } else if (conn != null) {
      // Reconnecting to existing room — mark active.
      await _connectionStore.setActive(conn.id);
    }
    _currentConnection = conn;

    // clientId is device-level, not session-level — always restore it.
    _clientId = conn?.clientId ?? '';

    // If we have a StoredConnection for THIS room with a session ID, treat
    // it as a reconnect — use the stored session/event IDs for resume_from.
    // If conn is null or has no session, it's a fresh scan of a new room.
    final isReconnect =
        conn != null && (conn.sessionId?.isNotEmpty ?? false) && conn.roomId == descriptor.roomId;
    if (isReconnect) {
      _sessionId = conn.sessionId ?? '';
      _lastAppliedEventId = conn.lastEventId ?? '';
      _lastDurableEventId = conn.durableEventId ?? _lastAppliedEventId;
    }
    _relayAuthorityEpoch = 0;
    final localService = createConnectionService(descriptor);
    if (!_isConnectionGenerationCurrent(generation)) {
      localService.dispose();
      return;
    }
    service = localService;
    _liveUrl = url;
    debugPrint(
      '[connection] provider connect url=${descriptor.publicUrl} clearState=$clearState '
      'savedSession=$_sessionId lastEvent=$_lastAppliedEventId client=${_clientId.isNotEmpty}',
    );

    // _loadResumeState overwrites _sessionId/_lastAppliedEventId from prefs.
    final savedSessionId = _sessionId;

    // When clearState is true (e.g. scanned from chat screen), always do a
    // fresh restore from local cache. When false (e.g. reconnecting), keep
    // existing UI if it matches the session being reconnected.
    final keepExistingUi =
        !clearState && uiHasContent && uiSessionId == savedSessionId;

    if (keepExistingUi) {
      // UI already renders this session — keep it. Ordinal dedup will skip
      // relay replay events we already have rendered.
      _beginRelaySyncWaiting(hasLocalState: true);
    } else {
      // Different session or fresh connect.
      _clearUiProjection();
      // For new scans (clearState=true): clear all selection so the new
      // session becomes selected+live unconditionally.
      // For reconnects (clearState=false): DON'T clear live — keeping it
      // lets registerLiveSession see selected==prevLive and followLive=true.
      if (clearState) {
        ref.read(workspaceCacheProvider.notifier).clearAllSelection();
      }

      if (!isReconnect && clearState) {
        // Fresh QR scan of a NEW room: dispose old context, start completely clean.
        // No variables from the previous workspace can leak.
        final savedClientId = _ctx.clientId;
        _ctx.dispose();
        _ctx = SessionContext();
        // Preserve clientId (device-level ID, not workspace-scoped).
        _ctx.clientId = savedClientId;
        _relaySyncTimeout?.cancel();
        _relaySyncTimeout = null;
      } else {
        // Reconnect: preserve session + event IDs for resume_from.
        _snapshotProjectionTimeout?.cancel();
        _snapshotProjectionTimeout = null;
        _recentEventIds.clear();
        _recentEventSet.clear();
        _gapRecoveryScheduled = false;
        _gapRecoveryDeferred = false;
        _gapRecoveryAttemptCount = 0;
      }

      final hadSavedSession = _sessionId.isNotEmpty;
      if (hadSavedSession) {
        _beginLocalRestoreSync();
        final restoredProjection = _restoreProjectionFromCache(
          adoptCursor: false,
          seedCursorIfUnset: true,
        );
        _beginRelaySyncWaiting(hasLocalState: restoredProjection || hadSavedSession);
      } else {
        _beginRelaySyncWaiting(hasLocalState: false);
      }
    }

    _bindService(localService, generation, url);
    // Resume strategy is handled in _bindService() when the service
    // reaches connected status — single source of truth, no double send.

    try {
      // dart:io WebSocket.connect properly awaits handshake
      await localService.connect();
    } catch (e) {
      if (!_isConnectionGenerationCurrent(generation)) return;
      state = state.copyWith(
          status: ConnectionStatus.disconnected,
          error: e.toString(),
          sessionReady: false);
    }
  }

  /// Reconnect using the last known URL (e.g. after app resumes from background).
  /// Preserves existing chat state — server will replay recent messages.
  Future<void> reconnect() async {
    // Use _liveUrl (full URL with auth_ticket) instead of state.url (publicUrl).
    if (_liveUrl.isEmpty) {
      await restoreSelectedWorkspace();
      return;
    }
    await connect(_liveUrl, clearState: false);
  }

  void disconnect() {
    _nextConnectionGeneration();
    service?.disconnect();
    _disposeActiveService();
    _clearRelaySyncState();
    _relayAuthorityEpoch = 0;
    _liveUrl = '';
    ref.read(workspaceCacheProvider.notifier).markDisconnected();
    // Mark this connection as dead — user explicitly disconnected
    if (_currentConnection != null) {
      unawaited(_connectionStore.markDead(_currentConnection!.id));
    }
    state = state.copyWith(
      status: ConnectionStatus.disconnected,
      sessionReady: false,
    );
  }

  Future<void> leaveSession() async {
    _nextConnectionGeneration();
    _disposeActiveService();
    _clearUiProjection();
    _sessionId = '';
    _lastAppliedEventId = '';
    _lastDurableEventId = '';
    _resumeOverrideEventId = '';
    _relayAuthorityEpoch = 0;
    _awaitingSnapshotProjection = false;
    _snapshotProjectionTimeout?.cancel();
    _snapshotProjectionTimeout = null;
    _clearRelaySyncState();
    _recentEventIds.clear();
    _recentEventSet.clear();
    final prefs = await SharedPreferences.getInstance();
    await _clearPersistedResumeState(prefs);
    await ref.read(workspaceCacheProvider.notifier).clearSelection();
    // User is leaving the session — mark connection as dead
    if (_currentConnection != null) {
      unawaited(_connectionStore.markDead(_currentConnection!.id));
    }
    state = TunnelConnectionState(
      status: ConnectionStatus.disconnected,
      sessionReady: false,
    );
  }

  void send(Map<String, dynamic> data) {
    final msg = proto.WsMessage(
        type: data['type'] as String? ?? 'message',
        messageId: data['message_id'] as String?,
        data: data['data'] as Map<String, dynamic>?);
    service?.sendEncrypted(msg);
  }

  void _dispatchMessage(proto.WsMessage msg) {
    final chatNotifier = ref.read(chatProvider.notifier);

    // Handle server_ack from encrypted channel (Desktop → Client ack).
    if (msg.type == 'server_ack') {
      final messageId = msg.data?['message_id'] as String? ?? '';
      if (messageId.isNotEmpty) {
        chatNotifier.updateMessageStatus(messageId, MessageStatus.acknowledged);
      }
      return;
    }

    switch (msg.type) {
      case 'active_session':
        final sessionId =
            msg.sessionId ?? msg.data?['session_id'] as String? ?? '';
        log('[tunnel] active_session: session=$sessionId currentSession=$_sessionId lastEvent=$_lastAppliedEventId');
        if (sessionId.isEmpty) break;
        // Save previous session before _acceptAuthorityEpoch may change _sessionId
        final previousSessionId = _sessionId;
        if (!_acceptAuthorityEpoch(msg, sessionId: sessionId)) {
          break;
        }
        // Session switch: clear UI so replay can refill fresh content.
        // _acceptAuthorityEpoch may have already set _sessionId, so
        // compare against the saved previousSessionId.
        if (previousSessionId.isNotEmpty && previousSessionId != sessionId) {
          _clearUiProjection();
          _hasAuthoritativeProjection = false;
          _recentEventIds.clear();
          _recentEventSet.clear();
        }
        _sessionId = sessionId;
        _noteAuthorityEpoch(msg);
        final barrierOrdinal = msg.barrierOrdinal ??
            ((msg.data?['barrier_ordinal']) as num?)?.toInt();
        final barrierEventId = msg.barrierEventId ??
            msg.data?['barrier_event_id'] as String? ??
            '';
        if (barrierOrdinal != null &&
            barrierOrdinal > (_parseEventOrdinal(_lastAppliedEventId) ?? 0) &&
            !_resumeCompleted) {
          _pendingActiveSessionBarrierOrdinal = barrierOrdinal;
          _pendingActiveSessionBarrierEventId = barrierEventId;
          _beginRelaySyncWaiting(hasLocalState: _hasLocalSessionState());
        } else {
          _pendingActiveSessionBarrierOrdinal = null;
          _pendingActiveSessionBarrierEventId = '';
        }
        // Do not re-enter relaySync waiting after resume_ack has already
        // completed the resume cycle. The active_session arriving here is
        // triggered by handleRelayConnected(client) on the host side and its
        // snapshot_reset + replayCanonicalEvents will drive the sync instead.
        if (state.relaySync == null &&
            !_resumeCompleted &&
            _pendingActiveSessionBarrierOrdinal == null) {
          _beginRelaySyncWaiting(hasLocalState: _hasLocalSessionState());
        }
        // Register workspace from the active_session message — this carries
        // the correct workspace_path for THIS room. If absent, skip; the
        // session_info event (encrypted, from host) will register it.
        final sessionInfo = _sessionInfoFromActiveSession(msg.data);
        if (sessionInfo != null) {
          ref.read(sessionInfoProvider.notifier).set(sessionInfo);
          debugPrint('[connection] active_session sessionId=$sessionId workspace=${sessionInfo.workspace}');
          unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
              sessionId, sessionInfo,
              lastEventId: _lastAppliedEventId,
              authorityEpoch: _relayAuthorityEpoch));
        }
        _restoreSessionProjectionIfAvailable(sessionId);
        _persistResumeState();
        break;

      case 'resume_ack':
        final sessionId =
            msg.sessionId ?? msg.data?['session_id'] as String? ?? '';
        if (!_acceptAuthorityEpoch(msg, sessionId: sessionId)) {
          break;
        }
        _sessionId = sessionId;
        _noteAuthorityEpoch(msg);
        final replayCount = (msg.data?['replay_count'] as num?)?.toInt() ?? 0;
        final resumeMode = msg.data?['resume_mode'] as String? ?? 'incremental';
        final resumeFromEventId =
            msg.data?['resume_from_event_id'] as String? ?? '';
        debugPrint(
          '[connection] resume_ack session=$sessionId replay=$replayCount mode=$resumeMode',
        );
        _resumeOverrideEventId = '';
        _resumeCompleted = true;
        // Cancel any pending gap recovery — the relay has acknowledged our
        // resume point. If replay_count is 0, there's nothing to backfill.
        _gapRecoveryScheduled = false;
        _gapRecoveryDeferred = false;
        _gapRecoveryAttemptCount = 0;
        // Reset ordinal tracking so we don't re-detect the same gap.
        // The relay will send events starting from its current position.
        if (replayCount == 0 && resumeFromEventId.isNotEmpty) {
          _lastAppliedEventId = resumeFromEventId;
        }
        _beginResumeReplaySync(
          replayCount: replayCount,
          resumeMode: resumeMode,
          hasLocalState: _hasLocalSessionState(),
        );
        // Always restore local snapshot first — relay replay will skip
        // already-cached events via ordinal dedup.
        _restoreSessionProjectionIfAvailable(sessionId);
        unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
            sessionId, ref.read(sessionInfoProvider),
            lastEventId: _lastAppliedEventId,
            authorityEpoch: _relayAuthorityEpoch));
        _restoreCachedAgentStatus(sessionId: sessionId);
        _persistResumeState();
        break;

      case 'language_change':
        if (msg.data != null) {
          final lang = msg.data!['language'] as String? ?? '';
          if (lang.isNotEmpty) {
            ref.read(languageProvider.notifier).setLanguage(lang);
            unawaited(loadTranslations(lang));
          }
        }
        break;

      case 'theme_change':
        if (msg.data != null) {
          final theme = msg.data!['theme'] as String? ?? '';
          if (theme.isNotEmpty) {
            ref.read(themeProvider.notifier).setTheme(theme);
          }
        }
        break;

      case 'snapshot_reset':
        if (!_acceptAuthorityEpoch(
          msg,
          sessionId: msg.sessionId ?? _sessionId,
        )) {
          break;
        }
        _clearUiProjection();
        _lastAppliedEventId = '';
        _recentEventIds.clear();
        _recentEventSet.clear();
        // Reset ordinal cursor — snapshot_reset means the host is sending a
        // fresh authoritative view. Any previous _lastAppliedEventId is from
        // a different snapshot/resume and must not be used for gap detection
        // against the incoming snapshot events.
        _lastAppliedEventId = '';
        _lastDurableEventId = '';
        _awaitingSnapshotProjection = true;
        _snapshotProjectionTimeout?.cancel();
        _snapshotProjectionTimeout = Timer(const Duration(seconds: 15), () {
          // Safety net: if session_info never arrives (e.g. host bug or
          // network loss), clear the flag so the UI doesn't stay stuck on
          // the loading screen forever.
          if (_awaitingSnapshotProjection) {
            debugPrint(
                '[connection] snapshot projection timeout — clearing awaitingSnapshotProjection');
            _awaitingSnapshotProjection = false;
            _syncSessionReady();
          }
        });
        _beginSnapshotSync();
        _markProjectionAuthoritative();
        if (msg.sessionId != null && msg.sessionId!.isNotEmpty) {
          _sessionId = msg.sessionId!;
        }
        _noteAuthorityEpoch(msg);
        unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
            _sessionId, ref.read(sessionInfoProvider),
            lastEventId: _lastAppliedEventId,
            authorityEpoch: _relayAuthorityEpoch));
        _restoreCachedAgentStatus(sessionId: _sessionId);
        _persistResumeState();
        break;

      case 'session_info':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          // Even when dedup-skipped, session_info means the host has sent its
          // projection — clear the awaiting flag so sessionReady can become true.
          if (_awaitingSnapshotProjection) {
            _awaitingSnapshotProjection = false;
            _snapshotProjectionTimeout?.cancel();
            _snapshotProjectionTimeout = null;
            _syncSessionReady();
          }
          break;
        }
        final data = proto.SessionInfoData.fromJson(msg.data!);
        debugPrint('[session_info] title="${data.title}" workspace="${data.workspace}" sessionId=${msg.sessionId} eventID=${msg.eventId}');
        ref.read(sessionInfoProvider.notifier).set(data);
        ref.read(currentModeProvider.notifier).set(data.mode);
        // Sync language from desktop
        if (data.language.isNotEmpty) {
          ref.read(languageProvider.notifier).setLanguage(data.language);
          unawaited(loadTranslations(data.language));
        }
        if (data.theme.isNotEmpty) {
          ref.read(themeProvider.notifier).setTheme(data.theme);
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        final wasAwaitingSnapshotProjection = _awaitingSnapshotProjection;
        if (wasAwaitingSnapshotProjection) {
          _awaitingSnapshotProjection = false;
          _snapshotProjectionTimeout?.cancel();
          _snapshotProjectionTimeout = null;
        }
        if (_pendingReplayCount == 0) {
          _clearRelaySyncState();
        }
        unawaited(ref.read(workspaceCacheProvider.notifier).registerLiveSession(
              _sessionId.isNotEmpty ? _sessionId : (msg.sessionId ?? ''),
              data,
              lastEventId: _lastAppliedEventId,
              authorityEpoch: _relayAuthorityEpoch,
            ));
        break;

      case 'replay_done':
        // Host signals end of replay batch. Unconditionally clear sync state
        // so the UI stops showing "relay sync" loading indicators.
        _awaitingSnapshotProjection = false;
        _snapshotProjectionTimeout?.cancel();
        _snapshotProjectionTimeout = null;
        _clearRelaySyncState();
        _syncSessionReady();
        break;

      case 'activity':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.ActivityData.fromJson(msg.data!);
          _setAgentActivity(data.activity);
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'user_message':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.MessageData.fromJson(msg.data!);
          final displayText =
              data.displayText.isNotEmpty ? data.displayText : data.text;
          final remoteMessageId = msg.eventId ??
              'remote-user-${DateTime.now().millisecondsSinceEpoch}';
          if (data.kind == 'cron') {
            chatNotifier.addRemoteSystemMessage(
              displayText.isNotEmpty ? displayText : '⏰ Cron job triggered',
              messageId: remoteMessageId,
              kind: data.kind,
            );
          } else if (displayText.isNotEmpty) {
            final absorbed = chatNotifier.bindRemoteUserMessage(
              data.text,
              remoteMessageId: remoteMessageId,
              localMessageId: data.messageId,
            );
            if (!absorbed) {
              chatNotifier.addRemoteUserMessage(
                displayText,
                messageId: remoteMessageId,
                kind: data.kind,
              );
            }
          }
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'system_message':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.MessageData.fromJson(msg.data!);
          final displayText =
              data.displayText.isNotEmpty ? data.displayText : data.text;
          if (displayText.isNotEmpty) {
            chatNotifier.addRemoteSystemMessage(
              displayText,
              messageId: msg.eventId ??
                  'remote-system-${DateTime.now().millisecondsSinceEpoch}',
              kind: data.kind,
            );
          }
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'text':
      case 'stream_text':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.TextData.fromJson(msg.data!);
          final text = data.chunk.isNotEmpty
              ? data.chunk
              : (msg.data!['text'] as String? ?? '');
          final msgId = data.id.isNotEmpty
              ? data.id
              : 'msg-${DateTime.now().millisecondsSinceEpoch}';
          chatNotifier.handleTextChunk(proto.TextData(
            id: msgId,
            chunk: text,
            done: data.done,
            kind: data.kind,
          ));
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'reasoning':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.TextData.fromJson(msg.data!);
          final reasoningId = data.id.isNotEmpty
              ? data.id
              : 'reasoning-${DateTime.now().millisecondsSinceEpoch}';
          chatNotifier.handleReasoningChunk(
            proto.TextData(
              id: reasoningId,
              chunk: data.chunk,
              done: data.done,
            ),
          );
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'stream_start':
        break;

      case 'stream_end':
      case 'text_done':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        final msgId = msg.data?['id'] as String? ?? msg.streamId;
        if (msgId != null && msgId.isNotEmpty) {
          chatNotifier.finalizeStreaming(msgId);
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'reasoning_done':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        final msgId = msg.data?['id'] as String? ?? msg.streamId;
        if (msgId != null && msgId.isNotEmpty) {
          chatNotifier.finalizeReasoning(msgId);
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'status':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.StatusData.fromJson(msg.data!);
          final normalized = _normalizeAgentStatus(data.status);
          _setAgentStatus(normalized, '');
          if (normalized == 'idle') {
            _setAgentActivity('');
            _runDeferredGapRecoveryIfIdle();
          } else if (data.message.isNotEmpty) {
            // Backward compatibility with older tunnel senders that packed the
            // transient activity text into status.message.
            _setAgentActivity(data.message);
          }
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'tool_call':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          chatNotifier.handleToolCall(
            proto.ToolCallData.fromJson(msg.data!),
            messageId: msg.eventId ??
                msg.streamId ??
                'tool-${DateTime.now().millisecondsSinceEpoch}',
          );
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'tool_result':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          chatNotifier
              .handleToolResult(proto.ToolResultData.fromJson(msg.data!));
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'approval_request':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.ApprovalRequestData.fromJson(msg.data!);
          ref.read(approvalProvider.notifier).set(ApprovalInfo(
              id: data.id, toolName: data.toolName, input: data.input));
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'approval_result':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.ApprovalResultData.fromJson(msg.data!);
          final approval = ref.read(approvalProvider);
          if (approval != null && approval.id == data.id) {
            ref.read(approvalProvider.notifier).set(null);
          }
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'ask_user_request':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.AskUserRequestData.fromJson(msg.data!);
          // Build a human-readable summary of the questions
          final detail = data.questions.map(describeAskUserQuestion).join('\n');
          final amsgId = msg.eventId ?? newAskUserMessageId();
          chatNotifier.addAskUserRequest(amsgId, data.title, detail);
          ref.read(askUserProvider.notifier).set(AskUserInfo(
              id: data.id,
              title: data.title,
              questions: data.questions,
              msgId: amsgId));
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'ask_user_response':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.AskUserResponseData.fromJson(msg.data!);
          final askUser = ref.read(askUserProvider);
          if (askUser != null && askUser.id == data.id) {
            if (askUser.msgId.isNotEmpty) {
              chatNotifier.updateAskUserAnswer(
                askUser.msgId,
                summarizeAskUserResponse(
                    askUser.questions, data.answers, data.status),
              );
            }
            ref.read(askUserProvider.notifier).set(null);
          }
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'subagent_spawn':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.SubagentSpawnData.fromJson(msg.data!);
          _upsertSubagent(
            agentId: data.agentId,
            name: data.name,
            task: data.task,
            color: data.color,
            parentId: data.parentId,
            status: 'running',
          );
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'subagent_text':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.SubagentTextData.fromJson(msg.data!);
          _upsertSubagent(
            agentId: data.agentId,
            status: 'running',
          );
          chatNotifier.handleSubagentText(data);
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'subagent_reasoning':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.SubagentReasoningData.fromJson(msg.data!);
          _upsertSubagent(
            agentId: data.agentId,
            status: 'thinking',
          );
          chatNotifier.handleSubagentReasoning(data);
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'subagent_reasoning_done':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.SubagentReasoningData.fromJson(msg.data!);
          final reasoningId = '${data.agentId}-${data.id}';
          chatNotifier.finalizeReasoning(reasoningId);
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'subagent_status':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.SubagentStatusData.fromJson(msg.data!);
          _upsertSubagent(
            agentId: data.agentId,
            status: data.status,
          );
        }
        _markEventApplied(msg);
        _markProjectionAuthoritative();
        break;

      case 'subagent_complete':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.SubagentCompleteData.fromJson(msg.data!);
          chatNotifier.finalizePendingReasoning(
            sourceId: data.agentId,
            collapse: true,
          );
          chatNotifier.finalizeStreamingMessagesForSource(data.agentId);
          _upsertSubagent(
            agentId: data.agentId,
            name: data.name,
            status: 'completed',
            completed: true,
            success: data.success,
            summary: data.summary,
          );
          _scheduleSubagentCleanup(
            data.agentId,
            generation: _connectionGeneration,
          );
        }
        _markEventApplied(msg);
        break;

      case 'subagent_tool_call':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.SubagentToolCallData.fromJson(msg.data!);
          final agent = _upsertSubagent(
            agentId: data.agentId,
            status: 'running',
          );
          final chatNotifier = ref.read(chatProvider.notifier);
          chatNotifier.addSubagentToolCall(
            messageId: msg.eventId ?? data.toolId,
            agentId: data.agentId,
            toolId: data.toolId,
            toolName: data.toolName,
            displayName: data.displayName,
            args: data.args,
            detail: data.detail,
            sourceName: agent.name,
            sourceColor: agent.color,
          );
        }
        _markEventApplied(msg);
        break;

      case 'subagent_tool_result':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final data = proto.SubagentToolResultData.fromJson(msg.data!);
          final agent = _upsertSubagent(agentId: data.agentId);
          final chatNotifier = ref.read(chatProvider.notifier);
          chatNotifier.updateSubagentToolResult(
            agentId: data.agentId,
            toolId: data.toolId,
            toolName: data.toolName,
            displayName: data.displayName,
            detail: data.detail,
            sourceName: agent.name,
            sourceColor: agent.color,
            result: data.result,
            summary: data.summary,
            payload: data.payload,
            payloadMode: data.payloadMode,
            isError: data.isError,
          );
        }
        _markEventApplied(msg);
        break;

      case 'error':
        if (!_shouldApplyEvent(msg)) {
          _ackSkippedEvent(msg);
          break;
        }
        if (msg.data != null) {
          final errMsg = msg.data!['message'] as String? ?? 'Unknown error';
          chatNotifier.addErrorMessage(
            errMsg,
            messageId:
                msg.eventId ?? 'error-${DateTime.now().millisecondsSinceEpoch}',
          );
        }
        _markEventApplied(msg);
        break;

      case 'server_offline':
        // Relay told us the room is temporarily offline. Keep live session
        // and selection intact — the connection will resume when host
        // rebuilds the room. Clearing liveSessionId would make all sessions
        // appear as "historical/offline" in the UI.
        final recoveryState = msg.data?['state'] as String? ?? '';
        _beginRelaySyncWaitingForHost(
          recoveryState: recoveryState,
          hasLocalState: _hasLocalSessionState(),
        );
        state = state.copyWith(
          status: ConnectionStatus.connecting,
          error: null,
          sessionReady: false,
        );
        break;

      case 'sharing_stopped':
        unawaited(_handlePermanentRoomFailure(
          state.url ?? '',
          'Sharing stopped',
          sourceService: service,
          generation: _connectionGeneration,
        ));
        break;
    }
  }

  void _clearUiProjection() {
    _cancelSubagentCleanupTimers();
    ref.read(chatProvider.notifier).clearMessages();
    ref.read(subagentProvider.notifier).clear();
    ref.read(approvalProvider.notifier).set(null);
    ref.read(askUserProvider.notifier).set(null);
    ref.read(sessionInfoProvider.notifier).set(null);
    ref.read(currentModeProvider.notifier).set('supervised');
    _setAgentStatus('idle', '');
    _setAgentActivity('');
    _hasAuthoritativeProjection = false;
    _pendingActiveSessionBarrierOrdinal = null;
    _pendingActiveSessionBarrierEventId = '';
    _gapRecoveryScheduled = false;
  }

  void _setAgentStatus(String status, String message) {
    ref.read(agentStatusProvider.notifier).set(_normalizeAgentStatus(status));
    if (message.isNotEmpty) {
      ref.read(agentStatusMessageProvider.notifier).set(message);
    }
  }

  void _setAgentActivity(String activity) {
    ref.read(agentStatusMessageProvider.notifier).set(activity);
  }

  String _normalizeAgentStatus(String status) {
    switch (status) {
      case 'busy':
      case 'running':
      case 'thinking':
      case 'waiting':
      case 'error':
        return 'busy';
      default:
        return 'idle';
    }
  }

  void _restoreCachedAgentStatus({String? workspaceKey, String? sessionId}) {
    final cacheState = ref.read(workspaceCacheProvider);
    final resolvedSessionId =
        sessionId ?? cacheState.selectedSessionId ?? cacheState.liveSessionId;
    if (resolvedSessionId == null || resolvedSessionId.isEmpty) {
      return;
    }
    final snapshot = ref
        .read(workspaceCacheProvider.notifier)
        .snapshotFor(resolvedSessionId);
    if (snapshot == null) return;
    _setAgentStatus(snapshot.agentStatus, '');
    _setAgentActivity(snapshot.agentStatusMessage);
  }

  Future<void> _saveUrl(String url) async {
    final prefs = await SharedPreferences.getInstance();
    final urls = prefs.getStringList('ggcode_history') ?? [];
    if (!urls.contains(url)) {
      urls.insert(0, url);
      if (urls.length > 10) urls.removeLast();
      prefs.setStringList('ggcode_history', urls);
    }
  }

  static Future<List<String>> loadHistory() async {
    final prefs = await SharedPreferences.getInstance();
    return prefs.getStringList('ggcode_history') ?? [];
  }

  Future<void> _handlePermanentRoomFailure(
    String failedUrl,
    String error, {
    required ConnectionService? sourceService,
    required int generation,
  }) async {
    final normalizedFailedUrl = normalizeTunnelUrl(failedUrl);
    final currentUrl = normalizeTunnelUrl(state.url ?? '');
    if (!_isConnectionGenerationCurrent(generation) ||
        normalizedFailedUrl.isEmpty ||
        currentUrl != normalizedFailedUrl ||
        sourceService == null ||
        !identical(service, sourceService)) {
      return;
    }
    _disposeActiveService();
    _clearUiProjection();
    _sessionId = '';
    _lastAppliedEventId = '';
    _lastDurableEventId = '';
    _resumeOverrideEventId = '';
    _relayAuthorityEpoch = 0;
    _awaitingSnapshotProjection = false;
    _snapshotProjectionTimeout?.cancel();
    _snapshotProjectionTimeout = null;
    _clearRelaySyncState();
    _recentEventIds.clear();
    _recentEventSet.clear();
    // Remove the failed connection from the store immediately
    final failedConnId = _connectionStore.all
        .where((c) => normalizeTunnelUrl(c.url) == normalizedFailedUrl)
        .map((c) => c.id)
        .firstOrNull;
    if (failedConnId != null) {
      await _connectionStore.markAndRemove(failedConnId, error);
    }
    final prefs = await SharedPreferences.getInstance();
    await _clearPersistedResumeState(prefs);
    await ref.read(workspaceCacheProvider.notifier).clearReconnectTarget(
          sessionId: _sessionId,
          workspaceKey: ref.read(workspaceCacheProvider).liveWorkspaceKey ??
              ref.read(workspaceCacheProvider).selectedWorkspaceKey,
        );
    if (!_isConnectionGenerationCurrent(generation) ||
        normalizeTunnelUrl(state.url ?? '') != normalizedFailedUrl) {
      return;
    }
    state = TunnelConnectionState(
      status: ConnectionStatus.disconnected,
      error: error,
    );
  }

  bool _acceptAuthorityEpoch(proto.WsMessage msg, {String? sessionId}) {
    final authorityEpoch = msg.authorityEpoch ?? 0;
    if (authorityEpoch <= 0) {
      return true;
    }
    if (_relayAuthorityEpoch > 0 && authorityEpoch < _relayAuthorityEpoch) {
      return false;
    }
    if (authorityEpoch > _relayAuthorityEpoch) {
      final nextSessionId = sessionId ?? msg.sessionId ?? _sessionId;
      _resetForAuthorityEpoch(nextSessionId, authorityEpoch);
    }
    return true;
  }

  void _noteAuthorityEpoch(proto.WsMessage msg) {
    final authorityEpoch = msg.authorityEpoch ?? 0;
    if (authorityEpoch > 0) {
      _relayAuthorityEpoch = authorityEpoch;
    }
  }

  void _resetForAuthorityEpoch(String sessionId, int authorityEpoch) {
    final previousSessionId = _sessionId;
    // Don't clear messages — just reset cursor and dedup.
    // Messages should never be cleared by authority epoch changes.
    _hasAuthoritativeProjection = false;
    _lastAppliedEventId = '';
    _lastDurableEventId = '';
    _resumeOverrideEventId = '';
    _recentEventIds.clear();
    _recentEventSet.clear();
    _awaitingSnapshotProjection = false;
    _snapshotProjectionTimeout?.cancel();
    _snapshotProjectionTimeout = null;
    _relayAuthorityEpoch = authorityEpoch;
    _sessionId = sessionId;
    if (sessionId.isNotEmpty) {
      unawaited(ref.read(workspaceCacheProvider.notifier).observeLiveSession(
            sessionId,
            previousSessionId: previousSessionId,
            sessionInfo: ref.read(sessionInfoProvider),
            authorityEpoch: authorityEpoch,
          ));
    }
  }

  bool _shouldApplyEvent(proto.WsMessage msg) {
    if (!_acceptAuthorityEpoch(msg, sessionId: msg.sessionId ?? _sessionId)) {
      return false;
    }
    final eventId = msg.eventId;
    final sessionId = msg.sessionId ?? _sessionId;
    // Events from a different session are ALWAYS dropped.
    // Session switching is handled exclusively by active_session messages.
    // This prevents background service events from hijacking the active session.
    if (sessionId.isNotEmpty &&
        _sessionId.isNotEmpty &&
        sessionId != _sessionId) {
      return false;
    }
    if (eventId == null || eventId.isEmpty) {
      return true;
    }
    // Dedup: exact match in recent window.
    if (_recentEventSet.contains(eventId)) {
      return false;
    }
    // Skip already-cached events: relay may replay events earlier than our
    // snapshot's lastEventId (ACK latency).  Skip + ACK so relay advances.
    // Exception: when awaiting a snapshot projection (after snapshot_reset),
    // the host is sending a complete fresh view — accept all events regardless
    // of ordinal.
    if (!_awaitingSnapshotProjection) {
      final ord = _parseEventOrdinal(eventId);
      final last = _parseEventOrdinal(_lastAppliedEventId);
      if (ord != null && last != null && ord <= last) {
        return false;
      }
      // Gap detection: when we see a gap in event ordinals, we still apply
      // the event (streaming text/reasoning chunks are incremental and safe to
      // append even with a gap).
      //
      // Small gaps (<=100): likely a network blip. Schedule reconnect recovery
      // to backfill missing events via resume_from.
      //
      // Large gaps (>100): the relay has already told us via resume_ack what
      // it can replay. Reconnecting would trigger a massive SQLite replay
      // from relay (e.g. 190k events = 500MB). Instead, accept the gap:
      // update our cursor so live streaming continues without looping.
      // The user may see missing history but won't lose the live stream.
      if (ord != null && last != null && ord > last + 1) {
        if (ord - last <= 100) {
          debugPrint(
            '[connection] ordinal gap detected last=$last incoming=$ord eventId=$eventId — scheduling recovery',
          );
          _scheduleGapRecovery(msg,
              lastOrdinal: last, incomingOrdinal: ord);
          // Do NOT return false — we still apply the event.
        } else {
          debugPrint(
            '[connection] large ordinal gap ($last -> $ord, ${ord - last} events) — accepting gap, updating cursor to continue live stream',
          );
          // Update cursor to incoming event so we don't re-detect this gap
          // on every subsequent event. Live streaming continues.
          _lastAppliedEventId = eventId;
          _captureLiveProjectionForDurableResume();
          _persistResumeState();
          return true;
        }
      }
    }
    return true;
  }

  /// ACK a skipped (already-cached) event so relay can advance its cursor.
  void _ackSkippedEvent(proto.WsMessage msg) {
    final eventId = msg.eventId;
    _noteReplayProgress(eventId);
    // Incremental persistence: write every message to this session's cache
    // so switching sessions or restarting the app preserves all data.
    if (_sessionId.isNotEmpty && msg.type != 'server_ack') {
      ref.read(workspaceCacheProvider.notifier).appendSessionEvent(
            sessionId: _sessionId,
            eventType: msg.type,
            eventData: Map<String, dynamic>.from(msg.data ?? {}),
            eventId: eventId,
          );
    }

    if (eventId != null && eventId.isNotEmpty && _clientId.isNotEmpty) {
      service?.sendAck(clientId: _clientId, eventId: eventId);
    }
  }

  void _markEventApplied(proto.WsMessage msg) {
    final eventId = msg.eventId;
    if (msg.sessionId != null && msg.sessionId!.isNotEmpty) {
      _sessionId = msg.sessionId!;
    }
    if (eventId == null || eventId.isEmpty) {
      _persistResumeState();
      _captureLiveProjectionForDurableResume();
      return;
    }
    _noteReplayProgress(eventId);
    _lastAppliedEventId = eventId;
    _maybeCompleteActiveSessionBarrier();
    _recentEventSet.add(eventId);
    _recentEventIds.add(eventId);
    if (_recentEventIds.length > 1000) {
      final evicted = _recentEventIds.removeAt(0);
      _recentEventSet.remove(evicted);
    }
    _captureLiveProjectionForDurableResume();
    _persistResumeState();
  }

  void _maybeCompleteActiveSessionBarrier() {
    final target = _pendingActiveSessionBarrierOrdinal;
    if (target == null) return;
    final applied = _parseEventOrdinal(_lastAppliedEventId) ?? 0;
    if (applied < target) return;
    debugPrint(
      '[connection] active_session barrier reached target=$target event=$_pendingActiveSessionBarrierEventId',
    );
    _pendingActiveSessionBarrierOrdinal = null;
    _pendingActiveSessionBarrierEventId = '';
    _clearRelaySyncState();
  }

  void _scheduleGapRecovery(
    proto.WsMessage msg, {
    required int lastOrdinal,
    required int incomingOrdinal,
  }) {
    if (_gapRecoveryScheduled) return;
    _gapRecoveryAttemptCount++;
    final lastEvent = _lastAppliedEventId;
    debugPrint(
      '[connection] event gap detected last=$lastOrdinal incoming=$incomingOrdinal lastEvent=$lastEvent incomingEvent=${msg.eventId} attempt=$_gapRecoveryAttemptCount',
    );
    // After 2 attempts, give up on gap recovery. The relay has likely
    // compacted its history via snapshot — the missing events no longer
    // exist. Accept the gap and move on.
    if (_gapRecoveryAttemptCount > 2) {
      debugPrint(
        '[connection] giving up gap recovery after $_gapRecoveryAttemptCount attempts — accepting gap and resuming from incoming event',
      );
      _lastAppliedEventId = msg.eventId ?? _lastAppliedEventId;
      _gapRecoveryScheduled = false;
      _gapRecoveryAttemptCount = 0;
      _clearRelaySyncState();
      return;
    }
    _gapRecoveryScheduled = true;
    _beginRelaySyncWaiting(hasLocalState: _hasLocalSessionState());
    final status = _normalizeAgentStatus(ref.read(agentStatusProvider));
    if (status == 'busy') {
      _gapRecoveryDeferred = true;
      _resumeOverrideEventId = lastEvent;
      debugPrint('[connection] deferring gap recovery reconnect until idle');
      return;
    }
    Future<void>.microtask(() => _runScheduledGapRecovery(lastEvent));
  }

  void _runDeferredGapRecoveryIfIdle() {
    if (!_gapRecoveryDeferred || !_gapRecoveryScheduled) return;
    final status = _normalizeAgentStatus(ref.read(agentStatusProvider));
    if (status == 'busy') return;
    final lastEvent = _resumeOverrideEventId;
    _gapRecoveryDeferred = false;
    unawaited(_runScheduledGapRecovery(lastEvent));
  }

  Future<void> _runScheduledGapRecovery(String lastEvent) async {
    final svc = service;
    if (svc == null) {
      _gapRecoveryScheduled = false;
      _gapRecoveryDeferred = false;
      return;
    }
    try {
      _resumeOverrideEventId = lastEvent;
      // Re-arm resume_hello with the gap-recovery event ID so the service
      // sends resume_from (not resume_hello) after reconnecting.
      svc.armResumeHello(
        clientId: _clientId,
        sessionId: _sessionId.isNotEmpty ? _sessionId : null,
        lastEventId: lastEvent,
        messageType: 'resume_from',
      );
      // Reconnect the EXISTING service directly — do NOT call connect()
      // which would demote to background and create a ping-pong loop.
      _beginRelaySyncWaiting(hasLocalState: _hasLocalSessionState());
      await svc.connect();
    } finally {
      _gapRecoveryScheduled = false;
      _gapRecoveryDeferred = false;
    }
  }

  int? _parseEventOrdinal(String? eventId) {
    if (eventId == null || eventId.isEmpty) return null;
    final idx = eventId.lastIndexOf('-');
    final raw = idx >= 0 ? eventId.substring(idx + 1) : eventId;
    return int.tryParse(raw);
  }

  bool _canRestoreSessionProjection() {
    return !_awaitingSnapshotProjection &&
        !_hasAuthoritativeProjection &&
        ref.read(subagentProvider).isEmpty &&
        ref.read(sessionInfoProvider) == null;
  }

  bool _restoreProjectionFromCache({
    bool adoptCursor = true,
    bool seedCursorIfUnset = false,
    int? expectedAuthorityEpoch,
  }) {
    final cacheState = ref.read(workspaceCacheProvider);
    final sessionId = cacheState.selectedSessionId;
    if (sessionId == null || sessionId.isEmpty) {
      return false;
    }
    final snapshot =
        ref.read(workspaceCacheProvider.notifier).snapshotFor(sessionId);
    if (snapshot == null) {
      return false;
    }
    final authorityEpoch = expectedAuthorityEpoch ?? _relayAuthorityEpoch;
    if (authorityEpoch > 0 && snapshot.authorityEpoch != authorityEpoch) {
      return false;
    }
    final sparseSnapshot = _isSparseResumeSnapshot(snapshot);
    if (!adoptCursor && seedCursorIfUnset && sparseSnapshot) {
      return false;
    }
    ref.read(chatProvider.notifier).set(historicalSnapshotMessages(snapshot));
    ref.read(subagentProvider.notifier).set(
          historicalSnapshotSubagents(snapshot),
        );
    ref.read(sessionInfoProvider.notifier).set(snapshot.sessionInfo);
    if (snapshot.sessionInfo != null && snapshot.sessionInfo!.mode.isNotEmpty) {
      ref.read(currentModeProvider.notifier).set(snapshot.sessionInfo!.mode);
    }
    _setAgentStatus(snapshot.agentStatus, '');
    _setAgentActivity(snapshot.agentStatusMessage);
    final snapshotCursor = snapshot.lastEventId;
    final canSeedCursor = !sparseSnapshot;
    if (adoptCursor && canSeedCursor) {
      _sessionId = sessionId;
      _lastAppliedEventId = snapshotCursor;
      _lastDurableEventId = snapshotCursor;
    } else if (seedCursorIfUnset &&
        canSeedCursor &&
        _lastAppliedEventId.isEmpty &&
        snapshotCursor.isNotEmpty) {
      if (_sessionId.isEmpty || _sessionId == sessionId) {
        _sessionId = sessionId;
        _lastAppliedEventId = snapshotCursor;
        _lastDurableEventId = snapshotCursor;
      }
    }
    if (snapshot.authorityEpoch > 0) {
      _relayAuthorityEpoch = snapshot.authorityEpoch;
    }
    return true;
  }

  void _bindService(
      ConnectionService localService, int generation, String connectUrl) {
    _cancelServiceSubscriptions();
    _serviceSubscriptions.addAll([
      localService.statusStream.listen((status) {
        if (!_isActiveConnection(localService, generation)) return;
        debugPrint(
          '[connection] provider status=$status generation=$generation '
          'session=$_sessionId lastEvent=$_lastAppliedEventId',
        );
        state = state.copyWith(
          status: status,
          sessionReady:
              status == ConnectionStatus.connected && state.sessionReady,
        );
        if (status == ConnectionStatus.connected) {
          // Mark connection as alive for app-restart recovery
          debugPrint('[connection] markAlive check: _currentConnection=${_currentConnection?.id} session=${_currentConnection?.sessionId}');
          if (_currentConnection != null) {
            unawaited(_connectionStore.markAlive(_currentConnection!.id));
          }
          _saveUrl(connectUrl);  // full URL with auth, not publicUrl
          localService.sendResumeHello(
            clientId: _clientId,
            sessionId: _sessionId.isNotEmpty ? _sessionId : null,
            lastEventId: _resumeOverrideEventId.isNotEmpty
                ? _resumeOverrideEventId
                : (_lastDurableEventId.isNotEmpty
                    ? _lastDurableEventId
                    : null),
            messageType: (_resumeOverrideEventId.isNotEmpty ||
                    _lastDurableEventId.isNotEmpty)
                ? 'resume_from'
                : 'resume_hello',
          );
        } else if (status == ConnectionStatus.disconnected) {
          _clearRelaySyncState();
        }
        _syncSessionReady();
      }),
      localService.errorStream.listen((error) {
        if (!_isActiveConnection(localService, generation)) return;
        if (isPermanentRoomFailureMessage(error)) {
          unawaited(_handlePermanentRoomFailure(
            connectUrl,
            error,
            sourceService: localService,
            generation: generation,
          ));
          return;
        }
        state = state.copyWith(error: error);
      }),
      localService.messageStream.listen((msg) {
        if (!_isActiveConnection(localService, generation)) return;
        _dispatchMessage(msg);
      }),
      localService.ackStream.listen((ack) {
        if (!_isActiveConnection(localService, generation)) return;
        final chatNotifier = ref.read(chatProvider.notifier);
        switch (ack.type) {
          case 'relay_ack':
            chatNotifier.updateMessageStatus(
                ack.messageId, MessageStatus.delivered);
            break;
          case 'server_ack':
            chatNotifier.updateMessageStatus(
                ack.messageId, MessageStatus.acknowledged);
            break;
        }
      }),
      localService.metadataStream.listen((metadata) {
        if (!_isActiveConnection(localService, generation)) return;
        if (metadata.renewToken.isNotEmpty) {
          // Update stored URL with renew_token so reconnection uses
          // the renewable URL instead of the short-lived auth_ticket URL.
          final newUrl = localService.descriptor.runtimeUrl();
          // Verify the renew_token belongs to the same room as _currentConnection
          final newDesc = ShareConnectionDescriptor.parse(newUrl);
          if (_currentConnection != null && newDesc.roomId != _currentConnection!.roomId) {
            debugPrint('[connection] metadata: renew_token room=${newDesc.roomId} != stored room=${_currentConnection!.roomId}, skipping update');
            return;
          }
          _liveUrl = newUrl;
          if (_currentConnection != null) {
            // Persist workspacePath + sessionId for restore matching.
            // NOTE: displayName is NOT set from sessionInfo — it's always
            // derived from workspaceKey to avoid cross-contamination.
            final sessionInfo = ref.read(sessionInfoProvider);
            _currentConnection = _currentConnection!.copyWith(
              url: newUrl,
              workspacePath: sessionInfo?.workspace.isNotEmpty == true
                  ? sessionInfo!.workspace
                  : _currentConnection!.workspacePath,
              sessionId: _sessionId.isNotEmpty
                  ? _sessionId
                  : _currentConnection!.sessionId,
            );
            _connectionStore.update(_currentConnection!.id, _currentConnection!);
          }
          _persistResumeState();
        }
        if (metadata.notice.isNotEmpty) {
          debugPrint('[connection] relay notice: ${metadata.notice}');
        }
      }),
    ]);
  }

  void _restoreSessionProjectionIfAvailable(String sessionId) {
    if (sessionId.isEmpty || !_canRestoreSessionProjection()) {
      log('[tunnel] _restoreSessionProjectionIfAvailable: SKIP session=$sessionId canRestore=${_canRestoreSessionProjection()} hasAuth=$_hasAuthoritativeProjection subs=${ref.read(subagentProvider).isEmpty} info=${ref.read(sessionInfoProvider)}');
      return;
    }
    log('[tunnel] _restoreSessionProjectionIfAvailable: restoring session=$sessionId');
    final generation = _connectionGeneration;
    unawaited(
      ref
          .read(workspaceCacheProvider.notifier)
          .attachSessionToActiveWorkspace(sessionId)
          .then((restored) {
        if (!_isConnectionGenerationCurrent(generation)) return;
        log('[tunnel] attachSession returned: restored=$restored canRestore=${_canRestoreSessionProjection()} lastEvent=$_lastAppliedEventId');
        if (restored && _canRestoreSessionProjection()) {
          _restoreProjectionFromCache(
            adoptCursor: false,
            seedCursorIfUnset: true,
            expectedAuthorityEpoch: _relayAuthorityEpoch,
          );
        }
      }),
    );
  }

  void _markProjectionAuthoritative() {
    _hasAuthoritativeProjection = true;
  }

  bool _isSparseResumeSnapshot(CachedSessionSnapshot snapshot) {
    return snapshot.sessionInfo == null &&
        snapshot.lastEventId.isNotEmpty &&
        snapshot.messages.length <= 8;
  }

  void _captureLiveProjectionForDurableResume() {
    unawaited(ref.read(workspaceCacheProvider.notifier).captureLiveProjection(
          messages: ref.read(chatProvider),
          subagents: ref.read(subagentProvider),
          sessionInfo: ref.read(sessionInfoProvider),
          agentStatus: ref.read(agentStatusProvider),
          agentStatusMessage: ref.read(agentStatusMessageProvider),
          lastEventId: _lastAppliedEventId,
          authorityEpoch: _relayAuthorityEpoch,
          authoritative: _sessionId.isNotEmpty && !_awaitingSnapshotProjection,
        ));
  }

  Future<void> _handleDurableSnapshotPersisted(
    String sessionId,
    String lastEventId,
  ) async {
    if (sessionId.isEmpty ||
        lastEventId.isEmpty ||
        (_sessionId.isNotEmpty && sessionId != _sessionId)) {
      return;
    }
    final durableOrdinal = _parseEventOrdinal(_lastDurableEventId);
    final nextOrdinal = _parseEventOrdinal(lastEventId);
    if (nextOrdinal == null ||
        (durableOrdinal != null && nextOrdinal <= durableOrdinal)) {
      return;
    }
    _lastDurableEventId = lastEventId;
    _resumeOverrideEventId = '';
    await _persistResumeStateNow();
    if (_clientId.isNotEmpty && _sessionId == sessionId) {
      service?.sendAck(clientId: _clientId, eventId: lastEventId);
    }
  }

  void handleIncomingForTest(proto.WsMessage msg) {
    _dispatchMessage(msg);
  }

  bool restoreProjectionFromCacheForTest({bool adoptCursor = true}) {
    return _restoreProjectionFromCache(adoptCursor: adoptCursor);
  }

  SubagentInfo _upsertSubagent({
    required String agentId,
    String? name,
    String? task,
    String? color,
    String? parentId,
    String? status,
    bool? completed,
    bool? success,
    String? summary,
  }) {
    final agents = Map<String, SubagentInfo>.from(ref.read(subagentProvider));
    final existing = agents[agentId];
    final next = SubagentInfo(
      agentId: agentId,
      name: (name != null && name.trim().isNotEmpty)
          ? name.trim()
          : existing?.name ?? agentId,
      task: (task != null && task.trim().isNotEmpty)
          ? task.trim()
          : existing?.task ?? '',
      color: (color != null && color.trim().isNotEmpty)
          ? color.trim()
          : existing?.color ?? '#4CAF50',
      parentId: (parentId != null && parentId.trim().isNotEmpty)
          ? parentId.trim()
          : existing?.parentId ?? '',
      status: (status != null && status.trim().isNotEmpty)
          ? status.trim()
          : existing?.status ?? 'running',
      completed: completed ?? existing?.completed ?? false,
      success: success ?? existing?.success ?? false,
      summary: (summary != null && summary.trim().isNotEmpty)
          ? summary.trim()
          : existing?.summary,
    );
    agents[agentId] = next;
    if (!next.completed) {
      _cancelSubagentCleanup(agentId);
    }
    ref.read(subagentProvider.notifier).set(agents);
    return next;
  }

  int _nextConnectionGeneration() => ++_connectionGeneration;

  bool _isConnectionGenerationCurrent(int generation) =>
      ref.mounted && generation == _connectionGeneration;

  bool _isActiveConnection(ConnectionService candidate, int generation) =>
      _isConnectionGenerationCurrent(generation) &&
      identical(service, candidate);

  void _cancelServiceSubscriptions() {
    for (final sub in _serviceSubscriptions) {
      unawaited(sub.cancel());
    }
    _serviceSubscriptions.clear();
  }

  void _disposeActiveService() {
    _cancelServiceSubscriptions();
    final current = service;
    service = null;
    current?.dispose();
  }

  /// Move the current active service to background instead of disposing it.
  /// The WebSocket stays alive; events are routed to cache silently via
  /// [BackgroundConnectionManager].
  void demoteToBackground() {
    if (service == null) return;
    final url = _liveUrl;
    final sessionId = _sessionId;
    // Sync current chat messages to snapshot before demoting.
    // Foreground updates chatProvider (in-memory) but doesn't call
    // appendSessionEvent. Without this sync, the snapshot is incomplete,
    // causing empty bubbles when the session is promoted back.
    if (sessionId.isNotEmpty) {
      final currentMessages = ref.read(chatProvider);
      if (currentMessages.isNotEmpty) {
        ref.read(workspaceCacheProvider.notifier).replaceSessionMessages(
          sessionId: sessionId,
          messages: currentMessages,
        );
      }
    }
    _cancelServiceSubscriptions();
    if (url.isNotEmpty && sessionId.isNotEmpty) {
      final bgConn = ref.read(backgroundConnectionProvider.notifier);
      bgConn.registerService(
        sessionId: sessionId,
        svc: service!,
        url: url,
      );
      debugPrint(
        '[connection] demoted to background url=$url session=$sessionId',
      );
    } else {
      service!.dispose();
    }
    service = null;
  }

  /// Promote a background service to foreground without reconnecting.
  /// The old foreground service is demoted to background (not disposed).
  /// Messages from cache are loaded into UI immediately.
  void adoptService(ConnectionService svc, String sessionId, String url) {
    // Demote current foreground service to background (not dispose!)
    demoteToBackground();

    service = svc;
    _sessionId = sessionId;
    _liveUrl = url;

    // Restore last known event ID from workspace cache for this session
    final cache = ref.read(workspaceCacheProvider);
    final snapshot = ref.read(workspaceCacheProvider.notifier).getSessionSnapshot(sessionId);
    String? sessionRecordKey;
    for (final entry in cache.sessions.entries) {
      if (entry.value.sessionId == sessionId) {
        sessionRecordKey = entry.key;
        break;
      }
    }
    final sessionRecord =
        sessionRecordKey != null ? cache.sessions[sessionRecordKey] : null;
    final lastEvent = snapshot?.lastEventId ??
        sessionRecord?.lastEventId ??
        '';
    _lastAppliedEventId = lastEvent;
    _lastDurableEventId = lastEvent;
    _gapRecoveryAttemptCount = 0;
    _gapRecoveryScheduled = false;
    _gapRecoveryDeferred = false;

    // Load cached messages into chat provider — the snapshot was being
    // incrementally updated by background events, so it's current.
    if (snapshot != null && snapshot.messages.isNotEmpty) {
      ref.read(chatProvider.notifier).loadCachedMessages(snapshot.messages);
      debugPrint(
        '[connection] loaded ${snapshot.messages.length} cached messages for session=$sessionId',
      );
    } else {
      ref.read(chatProvider.notifier).clearMessages();
      debugPrint('[connection] no snapshot for session=$sessionId, cleared chat');
    }

    // Set cursor so only events AFTER the cached snapshot are dispatched.
    // This prevents re-processing events that are already in the snapshot.
    _lastAppliedEventId = lastEvent;
    _lastDurableEventId = lastEvent;

    // Request incremental replay from host for any events after our cursor.
    // This is cheap — host only sends events we haven't seen yet.
    try {
      svc.sendResumeHello(
        clientId: _clientId,
        sessionId: sessionId,
        lastEventId: lastEvent.isEmpty ? null : lastEvent,
      );
      debugPrint('[connection] adoptService: incremental replay for session=$sessionId lastEvent=$lastEvent');
    } catch (e) {
      debugPrint('[connection] adoptService: sendResumeHello failed: $e');
    }

    // Session was already connected in background — mark ready immediately.
    state = state.copyWith(
      status: ConnectionStatus.connected,
      url: url,
      error: null,
      sessionReady: true,
    );
    _bindService(svc, _connectionGeneration, url);

    // Tell workspace cache we selected this session and it's now live
    final cacheNotifier = ref.read(workspaceCacheProvider.notifier);
    cacheNotifier.selectSession(sessionId);
    cacheNotifier.setLive(sessionId);

    debugPrint(
      '[connection] adopted background service session=$sessionId url=$url lastEvent=$lastEvent',
    );
  }

  void _beginLocalRestoreSync() {
    _setRelaySyncState(const RelaySyncState(
      phase: RelaySyncPhase.restoringLocal,
      hasLocalState: true,
    ));
  }

  void _beginRelaySyncWaitingForHost({
    required String recoveryState,
    required bool hasLocalState,
  }) {
    _pendingReplayCount = 0;
    _pendingResumeMode = '';
    _setRelaySyncState(RelaySyncState(
      phase: RelaySyncPhase.waitingHost,
      recoveryState: recoveryState,
      hasLocalState: hasLocalState,
    ));
  }

  void _beginRelaySyncWaiting({required bool hasLocalState}) {
    _pendingReplayCount = 0;
    _pendingResumeMode = '';
    _setRelaySyncState(
      RelaySyncState(
        phase: RelaySyncPhase.waiting,
        hasLocalState: hasLocalState,
      ),
    );
  }

  void _beginResumeReplaySync({
    required int replayCount,
    required String resumeMode,
    required bool hasLocalState,
  }) {
    if (replayCount <= 0) {
      _clearRelaySyncState();
      return;
    }
    _pendingReplayCount = replayCount;
    _pendingResumeMode = resumeMode;
    _setRelaySyncState(
      RelaySyncState(
        phase: RelaySyncPhase.replaying,
        remainingReplayCount: replayCount,
        resumeMode: resumeMode,
        hasLocalState: hasLocalState,
      ),
    );
  }

  void _beginSnapshotSync() {
    _pendingReplayCount = 0;
    _pendingResumeMode = '';
    _setRelaySyncState(const RelaySyncState(phase: RelaySyncPhase.snapshot));
  }

  void _noteReplayProgress(String? eventId) {
    if (_pendingReplayCount <= 0 || eventId == null || eventId.isEmpty) {
      return;
    }
    _pendingReplayCount--;
    if (_pendingReplayCount <= 0) {
      _clearRelaySyncState();
      return;
    }
    final current = state.relaySync;
    _setRelaySyncState(
      RelaySyncState(
        phase: RelaySyncPhase.replaying,
        remainingReplayCount: _pendingReplayCount,
        resumeMode: _pendingResumeMode,
        hasLocalState: current?.hasLocalState ?? false,
        stalled: current?.stalled ?? false,
      ),
    );
  }

  bool _hasLocalSessionState() {
    return _sessionId.isNotEmpty ||
        ref.read(sessionInfoProvider) != null ||
        ref.read(chatProvider).isNotEmpty;
  }

  void _syncSessionReady() {
    final ready = state.status == ConnectionStatus.connected &&
        !_awaitingSnapshotProjection &&
        _pendingActiveSessionBarrierOrdinal == null &&
        state.relaySync == null;
    if (state.sessionReady != ready) {
      state = state.copyWith(sessionReady: ready);
    }
  }

  void _setRelaySyncState(RelaySyncState? relaySync) {
    _relaySyncTimeout?.cancel();
    debugPrint(
      '[connection] relaySync=${relaySync == null ? 'clear' : '${relaySync.phase}:${relaySync.remainingReplayCount}:${relaySync.resumeMode}:stalled=${relaySync.stalled}'}',
    );
    state = state.copyWith(relaySync: relaySync);
    _syncSessionReady();
    if (relaySync == null) {
      return;
    }
    _relaySyncTimeout = Timer(const Duration(seconds: 30), () {
      if (!ref.mounted || state.relaySync == null) return;
      state = state.copyWith(
        relaySync: state.relaySync!.copyWith(stalled: true),
      );
    });
  }

  void _clearRelaySyncState() {
    _pendingReplayCount = 0;
    _pendingResumeMode = '';
    _relaySyncTimeout?.cancel();
    _relaySyncTimeout = null;
    if (state.relaySync != null &&
        _pendingActiveSessionBarrierOrdinal == null) {
      state = state.copyWith(relaySync: null);
    }
    _syncSessionReady();
  }

  void _scheduleSubagentCleanup(String agentId, {required int generation}) {
    _cancelSubagentCleanup(agentId);
    _subagentCleanupTimers[agentId] = Timer(const Duration(seconds: 5), () {
      if (!_isConnectionGenerationCurrent(generation)) return;
      final current =
          Map<String, SubagentInfo>.from(ref.read(subagentProvider));
      current.remove(agentId);
      ref.read(subagentProvider.notifier).set(current);
      final msgs = ref.read(chatProvider);
      ref
          .read(chatProvider.notifier)
          .set(msgs.where((m) => m.sourceId != agentId).toList());
      _subagentCleanupTimers.remove(agentId);
    });
  }

  void _cancelSubagentCleanup(String agentId) {
    _subagentCleanupTimers.remove(agentId)?.cancel();
  }

  void _cancelSubagentCleanupTimers() {
    for (final timer in _subagentCleanupTimers.values) {
      timer.cancel();
    }
    _subagentCleanupTimers.clear();
  }

  void _disposeNotifier() {
    _nextConnectionGeneration();
    _cancelSubagentCleanupTimers();
    _pendingReplayCount = 0;
    _pendingResumeMode = '';
    _relaySyncTimeout?.cancel();
    _relaySyncTimeout = null;
    _snapshotProjectionTimeout?.cancel();
    _snapshotProjectionTimeout = null;
    _disposeActiveService();
  }

  void _persistResumeState() {
    // Update _currentConnection synchronously so reconnects read fresh state.
    if (_currentConnection != null) {
      _currentConnection = _currentConnection!.copyWith(
        sessionId: _sessionId,
        lastEventId: _lastDurableEventId.isNotEmpty
            ? _lastDurableEventId
            : _lastAppliedEventId,
        lastConnectedAt: DateTime.now(),
        alive: true, // connection is alive if we're persisting resume state
      );
      unawaited(_connectionStore.update(
          _currentConnection!.id, _currentConnection!));
    }
  }

  Future<void> _persistResumeStateNow() async {
    if (_currentConnection != null) {
      _currentConnection = _currentConnection!.copyWith(
        sessionId: _sessionId,
        lastEventId: _lastDurableEventId.isNotEmpty
            ? _lastDurableEventId
            : _lastAppliedEventId,
        lastConnectedAt: DateTime.now(),
      );
      await _connectionStore.update(
          _currentConnection!.id, _currentConnection!);
    }
  }

  Future<void> _clearPersistedResumeState([SharedPreferences? prefs]) async {
    // Resume state is now per-connection in ConnectionStore.
  }

  /// Construct a SessionInfoData from the workspace metadata embedded in
  /// active_session by the relay (workspace_path, provider_name, model_name).
  /// Returns null if no workspace info is available.
  proto.SessionInfoData? _sessionInfoFromActiveSession(Map<String, dynamic>? data) {
    if (data == null) return null;
    // Relay sends workspace info in a nested "data" field:
    // {"authority_epoch": 1, "data": {"workspace_path": "...", ...}}
    final inner = data['data'] is Map<String, dynamic>
        ? data['data'] as Map<String, dynamic>
        : data['data'] is Map
            ? Map<String, dynamic>.from(data['data'] as Map)
            : null;
    final source = inner ?? data;
    final wsPath = source['workspace_path'] as String? ?? '';
    final prov = source['provider_name'] as String? ?? '';
    final mdl = source['model_name'] as String? ?? '';
    if (wsPath.isEmpty && prov.isEmpty && mdl.isEmpty) return null;
    return proto.SessionInfoData(
      workspace: wsPath,
      provider: prov,
      model: mdl,
      mode: '',
      version: '',
      language: '',
      theme: '',
    );
  }
}

// ---- Message delivery status for ack tracking ----
