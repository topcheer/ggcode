import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:sqlite3/sqlite3.dart';

import '../models/protocol.dart' as proto;
import 'chat_provider.dart';
import 'connection_provider.dart';
import 'ui_providers.dart';

class WorkspaceRecord {
  final String key;
  final String url;
  final String displayName;
  final String lastSessionId;
  final DateTime lastOpenedAt;

  const WorkspaceRecord({
    required this.key,
    required this.url,
    required this.displayName,
    required this.lastSessionId,
    required this.lastOpenedAt,
  });

  WorkspaceRecord copyWith({
    String? url,
    String? displayName,
    String? lastSessionId,
    DateTime? lastOpenedAt,
  }) =>
      WorkspaceRecord(
        key: key,
        url: url ?? this.url,
        displayName: displayName ?? this.displayName,
        lastSessionId: lastSessionId ?? this.lastSessionId,
        lastOpenedAt: lastOpenedAt ?? this.lastOpenedAt,
      );

  Map<String, dynamic> toJson() => {
        'key': key,
        'url': url,
        'display_name': displayName,
        'last_session_id': lastSessionId,
        'last_opened_at': lastOpenedAt.toIso8601String(),
      };

  factory WorkspaceRecord.fromJson(Map<String, dynamic> json) =>
      WorkspaceRecord(
        key: json['key'] as String? ?? '',
        url: json['url'] as String? ?? '',
        displayName: json['display_name'] as String? ?? '',
        lastSessionId: json['last_session_id'] as String? ?? '',
        lastOpenedAt:
            DateTime.tryParse(json['last_opened_at'] as String? ?? '') ??
                DateTime.fromMillisecondsSinceEpoch(0),
      );
}

class CachedSessionRecord {
  final String workspaceKey;
  final String sessionId;
  final String title;
  final String model;
  final String provider;
  final String mode;
  final String version;
  final String lastEventId;
  final DateTime lastUpdatedAt;

  const CachedSessionRecord({
    required this.workspaceKey,
    required this.sessionId,
    required this.title,
    required this.model,
    required this.provider,
    required this.mode,
    required this.version,
    required this.lastEventId,
    required this.lastUpdatedAt,
  });

  CachedSessionRecord copyWith({
    String? title,
    String? model,
    String? provider,
    String? mode,
    String? version,
    String? lastEventId,
    DateTime? lastUpdatedAt,
  }) =>
      CachedSessionRecord(
        workspaceKey: workspaceKey,
        sessionId: sessionId,
        title: title ?? this.title,
        model: model ?? this.model,
        provider: provider ?? this.provider,
        mode: mode ?? this.mode,
        version: version ?? this.version,
        lastEventId: lastEventId ?? this.lastEventId,
        lastUpdatedAt: lastUpdatedAt ?? this.lastUpdatedAt,
      );

  Map<String, dynamic> toJson() => {
        'workspace_key': workspaceKey,
        'session_id': sessionId,
        'title': title,
        'model': model,
        'provider': provider,
        'mode': mode,
        'version': version,
        'last_event_id': lastEventId,
        'last_updated_at': lastUpdatedAt.toIso8601String(),
      };

  factory CachedSessionRecord.fromJson(Map<String, dynamic> json) =>
      CachedSessionRecord(
        workspaceKey: json['workspace_key'] as String? ?? '',
        sessionId: json['session_id'] as String? ?? '',
        title: json['title'] as String? ?? '',
        model: json['model'] as String? ?? '',
        provider: json['provider'] as String? ?? '',
        mode: json['mode'] as String? ?? '',
        version: json['version'] as String? ?? '',
        lastEventId: json['last_event_id'] as String? ?? '',
        lastUpdatedAt:
            DateTime.tryParse(json['last_updated_at'] as String? ?? '') ??
                DateTime.fromMillisecondsSinceEpoch(0),
      );
}

class CachedSessionSnapshot {
  final List<ChatMessage> messages;
  final Map<String, SubagentInfo> subagents;
  final proto.SessionInfoData? sessionInfo;
  final String agentStatus;
  final String agentStatusMessage;
  final String lastEventId;

  const CachedSessionSnapshot({
    required this.messages,
    required this.subagents,
    required this.sessionInfo,
    this.agentStatus = 'idle',
    this.agentStatusMessage = '',
    this.lastEventId = '',
  });

  Map<String, dynamic> toJson() => {
        'messages': messages.map((m) => m.toJson()).toList(),
        'subagents': subagents.map((k, v) => MapEntry(k, v.toJson())),
        'session_info': _sessionInfoToJson(sessionInfo),
        'agent_status': _normalizedCachedAgentStatus(agentStatus),
        'agent_status_message': agentStatusMessage,
        'last_event_id': lastEventId,
      };

  factory CachedSessionSnapshot.fromJson(Map<String, dynamic> json) {
    final rawSubagents = json['subagents'] as Map<String, dynamic>? ?? {};
    return CachedSessionSnapshot(
      messages: (json['messages'] as List<dynamic>? ?? const [])
          .map((m) => ChatMessage.fromJson(Map<String, dynamic>.from(m)))
          .toList(),
      subagents: rawSubagents.map((key, value) => MapEntry(
            key,
            SubagentInfo.fromJson(Map<String, dynamic>.from(value)),
          )),
      sessionInfo: _sessionInfoFromJson(
        json['session_info'] is Map<String, dynamic>
            ? Map<String, dynamic>.from(json['session_info'])
            : null,
      ),
      agentStatus: _normalizedCachedAgentStatus(
        json['agent_status'] as String? ?? 'idle',
      ),
      agentStatusMessage: json['agent_status_message'] as String? ?? '',
      lastEventId: json['last_event_id'] as String? ?? '',
    );
  }
}

class WorkspaceCacheState {
  final bool initialized;
  final Map<String, WorkspaceRecord> workspaces;
  final Map<String, CachedSessionRecord> sessions;
  final Map<String, CachedSessionSnapshot> snapshots;
  final String? selectedWorkspaceKey;
  final String? selectedSessionId;
  final String? liveWorkspaceKey;
  final String? liveSessionId;

  const WorkspaceCacheState({
    required this.initialized,
    required this.workspaces,
    required this.sessions,
    required this.snapshots,
    this.selectedWorkspaceKey,
    this.selectedSessionId,
    this.liveWorkspaceKey,
    this.liveSessionId,
  });

  WorkspaceCacheState copyWith({
    bool? initialized,
    Map<String, WorkspaceRecord>? workspaces,
    Map<String, CachedSessionRecord>? sessions,
    Map<String, CachedSessionSnapshot>? snapshots,
    Object? selectedWorkspaceKey = _workspaceCacheSentinel,
    Object? selectedSessionId = _workspaceCacheSentinel,
    Object? liveWorkspaceKey = _workspaceCacheSentinel,
    Object? liveSessionId = _workspaceCacheSentinel,
  }) =>
      WorkspaceCacheState(
        initialized: initialized ?? this.initialized,
        workspaces: workspaces ?? this.workspaces,
        sessions: sessions ?? this.sessions,
        snapshots: snapshots ?? this.snapshots,
        selectedWorkspaceKey:
            identical(selectedWorkspaceKey, _workspaceCacheSentinel)
                ? this.selectedWorkspaceKey
                : selectedWorkspaceKey as String?,
        selectedSessionId: identical(selectedSessionId, _workspaceCacheSentinel)
            ? this.selectedSessionId
            : selectedSessionId as String?,
        liveWorkspaceKey: identical(liveWorkspaceKey, _workspaceCacheSentinel)
            ? this.liveWorkspaceKey
            : liveWorkspaceKey as String?,
        liveSessionId: identical(liveSessionId, _workspaceCacheSentinel)
            ? this.liveSessionId
            : liveSessionId as String?,
      );
}

const _workspaceCacheSentinel = Object();
const _workspaceCacheIndexKey = 'ggcode_workspace_cache_v1';
const _workspaceCacheIndexSelectedWorkspaceUrlKey = 'selected_workspace_url';
const _workspaceSnapshotPrefix = 'ggcode_workspace_snapshot_v1_';
const _workspaceCacheStorageSchemaVersion = 1;
const _workspaceCacheProjectionVersion = 2;
String? debugWorkspaceCacheDatabasePathOverride;

class _SnapshotWrite {
  const _SnapshotWrite({
    required this.workspaceKey,
    required this.sessionId,
    required this.snapshot,
  });

  final String workspaceKey;
  final String sessionId;
  final CachedSessionSnapshot snapshot;
}

class _WorkspaceCacheSqlStore {
  _WorkspaceCacheSqlStore._(this._db);

  final Database _db;

  static Future<_WorkspaceCacheSqlStore> open() async {
    final path = await _resolveDatabasePath();
    final parent = Directory(p.dirname(path));
    if (!parent.existsSync()) {
      parent.createSync(recursive: true);
    }
    final db = sqlite3.open(path);
    final store = _WorkspaceCacheSqlStore._(db);
    store._initialize();
    return store;
  }

  static Future<String> _resolveDatabasePath() async {
    if (debugWorkspaceCacheDatabasePathOverride != null &&
        debugWorkspaceCacheDatabasePathOverride!.isNotEmpty) {
      return debugWorkspaceCacheDatabasePathOverride!;
    }
    try {
      final dir = await getApplicationSupportDirectory();
      return p.join(dir.path, 'workspace_cache_v2.sqlite');
    } catch (_) {
      return p.join(
        Directory.systemTemp.path,
        'ggcode_mobile_workspace_cache_v2.sqlite',
      );
    }
  }

  void _initialize() {
    _db.execute('''
      CREATE TABLE IF NOT EXISTS cache_meta (
        key TEXT PRIMARY KEY,
        value TEXT NOT NULL
      );

      CREATE TABLE IF NOT EXISTS cache_workspaces (
        key TEXT PRIMARY KEY,
        url TEXT NOT NULL,
        display_name TEXT NOT NULL,
        last_session_id TEXT NOT NULL,
        last_opened_at TEXT NOT NULL
      );

      CREATE TABLE IF NOT EXISTS cache_sessions (
        workspace_key TEXT NOT NULL,
        session_id TEXT NOT NULL,
        title TEXT NOT NULL,
        model TEXT NOT NULL,
        provider TEXT NOT NULL,
        mode TEXT NOT NULL,
        version TEXT NOT NULL,
        last_event_id TEXT NOT NULL,
        last_updated_at TEXT NOT NULL,
        PRIMARY KEY (workspace_key, session_id)
      );

      CREATE TABLE IF NOT EXISTS cache_snapshots (
        workspace_key TEXT NOT NULL,
        session_id TEXT NOT NULL,
        payload_json TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        PRIMARY KEY (workspace_key, session_id)
      );
    ''');
    _ensureVersion(
      key: 'storage_schema_version',
      expected: '$_workspaceCacheStorageSchemaVersion',
      resetStore: false,
    );
    _ensureVersion(
      key: 'projection_version',
      expected: '$_workspaceCacheProjectionVersion',
      resetStore: true,
    );
  }

  void _ensureVersion({
    required String key,
    required String expected,
    required bool resetStore,
  }) {
    final current = _metaValue(key);
    if (current == expected) {
      return;
    }
    if (resetStore) {
      clearAll();
    }
    _db.execute(
      'INSERT INTO cache_meta(key, value) VALUES(?, ?) '
      'ON CONFLICT(key) DO UPDATE SET value = excluded.value',
      [key, expected],
    );
  }

  String? _metaValue(String key) {
    final result = _db.select(
      'SELECT value FROM cache_meta WHERE key = ?',
      [key],
    );
    if (result.isEmpty) {
      return null;
    }
    return result.first['value'] as String?;
  }

  bool get isEmpty {
    final rows = _db.select('SELECT COUNT(*) AS count FROM cache_sessions');
    final count = rows.first['count'];
    if (count is int) {
      return count == 0;
    }
    if (count is BigInt) {
      return count == BigInt.zero;
    }
    return true;
  }

  void clearAll() {
    _db.execute('DELETE FROM cache_snapshots');
    _db.execute('DELETE FROM cache_sessions');
    _db.execute('DELETE FROM cache_workspaces');
  }

  List<WorkspaceRecord> loadWorkspaces() {
    final rows = _db.select('''
      SELECT key, url, display_name, last_session_id, last_opened_at
      FROM cache_workspaces
      ORDER BY last_opened_at DESC
    ''');
    return rows
        .map(
          (row) => WorkspaceRecord(
            key: row['key'] as String? ?? '',
            url: row['url'] as String? ?? '',
            displayName: row['display_name'] as String? ?? '',
            lastSessionId: row['last_session_id'] as String? ?? '',
            lastOpenedAt: DateTime.tryParse(
                  row['last_opened_at'] as String? ?? '',
                ) ??
                DateTime.fromMillisecondsSinceEpoch(0),
          ),
        )
        .toList();
  }

  List<CachedSessionRecord> loadSessions() {
    final rows = _db.select('''
      SELECT workspace_key, session_id, title, model, provider, mode, version,
             last_event_id, last_updated_at
      FROM cache_sessions
      ORDER BY last_updated_at DESC
    ''');
    return rows
        .map(
          (row) => CachedSessionRecord(
            workspaceKey: row['workspace_key'] as String? ?? '',
            sessionId: row['session_id'] as String? ?? '',
            title: row['title'] as String? ?? '',
            model: row['model'] as String? ?? '',
            provider: row['provider'] as String? ?? '',
            mode: row['mode'] as String? ?? '',
            version: row['version'] as String? ?? '',
            lastEventId: row['last_event_id'] as String? ?? '',
            lastUpdatedAt: DateTime.tryParse(
                  row['last_updated_at'] as String? ?? '',
                ) ??
                DateTime.fromMillisecondsSinceEpoch(0),
          ),
        )
        .toList();
  }

  CachedSessionSnapshot? loadSnapshot(String workspaceKey, String sessionId) {
    final rows = _db.select(
      '''
      SELECT payload_json
      FROM cache_snapshots
      WHERE workspace_key = ? AND session_id = ?
      ''',
      [workspaceKey, sessionId],
    );
    if (rows.isEmpty) {
      return null;
    }
    final payload = rows.first['payload_json'] as String? ?? '';
    if (payload.isEmpty) {
      return null;
    }
    final json = _decodeJsonObjectOrEmpty(
      payload,
      'decode cached snapshot $workspaceKey/$sessionId',
    );
    if (json.isEmpty) {
      return null;
    }
    return CachedSessionSnapshot.fromJson(json);
  }

  void upsertWorkspace(WorkspaceRecord record) {
    _db.execute(
      '''
      INSERT INTO cache_workspaces(key, url, display_name, last_session_id, last_opened_at)
      VALUES(?, ?, ?, ?, ?)
      ON CONFLICT(key) DO UPDATE SET
        url = excluded.url,
        display_name = excluded.display_name,
        last_session_id = excluded.last_session_id,
        last_opened_at = excluded.last_opened_at
      ''',
      [
        record.key,
        record.url,
        record.displayName,
        record.lastSessionId,
        record.lastOpenedAt.toIso8601String(),
      ],
    );
  }

  void upsertSession(CachedSessionRecord record) {
    _db.execute(
      '''
      INSERT INTO cache_sessions(
        workspace_key, session_id, title, model, provider, mode, version,
        last_event_id, last_updated_at
      )
      VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
      ON CONFLICT(workspace_key, session_id) DO UPDATE SET
        title = excluded.title,
        model = excluded.model,
        provider = excluded.provider,
        mode = excluded.mode,
        version = excluded.version,
        last_event_id = excluded.last_event_id,
        last_updated_at = excluded.last_updated_at
      ''',
      [
        record.workspaceKey,
        record.sessionId,
        record.title,
        record.model,
        record.provider,
        record.mode,
        record.version,
        record.lastEventId,
        record.lastUpdatedAt.toIso8601String(),
      ],
    );
  }

  void upsertSnapshot(
    String workspaceKey,
    String sessionId,
    CachedSessionSnapshot snapshot,
  ) {
    _db.execute(
      '''
      INSERT INTO cache_snapshots(workspace_key, session_id, payload_json, updated_at)
      VALUES(?, ?, ?, ?)
      ON CONFLICT(workspace_key, session_id) DO UPDATE SET
        payload_json = excluded.payload_json,
        updated_at = excluded.updated_at
      ''',
      [
        workspaceKey,
        sessionId,
        jsonEncode(snapshot.toJson()),
        DateTime.now().toIso8601String(),
      ],
    );
  }

  void writeBatch({
    required List<WorkspaceRecord> workspaces,
    required List<CachedSessionRecord> sessions,
    required List<_SnapshotWrite> snapshots,
  }) {
    if (workspaces.isEmpty && sessions.isEmpty && snapshots.isEmpty) {
      return;
    }
    _db.execute('BEGIN IMMEDIATE');
    try {
      for (final workspace in workspaces) {
        upsertWorkspace(workspace);
      }
      for (final session in sessions) {
        upsertSession(session);
      }
      for (final snapshot in snapshots) {
        upsertSnapshot(
          snapshot.workspaceKey,
          snapshot.sessionId,
          snapshot.snapshot,
        );
      }
      _db.execute('COMMIT');
    } catch (_) {
      _db.execute('ROLLBACK');
      rethrow;
    }
  }

  Future<void> importLegacyPreferences(SharedPreferences prefs) async {
    if (!isEmpty) {
      return;
    }
    final raw = prefs.getString(_workspaceCacheIndexKey);
    final json = _decodeJsonObjectOrEmpty(raw, 'decode legacy workspace cache');
    if (json.isEmpty) {
      return;
    }
    for (final item in json['workspaces'] as List<dynamic>? ?? const []) {
      final record = WorkspaceRecord.fromJson(Map<String, dynamic>.from(item));
      if (record.key.isEmpty) {
        continue;
      }
      upsertWorkspace(record);
    }
    for (final item in json['sessions'] as List<dynamic>? ?? const []) {
      final record =
          CachedSessionRecord.fromJson(Map<String, dynamic>.from(item));
      if (record.workspaceKey.isEmpty || record.sessionId.isEmpty) {
        continue;
      }
      upsertSession(record);
      final sessionKey =
          _sessionCacheKey(record.workspaceKey, record.sessionId);
      final snapshotRaw = prefs.getString(_snapshotStorageKey(sessionKey));
      if (snapshotRaw == null || snapshotRaw.isEmpty) {
        continue;
      }
      final snapshotJson = _decodeJsonObjectOrEmpty(
        snapshotRaw,
        'decode legacy workspace snapshot $sessionKey',
      );
      if (snapshotJson.isEmpty) {
        continue;
      }
      upsertSnapshot(
        record.workspaceKey,
        record.sessionId,
        CachedSessionSnapshot.fromJson(snapshotJson),
      );
    }
  }

  void dispose() {
    _db.dispose();
  }
}

final workspaceCacheProvider =
    NotifierProvider<WorkspaceCacheNotifier, WorkspaceCacheState>(
  WorkspaceCacheNotifier.new,
);

class WorkspaceCacheNotifier extends Notifier<WorkspaceCacheState> {
  SharedPreferences? _prefs;
  _WorkspaceCacheSqlStore? _store;
  Future<void>? _initializeFuture;
  Timer? _flushTimer;
  final Set<String> _dirtySnapshots = <String>{};
  final Set<String> _dirtySessions = <String>{};
  final Set<String> _dirtyWorkspaces = <String>{};
  bool _selectionDirty = false;

  @override
  WorkspaceCacheState build() {
    ref.onDispose(() {
      _flushTimer?.cancel();
      _flushTimer = null;
      _store?.dispose();
      _store = null;
    });
    return const WorkspaceCacheState(
      initialized: false,
      workspaces: {},
      sessions: {},
      snapshots: {},
    );
  }

  Future<void> initialize() async {
    if (state.initialized) return;
    final inFlight = _initializeFuture;
    if (inFlight != null) {
      await inFlight;
      return;
    }
    final future = _initializeImpl();
    _initializeFuture = future;
    try {
      await future;
    } finally {
      if (identical(_initializeFuture, future)) {
        _initializeFuture = null;
      }
    }
  }

  Future<void> _initializeImpl() async {
    _prefs ??= await SharedPreferences.getInstance();
    if (!ref.mounted) return;
    final index = _decodeJsonObjectOrEmpty(
      _prefs!.getString(_workspaceCacheIndexKey),
      'decode workspace cache index',
    );
    final workspaces = <String, WorkspaceRecord>{};
    final sessions = <String, CachedSessionRecord>{};
    try {
      _store ??= await _WorkspaceCacheSqlStore.open();
      if (!ref.mounted) return;
      await _store!.importLegacyPreferences(_prefs!);
      if (!ref.mounted) return;
      for (final record in _store!.loadWorkspaces()) {
        if (record.key.isNotEmpty) {
          workspaces[record.key] = record;
        }
      }
      for (final record in _store!.loadSessions()) {
        if (record.workspaceKey.isNotEmpty && record.sessionId.isNotEmpty) {
          sessions[_sessionCacheKey(record.workspaceKey, record.sessionId)] =
              record;
        }
      }
    } catch (error, stackTrace) {
      _reportWorkspaceCacheError(
        'initialize workspace cache store',
        error,
        stackTrace,
      );
      _store?.dispose();
      _store = null;
    }
    final selectedWorkspaceKey = index['selected_workspace_key'] as String?;
    final selectedSessionId = index['selected_session_id'] as String?;
    final selectedWorkspaceUrl = normalizeTunnelUrl(
      index[_workspaceCacheIndexSelectedWorkspaceUrlKey] as String? ?? '',
    );
    if (selectedWorkspaceKey != null &&
        selectedWorkspaceKey.isNotEmpty &&
        selectedWorkspaceUrl.isNotEmpty) {
      final existing = workspaces[selectedWorkspaceKey];
      workspaces[selectedWorkspaceKey] = (existing ??
              WorkspaceRecord(
                key: selectedWorkspaceKey,
                url: selectedWorkspaceUrl,
                displayName: _workspaceDisplayName(selectedWorkspaceUrl, null),
                lastSessionId: selectedSessionId ?? '',
                lastOpenedAt: DateTime.fromMillisecondsSinceEpoch(0),
              ))
          .copyWith(
        url: selectedWorkspaceUrl,
        displayName: existing?.displayName.isNotEmpty == true
            ? existing!.displayName
            : _workspaceDisplayName(selectedWorkspaceUrl, null),
        lastSessionId: selectedSessionId ?? existing?.lastSessionId ?? '',
      );
    }
    if (!ref.mounted) return;
    state = WorkspaceCacheState(
      initialized: true,
      workspaces: workspaces,
      sessions: sessions,
      snapshots: {},
      selectedWorkspaceKey: selectedWorkspaceKey,
      selectedSessionId: selectedSessionId,
    );
  }

  String? urlForWorkspace(String workspaceKey) =>
      state.workspaces[workspaceKey]?.url;

  Future<void> activateWorkspaceUrl(String url) async {
    await initialize();
    if (!ref.mounted) return;
    final normalized = normalizeTunnelUrl(url);
    final key = _workspaceKeyForUrl(normalized);
    final now = DateTime.now();
    final current = state.workspaces[key];
    final sessions = sessionsForWorkspace(key);
    final selectedSessionId = current?.lastSessionId.isNotEmpty == true
        ? current!.lastSessionId
        : (sessions.isNotEmpty ? sessions.first.sessionId : null);
    final workspaces = Map<String, WorkspaceRecord>.from(state.workspaces)
      ..[key] = (current ??
              WorkspaceRecord(
                key: key,
                url: normalized,
                displayName: _workspaceDisplayName(normalized, null),
                lastSessionId: selectedSessionId ?? '',
                lastOpenedAt: now,
              ))
          .copyWith(url: normalized, lastOpenedAt: now);
    state = state.copyWith(
      workspaces: workspaces,
      selectedWorkspaceKey: key,
      selectedSessionId: selectedSessionId,
      liveWorkspaceKey: key,
      liveSessionId: state.liveWorkspaceKey == key ? state.liveSessionId : null,
    );
    _dirtyWorkspaces.add(key);
    _selectionDirty = true;
    _scheduleFlush();
  }

  Future<void> clearSelection() async {
    await initialize();
    if (!ref.mounted) return;
    state = state.copyWith(
      selectedWorkspaceKey: null,
      selectedSessionId: null,
      liveWorkspaceKey: null,
      liveSessionId: null,
    );
    _selectionDirty = true;
    _scheduleFlush();
  }

  void markDisconnected() {
    state = state.copyWith(liveWorkspaceKey: null, liveSessionId: null);
  }

  Future<bool> attachSessionToActiveWorkspace(String sessionId) async {
    if (sessionId.isEmpty) return false;
    await initialize();
    if (!ref.mounted) return false;
    final workspaceKey = state.liveWorkspaceKey ?? state.selectedWorkspaceKey;
    if (workspaceKey == null || workspaceKey.isEmpty) return false;

    final targetKey = _sessionCacheKey(workspaceKey, sessionId);
    await _ensureSnapshotLoaded(workspaceKey, sessionId);
    if (!ref.mounted) return false;
    final targetRecord = state.sessions[targetKey];
    final hasTargetSnapshot = state.snapshots.containsKey(targetKey);
    if (targetRecord == null) {
      return false;
    }
    if (state.selectedWorkspaceKey == workspaceKey &&
        (state.selectedSessionId == null || state.selectedSessionId!.isEmpty)) {
      state = state.copyWith(selectedSessionId: sessionId);
      await _persistIndex();
    }
    return hasTargetSnapshot;
  }

  Future<void> selectSession(String workspaceKey, String sessionId) async {
    await initialize();
    if (!ref.mounted) return;
    state = state.copyWith(
      selectedWorkspaceKey: workspaceKey,
      selectedSessionId: sessionId,
    );
    await _ensureSnapshotLoaded(workspaceKey, sessionId);
    if (!ref.mounted) return;
    _selectionDirty = true;
    _scheduleFlush();
  }

  Future<void> registerLiveSession(
    String sessionId,
    proto.SessionInfoData? sessionInfo, {
    String? lastEventId,
  }) async {
    if (sessionId.isEmpty) return;
    await initialize();
    if (!ref.mounted) return;
    final workspaceKey = state.liveWorkspaceKey ?? state.selectedWorkspaceKey;
    if (workspaceKey == null || workspaceKey.isEmpty) return;
    final now = DateTime.now();
    final previousLiveSessionId = state.liveSessionId;
    final lastKnownLiveSessionId =
        state.workspaces[workspaceKey]?.lastSessionId;
    final selectionFollowedLive = state.selectedWorkspaceKey == workspaceKey &&
        (state.selectedSessionId == null ||
            state.selectedSessionId!.isEmpty ||
            state.selectedSessionId == previousLiveSessionId ||
            (previousLiveSessionId == null &&
                state.selectedSessionId == lastKnownLiveSessionId));
    final sessionKey = _sessionCacheKey(workspaceKey, sessionId);
    final sessions = Map<String, CachedSessionRecord>.from(state.sessions)
      ..[sessionKey] = (state.sessions[sessionKey] ??
              CachedSessionRecord(
                workspaceKey: workspaceKey,
                sessionId: sessionId,
                title: _sessionTitle(sessionInfo, sessionId),
                model: sessionInfo?.model ?? '',
                provider: sessionInfo?.provider ?? '',
                mode: sessionInfo?.mode ?? '',
                version: sessionInfo?.version ?? '',
                lastEventId: lastEventId ?? '',
                lastUpdatedAt: now,
              ))
          .copyWith(
        title: _sessionTitle(sessionInfo, sessionId),
        model: sessionInfo?.model ?? state.sessions[sessionKey]?.model,
        provider: sessionInfo?.provider ?? state.sessions[sessionKey]?.provider,
        mode: sessionInfo?.mode ?? state.sessions[sessionKey]?.mode,
        version: sessionInfo?.version ?? state.sessions[sessionKey]?.version,
        lastEventId: lastEventId ?? state.sessions[sessionKey]?.lastEventId,
        lastUpdatedAt: now,
      );
    final workspace = state.workspaces[workspaceKey];
    final workspaces = Map<String, WorkspaceRecord>.from(state.workspaces);
    if (workspace != null) {
      workspaces[workspaceKey] = workspace.copyWith(
        displayName: _workspaceDisplayName(workspace.url, sessionInfo),
        lastSessionId: sessionId,
        lastOpenedAt: now,
      );
    }
    state = state.copyWith(
      workspaces: workspaces,
      sessions: sessions,
      liveWorkspaceKey: workspaceKey,
      liveSessionId: sessionId,
      selectedWorkspaceKey:
          selectionFollowedLive ? workspaceKey : state.selectedWorkspaceKey,
      selectedSessionId:
          selectionFollowedLive ? sessionId : state.selectedSessionId,
    );
    _dirtyWorkspaces.add(workspaceKey);
    _dirtySessions.add(sessionKey);
    if (selectionFollowedLive) {
      _selectionDirty = true;
    }
    _scheduleFlush();
    if (selectionFollowedLive) {
      await _ensureSnapshotLoaded(workspaceKey, sessionId);
    }
  }

  Future<void> observeLiveSession(
    String sessionId, {
    required String previousSessionId,
    proto.SessionInfoData? sessionInfo,
  }) async {
    await registerLiveSession(
      sessionId,
      sessionInfo,
      lastEventId: state
          .sessions[_sessionCacheKey(
              state.liveWorkspaceKey ?? state.selectedWorkspaceKey ?? '',
              sessionId)]
          ?.lastEventId,
    );
    if (previousSessionId.isEmpty || previousSessionId == sessionId) return;
  }

  Future<void> updateLiveCursor(String sessionId, String lastEventId) async {
    if (sessionId.isEmpty) return;
    await initialize();
    if (!ref.mounted) return;
    final workspaceKey = state.liveWorkspaceKey ?? state.selectedWorkspaceKey;
    if (workspaceKey == null || workspaceKey.isEmpty) return;
    final key = _sessionCacheKey(workspaceKey, sessionId);
    final record = state.sessions[key];
    if (record == null) return;
    final updated = Map<String, CachedSessionRecord>.from(state.sessions)
      ..[key] = record.copyWith(
        lastEventId: lastEventId,
        lastUpdatedAt: DateTime.now(),
      );
    state = state.copyWith(sessions: updated);
    _dirtySessions.add(key);
    _scheduleFlush();
  }

  Future<void> captureLiveProjection({
    required List<ChatMessage> messages,
    required Map<String, SubagentInfo> subagents,
    required proto.SessionInfoData? sessionInfo,
    required String agentStatus,
    required String agentStatusMessage,
    required String lastEventId,
  }) async {
    await initialize();
    if (!ref.mounted) return;
    final workspaceKey = state.liveWorkspaceKey;
    final sessionId = state.liveSessionId;
    if (workspaceKey == null ||
        workspaceKey.isEmpty ||
        sessionId == null ||
        sessionId.isEmpty) {
      return;
    }
    final snapshot = CachedSessionSnapshot(
      messages: List<ChatMessage>.from(messages),
      subagents: Map<String, SubagentInfo>.from(subagents),
      sessionInfo: sessionInfo,
      agentStatus: _normalizedCachedAgentStatus(agentStatus),
      agentStatusMessage: agentStatusMessage,
      lastEventId: lastEventId,
    );
    final snapshotKey = _sessionCacheKey(workspaceKey, sessionId);
    final snapshots = Map<String, CachedSessionSnapshot>.from(state.snapshots)
      ..[snapshotKey] = snapshot;
    final now = DateTime.now();
    final sessions = Map<String, CachedSessionRecord>.from(state.sessions)
      ..[snapshotKey] = (state.sessions[snapshotKey] ??
              CachedSessionRecord(
                workspaceKey: workspaceKey,
                sessionId: sessionId,
                title: _sessionTitle(sessionInfo, sessionId),
                model: sessionInfo?.model ?? '',
                provider: sessionInfo?.provider ?? '',
                mode: sessionInfo?.mode ?? '',
                version: sessionInfo?.version ?? '',
                lastEventId: lastEventId,
                lastUpdatedAt: now,
              ))
          .copyWith(
        title: _sessionTitle(sessionInfo, sessionId),
        model: sessionInfo?.model ?? state.sessions[snapshotKey]?.model,
        provider:
            sessionInfo?.provider ?? state.sessions[snapshotKey]?.provider,
        mode: sessionInfo?.mode ?? state.sessions[snapshotKey]?.mode,
        version: sessionInfo?.version ?? state.sessions[snapshotKey]?.version,
        lastEventId: lastEventId,
        lastUpdatedAt: now,
      );
    final workspaces = Map<String, WorkspaceRecord>.from(state.workspaces);
    final workspace = workspaces[workspaceKey];
    if (workspace != null) {
      workspaces[workspaceKey] = workspace.copyWith(
        displayName: _workspaceDisplayName(workspace.url, sessionInfo),
        lastSessionId: sessionId,
        lastOpenedAt: now,
      );
    }
    state = state.copyWith(
      workspaces: workspaces,
      sessions: sessions,
      snapshots: snapshots,
    );
    _dirtyWorkspaces.add(workspaceKey);
    _dirtySessions.add(snapshotKey);
    _dirtySnapshots.add(snapshotKey);
    _scheduleFlush();
  }

  Future<void> flushNow() async {
    _flushTimer?.cancel();
    _flushTimer = null;
    if (!ref.mounted) return;
    await _flushDirtyState();
    if (!ref.mounted) return;
    if (_selectionDirty) {
      await _persistIndex();
    }
  }

  List<WorkspaceRecord> sortedWorkspaces() {
    final items = state.workspaces.values.toList()
      ..sort((a, b) => b.lastOpenedAt.compareTo(a.lastOpenedAt));
    return items;
  }

  List<CachedSessionRecord> sessionsForWorkspace(String workspaceKey) {
    final items = state.sessions.values
        .where((record) => record.workspaceKey == workspaceKey)
        .toList()
      ..sort((a, b) => b.lastUpdatedAt.compareTo(a.lastUpdatedAt));
    return items;
  }

  CachedSessionSnapshot? snapshotFor(String workspaceKey, String sessionId) {
    final key = _sessionCacheKey(workspaceKey, sessionId);
    final cached = state.snapshots[key];
    if (cached != null) {
      return cached;
    }
    final snapshot = _store?.loadSnapshot(workspaceKey, sessionId);
    if (snapshot == null) {
      return null;
    }
    state = state.copyWith(
      snapshots: Map<String, CachedSessionSnapshot>.from(state.snapshots)
        ..[key] = snapshot,
    );
    return snapshot;
  }

  Future<void> _ensureSnapshotLoaded(
      String workspaceKey, String sessionId) async {
    await initialize();
    if (!ref.mounted) return;
    snapshotFor(workspaceKey, sessionId);
  }

  void _scheduleFlush() {
    _flushTimer?.cancel();
    _flushTimer = Timer(
      const Duration(milliseconds: 350),
      () => unawaited(flushNow()),
    );
  }

  Future<void> _flushDirtyState() async {
    if (_dirtySnapshots.isEmpty &&
        _dirtySessions.isEmpty &&
        _dirtyWorkspaces.isEmpty) {
      return;
    }
    await initialize();
    if (!ref.mounted) return;
    if (_store == null) {
      return;
    }
    final pendingWorkspaces = List<String>.from(_dirtyWorkspaces);
    final pendingWorkspaceRecords = <WorkspaceRecord>[];
    for (final key in pendingWorkspaces) {
      final workspace = state.workspaces[key];
      if (workspace == null) continue;
      pendingWorkspaceRecords.add(workspace);
    }
    final pendingSessions = List<String>.from(_dirtySessions);
    final pendingSessionRecords = <CachedSessionRecord>[];
    for (final key in pendingSessions) {
      final session = state.sessions[key];
      if (session == null) continue;
      pendingSessionRecords.add(session);
    }
    final pendingSnapshotKeys = List<String>.from(_dirtySnapshots);
    final pendingSnapshotWrites = <_SnapshotWrite>[];
    for (final key in pendingSnapshotKeys) {
      final snapshot = state.snapshots[key];
      if (snapshot == null) continue;
      final split = key.split('::');
      if (split.length != 2) continue;
      pendingSnapshotWrites.add(
        _SnapshotWrite(
          workspaceKey: split[0],
          sessionId: split[1],
          snapshot: snapshot,
        ),
      );
    }
    _dirtyWorkspaces.clear();
    _dirtySessions.clear();
    _dirtySnapshots.clear();
    try {
      _store!.writeBatch(
        workspaces: pendingWorkspaceRecords,
        sessions: pendingSessionRecords,
        snapshots: pendingSnapshotWrites,
      );
    } catch (error, stackTrace) {
      _dirtyWorkspaces.addAll(pendingWorkspaces);
      _dirtySessions.addAll(pendingSessions);
      _dirtySnapshots.addAll(pendingSnapshotKeys);
      _reportWorkspaceCacheError(
        'flush workspace cache',
        error,
        stackTrace,
      );
    }
  }

  Future<void> _persistIndex() async {
    _prefs ??= await SharedPreferences.getInstance();
    if (!ref.mounted) return;
    final selectedWorkspace = state.selectedWorkspaceKey == null
        ? null
        : state.workspaces[state.selectedWorkspaceKey!];
    final payload = {
      'selected_workspace_key': state.selectedWorkspaceKey,
      'selected_session_id': state.selectedSessionId,
      _workspaceCacheIndexSelectedWorkspaceUrlKey: selectedWorkspace?.url,
    };
    try {
      await _prefs!.setString(_workspaceCacheIndexKey, jsonEncode(payload));
      _selectionDirty = false;
    } catch (error, stackTrace) {
      _reportWorkspaceCacheError(
        'persist workspace cache index',
        error,
        stackTrace,
      );
    }
  }
}

final displayedMessagesProvider = Provider<List<ChatMessage>>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return ref.watch(chatProvider);
  }
  final workspaceKey = cache.selectedWorkspaceKey;
  final sessionId = cache.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return const [];
  }
  return ref
          .watch(workspaceCacheProvider.notifier)
          .snapshotFor(workspaceKey, sessionId)
          ?.messages ??
      const [];
});

final displayedSubagentProvider = Provider<Map<String, SubagentInfo>>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return ref.watch(subagentProvider);
  }
  final workspaceKey = cache.selectedWorkspaceKey;
  final sessionId = cache.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return const {};
  }
  return ref
          .watch(workspaceCacheProvider.notifier)
          .snapshotFor(workspaceKey, sessionId)
          ?.subagents ??
      const {};
});

final displayedSessionInfoProvider = Provider<proto.SessionInfoData?>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return ref.watch(sessionInfoProvider);
  }
  final workspaceKey = cache.selectedWorkspaceKey;
  final sessionId = cache.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return null;
  }
  return ref
      .watch(workspaceCacheProvider.notifier)
      .snapshotFor(workspaceKey, sessionId)
      ?.sessionInfo;
});

final displayedAgentStatusProvider = Provider<String>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return _normalizedCachedAgentStatus(ref.watch(agentStatusProvider));
  }
  final workspaceKey = cache.selectedWorkspaceKey;
  final sessionId = cache.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return 'idle';
  }
  return _normalizedCachedAgentStatus(ref
          .watch(workspaceCacheProvider.notifier)
          .snapshotFor(workspaceKey, sessionId)
          ?.agentStatus ??
      'idle');
});

final displayedAgentStatusMessageProvider = Provider<String>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return ref.watch(agentStatusMessageProvider);
  }
  final workspaceKey = cache.selectedWorkspaceKey;
  final sessionId = cache.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return '';
  }
  return ref
          .watch(workspaceCacheProvider.notifier)
          .snapshotFor(workspaceKey, sessionId)
          ?.agentStatusMessage ??
      '';
});

final isHistoricalViewProvider = Provider<bool>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  final selectedWorkspaceKey = cache.selectedWorkspaceKey;
  final selectedSessionId = cache.selectedSessionId;
  if (selectedWorkspaceKey == null ||
      selectedWorkspaceKey.isEmpty ||
      selectedSessionId == null ||
      selectedSessionId.isEmpty) {
    return false;
  }
  return !_isViewingLive(cache);
});

final canSendMessagesProvider = Provider<bool>((ref) {
  final conn = ref.watch(connectionProvider);
  return conn.status == ConnectionStatus.connected &&
      !ref.watch(isHistoricalViewProvider);
});

bool _isViewingLive(WorkspaceCacheState state) {
  final workspaceKey = state.selectedWorkspaceKey;
  final sessionId = state.selectedSessionId;
  if (workspaceKey == null ||
      workspaceKey.isEmpty ||
      sessionId == null ||
      sessionId.isEmpty) {
    return true;
  }
  return workspaceKey == state.liveWorkspaceKey &&
      sessionId == state.liveSessionId;
}

String _normalizedCachedAgentStatus(String status) {
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

String _workspaceKeyForUrl(String url) =>
    base64UrlEncode(utf8.encode(url)).replaceAll('=', '');

String _sessionCacheKey(String workspaceKey, String sessionId) =>
    '$workspaceKey::$sessionId';

String _snapshotStorageKey(String sessionKey) =>
    '$_workspaceSnapshotPrefix${base64UrlEncode(utf8.encode(sessionKey)).replaceAll('=', '')}';

Map<String, dynamic> _decodeJsonObjectOrEmpty(String? raw, String context) {
  if (raw == null || raw.isEmpty) {
    return const <String, dynamic>{};
  }
  try {
    final decoded = jsonDecode(raw);
    if (decoded is Map<String, dynamic>) {
      return decoded;
    }
    if (decoded is Map) {
      return Map<String, dynamic>.from(decoded);
    }
  } catch (error, stackTrace) {
    _reportWorkspaceCacheError(context, error, stackTrace);
  }
  return const <String, dynamic>{};
}

void _reportWorkspaceCacheError(
  String context,
  Object error,
  StackTrace stackTrace,
) {
  debugPrint('[workspace_cache] $context: $error');
  FlutterError.reportError(
    FlutterErrorDetails(
      exception: error,
      stack: stackTrace,
      library: 'ggcode_mobile.workspace_cache',
      context: ErrorDescription(context),
    ),
  );
}

Map<String, dynamic>? _sessionInfoToJson(proto.SessionInfoData? info) {
  if (info == null) return null;
  return {
    'workspace': info.workspace,
    'model': info.model,
    'provider': info.provider,
    'mode': info.mode,
    'version': info.version,
    'language': info.language,
    'theme': info.theme,
  };
}

proto.SessionInfoData? _sessionInfoFromJson(Map<String, dynamic>? json) {
  if (json == null) return null;
  return proto.SessionInfoData.fromJson(json);
}

String _workspaceDisplayName(String url, proto.SessionInfoData? sessionInfo) {
  final workspace = sessionInfo?.workspace ?? '';
  if (workspace.isNotEmpty) {
    final parts = workspace.split('/');
    return parts.isNotEmpty ? parts.last : workspace;
  }
  final uri = Uri.tryParse(url);
  return uri?.host.isNotEmpty == true ? uri!.host : 'Workspace';
}

String _sessionTitle(proto.SessionInfoData? sessionInfo, String sessionId) {
  final workspace = sessionInfo?.workspace ?? '';
  final label = workspace.isNotEmpty ? workspace.split('/').last : 'Session';
  final shortId = sessionId.length > 8 ? sessionId.substring(0, 8) : sessionId;
  return '$label · $shortId';
}
