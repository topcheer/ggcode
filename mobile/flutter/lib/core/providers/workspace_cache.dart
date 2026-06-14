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
  final String displayName;
  final String lastSessionId;
  final DateTime lastOpenedAt;

  const WorkspaceRecord({
    required this.key,
    required this.displayName,
    required this.lastSessionId,
    required this.lastOpenedAt,
  });

  WorkspaceRecord copyWith({
    String? displayName,
    String? lastSessionId,
    DateTime? lastOpenedAt,
  }) =>
      WorkspaceRecord(
        key: key,
        displayName: displayName ?? this.displayName,
        lastSessionId: lastSessionId ?? this.lastSessionId,
        lastOpenedAt: lastOpenedAt ?? this.lastOpenedAt,
      );

  Map<String, dynamic> toJson() => {
        'key': key,
        'display_name': displayName,
        'last_session_id': lastSessionId,
        'last_opened_at': lastOpenedAt.toIso8601String(),
      };

  factory WorkspaceRecord.fromJson(Map<String, dynamic> json) =>
      WorkspaceRecord(
        key: json['key'] as String? ?? '',
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
  final String workspacePath;
  final String lastEventId;
  final int authorityEpoch;
  final DateTime lastUpdatedAt;
  final String url; // relay URL with token for this specific session

  const CachedSessionRecord({
    required this.workspaceKey,
    required this.sessionId,
    required this.title,
    required this.model,
    required this.provider,
    required this.mode,
    required this.version,
    this.workspacePath = '',
    required this.lastEventId,
    this.authorityEpoch = 0,
    required this.lastUpdatedAt,
    this.url = '',
  });

  CachedSessionRecord copyWith({
    String? title,
    String? model,
    String? provider,
    String? mode,
    String? version,
    String? workspacePath,
    String? lastEventId,
    int? authorityEpoch,
    DateTime? lastUpdatedAt,
    String? url,
  }) =>
      CachedSessionRecord(
        workspaceKey: workspaceKey,
        sessionId: sessionId,
        title: title ?? this.title,
        model: model ?? this.model,
        provider: provider ?? this.provider,
        mode: mode ?? this.mode,
        version: version ?? this.version,
        workspacePath: workspacePath ?? this.workspacePath,
        lastEventId: lastEventId ?? this.lastEventId,
        authorityEpoch: authorityEpoch ?? this.authorityEpoch,
        lastUpdatedAt: lastUpdatedAt ?? this.lastUpdatedAt,
        url: url ?? this.url,
      );

  Map<String, dynamic> toJson() => {
        'workspace_key': workspaceKey,
        'session_id': sessionId,
        'title': title,
        'model': model,
        'provider': provider,
        'mode': mode,
        'version': version,
        'workspace_path': workspacePath,
        'last_event_id': lastEventId,
        'authority_epoch': authorityEpoch,
        'last_updated_at': lastUpdatedAt.toIso8601String(),
        'url': url,
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
        authorityEpoch: (json['authority_epoch'] as num?)?.toInt() ?? 0,
        lastUpdatedAt:
            DateTime.tryParse(json['last_updated_at'] as String? ?? '') ??
                DateTime.fromMillisecondsSinceEpoch(0),
        url: json['url'] as String? ?? '',
      );
}

class CachedSessionSnapshot {
  final List<ChatMessage> messages;
  final Map<String, SubagentInfo> subagents;
  final proto.SessionInfoData? sessionInfo;
  final String agentStatus;
  final String agentStatusMessage;
  final String lastEventId;
  final int authorityEpoch;

  const CachedSessionSnapshot({
    required this.messages,
    required this.subagents,
    required this.sessionInfo,
    this.agentStatus = 'idle',
    this.agentStatusMessage = '',
    this.lastEventId = '',
    this.authorityEpoch = 0,
  });

  Map<String, dynamic> toJson() => {
        'messages': messages.map((m) => m.toJson()).toList(),
        'subagents': subagents.map((k, v) => MapEntry(k, v.toJson())),
        'session_info': _sessionInfoToJson(sessionInfo),
        'agent_status': _normalizedCachedAgentStatus(agentStatus),
        'agent_status_message': agentStatusMessage,
        'last_event_id': lastEventId,
        'authority_epoch': authorityEpoch,
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
      authorityEpoch: (json['authority_epoch'] as num?)?.toInt() ?? 0,
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
const _workspaceCacheStorageSchemaVersion = 2;
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

typedef DurableSnapshotObserver = Future<void> Function(
  String sessionId,
  String lastEventId,
);

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
    } catch (error, stackTrace) {
      _reportWorkspaceCacheError(
        'resolve workspace cache database path',
        error,
        stackTrace,
      );
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
        workspace_path TEXT NOT NULL DEFAULT '',
        last_event_id TEXT NOT NULL,
        authority_epoch INTEGER NOT NULL DEFAULT 0,
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
    _migrateDropWorkspaceUrl();
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
    try {
      _db.execute(
        'ALTER TABLE cache_sessions ADD COLUMN authority_epoch INTEGER NOT NULL DEFAULT 0;',
      );
    } on SqliteException catch (err) {
      final message = err.message.toLowerCase();
      if (!message.contains('duplicate column name')) {
        rethrow;
      }
    }
    try {
      _db.execute(
        'ALTER TABLE cache_sessions ADD COLUMN workspace_path TEXT NOT NULL DEFAULT \'\';',
      );
    } on SqliteException catch (err) {
      final message = err.message.toLowerCase();
      if (!message.contains('duplicate column name')) {
        rethrow;
      }
    }
    try {
      _db.execute(
        'ALTER TABLE cache_sessions ADD COLUMN url TEXT NOT NULL DEFAULT \'\';',
      );
    } on SqliteException catch (err) {
      final message = err.message.toLowerCase();
      if (!message.contains('duplicate column name')) {
        rethrow;
      }
    }
  }

  /// Migrate cache_workspaces: drop the legacy `url` column (v1 → v2).
  /// SQLite doesn't support DROP COLUMN before 3.35.0, so we recreate the
  /// table.  This runs once when the storage schema version bumps from 1→2.
  void _migrateDropWorkspaceUrl() {
    final current = _metaValue('storage_schema_version');
    if (current != null && current != '1') return; // only migrate from v1
    _db.execute('ALTER TABLE cache_workspaces RENAME TO cache_workspaces_old;');
    _db.execute('''
      CREATE TABLE cache_workspaces (
        key TEXT PRIMARY KEY,
        display_name TEXT NOT NULL,
        last_session_id TEXT NOT NULL,
        last_opened_at TEXT NOT NULL
      );
    ''');
    _db.execute('''
      INSERT INTO cache_workspaces(key, display_name, last_session_id, last_opened_at)
        SELECT key, display_name, last_session_id, last_opened_at
        FROM cache_workspaces_old;
    ''');
    _db.execute('DROP TABLE cache_workspaces_old;');
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
      SELECT key, display_name, last_session_id, last_opened_at
      FROM cache_workspaces
      ORDER BY last_opened_at DESC
    ''');
    return rows
        .map(
          (row) => WorkspaceRecord(
            key: row['key'] as String? ?? '',
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
             workspace_path, last_event_id, authority_epoch, last_updated_at
      FROM cache_sessions
      ORDER BY last_updated_at DESC
    ''');
    final deduped = <String, CachedSessionRecord>{};
    for (final row in rows) {
      final record = CachedSessionRecord(
        workspaceKey: row['workspace_key'] as String? ?? '',
        sessionId: row['session_id'] as String? ?? '',
        title: row['title'] as String? ?? '',
        model: row['model'] as String? ?? '',
        provider: row['provider'] as String? ?? '',
        mode: row['mode'] as String? ?? '',
        version: row['version'] as String? ?? '',
        workspacePath: row['workspace_path'] as String? ?? '',
        lastEventId: row['last_event_id'] as String? ?? '',
        authorityEpoch: (row['authority_epoch'] as num?)?.toInt() ?? 0,
        lastUpdatedAt:
            DateTime.tryParse(row['last_updated_at'] as String? ?? '') ??
                DateTime.fromMillisecondsSinceEpoch(0),
      );
      if (record.sessionId.isEmpty || deduped.containsKey(record.sessionId)) {
        continue;
      }
      deduped[record.sessionId] = record;
    }
    return deduped.values.toList();
  }

  CachedSessionSnapshot? loadSnapshot(String sessionId) {
    final rows = _db.select(
      '''
      SELECT payload_json, updated_at
      FROM cache_snapshots
      WHERE session_id = ?
      ORDER BY updated_at DESC
      ''',
      [sessionId],
    );
    if (rows.isEmpty) {
      return null;
    }
    CachedSessionSnapshot? bestSnapshot;
    DateTime bestUpdatedAt = DateTime.fromMillisecondsSinceEpoch(0);
    for (final row in rows) {
      final payload = row['payload_json'] as String? ?? '';
      if (payload.isEmpty) {
        continue;
      }
      final json = _decodeJsonObjectOrEmpty(
        payload,
        'decode cached snapshot $sessionId',
      );
      if (json.isEmpty) {
        continue;
      }
      final snapshot = CachedSessionSnapshot.fromJson(json);
      final updatedAt = DateTime.tryParse(row['updated_at'] as String? ?? '') ??
          DateTime.fromMillisecondsSinceEpoch(0);
      if (bestSnapshot == null ||
          _isBetterStoredSnapshot(
            candidate: snapshot,
            candidateUpdatedAt: updatedAt,
            current: bestSnapshot,
            currentUpdatedAt: bestUpdatedAt,
          )) {
        bestSnapshot = snapshot;
        bestUpdatedAt = updatedAt;
      }
    }
    return bestSnapshot;
  }

  void upsertWorkspace(WorkspaceRecord record) {
    _db.execute(
      '''
      INSERT INTO cache_workspaces(key, display_name, last_session_id, last_opened_at)
      VALUES(?, ?, ?, ?)
      ON CONFLICT(key) DO UPDATE SET
        display_name = excluded.display_name,
        last_session_id = excluded.last_session_id,
        last_opened_at = excluded.last_opened_at
      ''',
      [
        record.key,
        record.displayName,
        record.lastSessionId,
        record.lastOpenedAt.toIso8601String(),
      ],
    );
  }

  void upsertSession(CachedSessionRecord record) {
    _db.execute(
      'DELETE FROM cache_sessions WHERE session_id = ?',
      [record.sessionId],
    );
    _db.execute(
      '''
      INSERT INTO cache_sessions(
        workspace_key, session_id, title, model, provider, mode, version,
        workspace_path, last_event_id, authority_epoch, last_updated_at, url
      )
      VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
      ON CONFLICT(workspace_key, session_id) DO UPDATE SET
        title = excluded.title,
        model = excluded.model,
        provider = excluded.provider,
        mode = excluded.mode,
        version = excluded.version,
        workspace_path = excluded.workspace_path,
        last_event_id = excluded.last_event_id,
        authority_epoch = excluded.authority_epoch,
        last_updated_at = excluded.last_updated_at,
        url = excluded.url
      ''',
      [
        record.workspaceKey,
        record.sessionId,
        record.title,
        record.model,
        record.provider,
        record.mode,
        record.version,
        record.workspacePath,
        record.lastEventId,
        record.authorityEpoch,
        record.lastUpdatedAt.toIso8601String(),
        record.url,
      ],
    );
  }

  void upsertSnapshot(
    String workspaceKey,
    String sessionId,
    CachedSessionSnapshot snapshot,
  ) {
    _db.execute(
      'DELETE FROM cache_snapshots WHERE session_id = ?',
      [sessionId],
    );
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
    } catch (error, stackTrace) {
      try {
        _db.execute('ROLLBACK');
      } catch (rollbackError, rollbackStackTrace) {
        _reportWorkspaceCacheError(
          'rollback workspace cache write batch',
          rollbackError,
          rollbackStackTrace,
        );
      }
      _reportWorkspaceCacheError(
        'write workspace cache batch',
        error,
        stackTrace,
      );
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
    _db.close();
  }

  bool _isBetterStoredSnapshot({
    required CachedSessionSnapshot candidate,
    required DateTime candidateUpdatedAt,
    required CachedSessionSnapshot current,
    required DateTime currentUpdatedAt,
  }) {
    final candidateScore = _storedSnapshotScore(candidate);
    final currentScore = _storedSnapshotScore(current);
    if (candidateScore != currentScore) {
      return candidateScore > currentScore;
    }

    final candidateCursor = _eventOrdinal(candidate.lastEventId);
    final currentCursor = _eventOrdinal(current.lastEventId);
    if (candidateCursor != currentCursor) {
      return candidateCursor > currentCursor;
    }

    return candidateUpdatedAt.isAfter(currentUpdatedAt);
  }

  int _storedSnapshotScore(CachedSessionSnapshot snapshot) {
    return (snapshot.messages.length * 100) +
        (snapshot.subagents.length * 10) +
        (snapshot.sessionInfo == null ? 0 : 1);
  }

  int _eventOrdinal(String eventId) {
    final match = RegExp(r'(\d+)$').firstMatch(eventId);
    if (match == null) {
      return -1;
    }
    return int.tryParse(match.group(1) ?? '') ?? -1;
  }
}

CachedSessionSnapshot _coalesceCachedSnapshotForPersistence({
  required CachedSessionSnapshot? existing,
  required CachedSessionSnapshot candidate,
}) {
  if (existing == null) {
    return candidate;
  }
  if (!_shouldPreserveExistingCachedSnapshot(
      existing: existing, candidate: candidate)) {
    return candidate;
  }
  return _mergeCachedSnapshots(existing: existing, candidate: candidate);
}

bool _shouldPreserveExistingCachedSnapshot({
  required CachedSessionSnapshot existing,
  required CachedSessionSnapshot candidate,
}) {
  if (existing.sessionInfo != null && candidate.sessionInfo == null) {
    return true;
  }
  if (existing.authorityEpoch != 0 &&
      candidate.authorityEpoch != 0 &&
      existing.authorityEpoch != candidate.authorityEpoch) {
    return false;
  }
  return _isSparseCachedSnapshot(candidate) &&
      _cachedSnapshotScore(existing) > _cachedSnapshotScore(candidate);
}

CachedSessionSnapshot _mergeCachedSnapshots({
  required CachedSessionSnapshot existing,
  required CachedSessionSnapshot candidate,
}) {
  final preserveRichProjection = _isSparseCachedSnapshot(candidate);
  final mergedMessages = <ChatMessage>[];
  final candidateById = <String, ChatMessage>{};
  final appendedAnonymous = <ChatMessage>[];
  for (final message in candidate.messages) {
    if (message.id.isEmpty) {
      appendedAnonymous.add(message);
      continue;
    }
    candidateById[message.id] = message;
  }
  final seenIds = <String>{};
  for (final message in existing.messages) {
    if (message.id.isNotEmpty) {
      final replacement = candidateById.remove(message.id);
      mergedMessages.add(replacement ?? message);
      seenIds.add(message.id);
      continue;
    }
    mergedMessages.add(message);
  }
  for (final message in candidate.messages) {
    if (message.id.isEmpty) {
      continue;
    }
    if (seenIds.add(message.id)) {
      mergedMessages.add(message);
    }
  }
  mergedMessages.addAll(appendedAnonymous);

  final mergedSubagents = Map<String, SubagentInfo>.from(existing.subagents)
    ..addAll(candidate.subagents);
  final existingCursor = _cachedEventOrdinal(existing.lastEventId);
  final candidateCursor = _cachedEventOrdinal(candidate.lastEventId);
  final mergedStatusMessage = candidate.agentStatusMessage.isNotEmpty
      ? candidate.agentStatusMessage
      : existing.agentStatusMessage;
  return CachedSessionSnapshot(
    messages: preserveRichProjection
        ? mergedMessages
        : List<ChatMessage>.from(candidate.messages),
    subagents: preserveRichProjection
        ? mergedSubagents
        : Map<String, SubagentInfo>.from(candidate.subagents),
    sessionInfo: candidate.sessionInfo ?? existing.sessionInfo,
    agentStatus: candidate.agentStatus,
    agentStatusMessage: mergedStatusMessage,
    lastEventId: candidateCursor >= existingCursor
        ? candidate.lastEventId
        : existing.lastEventId,
    authorityEpoch: candidate.authorityEpoch != 0
        ? candidate.authorityEpoch
        : existing.authorityEpoch,
  );
}

int _cachedSnapshotScore(CachedSessionSnapshot snapshot) {
  return (snapshot.messages.length * 100) +
      (snapshot.subagents.length * 10) +
      (snapshot.sessionInfo == null ? 0 : 1);
}

bool _isSparseCachedSnapshot(CachedSessionSnapshot snapshot) {
  return snapshot.sessionInfo == null &&
      snapshot.lastEventId.isNotEmpty &&
      snapshot.messages.length <= 8;
}

int _cachedEventOrdinal(String eventId) {
  final match = RegExp(r'(\d+)$').firstMatch(eventId);
  if (match == null) {
    return -1;
  }
  return int.tryParse(match.group(1) ?? '') ?? -1;
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
  DurableSnapshotObserver? onDurableSnapshotPersisted;
  final Set<String> _dirtySnapshots = <String>{};
  final Set<String> _dirtySessions = <String>{};
  final Set<String> _dirtyWorkspaces = <String>{};
  bool _selectionDirty = false;
  String? _pendingWorkspaceUrl;

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
          // If displayName is missing (e.g. legacy data), try to recover it
          // from the cached snapshot's sessionInfo.
          var updated = record;
          if (record.displayName.isEmpty && record.lastSessionId.isNotEmpty) {
            final snap = _store!.loadSnapshot(record.lastSessionId);
            final name = _workspaceDisplayName(snap?.sessionInfo);
            if (name.isNotEmpty) {
              updated = record.copyWith(displayName: name);
            }
          }
          workspaces[record.key] = updated;
        }
      }
      for (final record in _store!.loadSessions()) {
        if (record.sessionId.isNotEmpty) {
          sessions[record.sessionId] = record;
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

  /// Find a reconnectable URL for a workspace by looking at its sessions.
  /// Returns the URL of the most recently updated session that has a URL.
  String? urlForWorkspace(String workspaceKey) {
    final sessions = state.sessions.values
        .where((s) => s.workspaceKey == workspaceKey && s.url.isNotEmpty)
        .toList()
      ..sort((a, b) => b.lastUpdatedAt.compareTo(a.lastUpdatedAt));
    return sessions.isNotEmpty ? sessions.first.url : null;
  }

  /// Store the URL for [registerLiveSession] to use when session_info arrives.
  /// Must NOT be called from [registerLiveSession] as it is set before
  /// connecting.  No workspace or session state is touched here — all
  /// workspace registration happens inside [registerLiveSession] once the
  /// host sends session_info with the real workspace path.
  void setPendingUrl(String url) {
    final normalized = normalizeTunnelUrl(url);
    _pendingWorkspaceUrl = normalized;
  }

  CachedSessionRecord? sessionForId(String sessionId) =>
      state.sessions[sessionId];

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

  Future<void> clearReconnectTarget({
    String? sessionId,
    String? workspaceKey,
  }) async {
    await initialize();
    if (!ref.mounted) return;
    final targetSessionId = sessionId ?? state.selectedSessionId;
    final targetWorkspaceKey =
        workspaceKey ?? state.liveWorkspaceKey ?? state.selectedWorkspaceKey;
    final sessions = Map<String, CachedSessionRecord>.from(state.sessions);
    if (targetSessionId != null && targetSessionId.isNotEmpty) {
      final existing = sessions[targetSessionId];
      if (existing != null &&
          (targetWorkspaceKey == null ||
              targetWorkspaceKey.isEmpty ||
              existing.workspaceKey == targetWorkspaceKey)) {
        sessions[targetSessionId] = CachedSessionRecord(
          workspaceKey: '',
          sessionId: existing.sessionId,
          title: existing.title,
          model: existing.model,
          provider: existing.provider,
          mode: existing.mode,
          version: existing.version,
          lastEventId: existing.lastEventId,
          lastUpdatedAt: DateTime.now(),
        );
        _dirtySessions.add(targetSessionId);
      }
    }
    state = state.copyWith(
      sessions: sessions,
      selectedWorkspaceKey: null,
      liveWorkspaceKey: null,
      liveSessionId: null,
    );
    _selectionDirty = true;
    _scheduleFlush();
  }

  void markDisconnected() {
    debugPrint('[cache] markDisconnected: selected=${state.selectedSessionId} live=${state.liveSessionId} -> clearing live');
    state = state.copyWith(liveWorkspaceKey: null, liveSessionId: null);
  }

  /// Clear all selection state — used when user scans a NEW connection.
  /// This ensures registerLiveSession will followLive unconditionally.
  void clearAllSelection() {
    debugPrint('[cache] clearAllSelection: selected=${state.selectedSessionId} live=${state.liveSessionId}');
    state = state.copyWith(
      liveWorkspaceKey: null,
      liveSessionId: null,
      selectedSessionId: null,
      selectedWorkspaceKey: null,
    );
  }

  /// Clear all cached workspaces, sessions, and snapshots from device.
  Future<void> clearAll() async {
    await _initializeFuture;
    _store?.clearAll();
    _dirtySnapshots.clear();
    _dirtySessions.clear();
    _dirtyWorkspaces.clear();
    state = const WorkspaceCacheState(
      initialized: true,
      workspaces: {},
      sessions: {},
      snapshots: {},
    );
    _scheduleFlush();
  }

  /// Cache a background event from a non-active session relay connection.
  /// This persists the event for later viewing without updating the UI.
  void cacheBackgroundEvent({
    required String sessionId,
    required String eventType,
    required Map<String, dynamic> eventData,
  }) {
    // Find the session record to get its workspace key
    CachedSessionRecord? record;
    for (final r in state.sessions.values) {
      if (r.sessionId == sessionId) {
        record = r;
        break;
      }
    }
    if (record == null) return;

    // Update lastEventId if present
    final eventId = eventData['event_id'] as String? ?? '';
    if (eventId.isNotEmpty) {
      final updated = record.copyWith(
        lastEventId: eventId,
        lastUpdatedAt: DateTime.now(),
      );
      final sessions = Map<String, CachedSessionRecord>.from(state.sessions);
      final key = _sessionCacheKey(updated.workspaceKey, updated.sessionId);
      sessions[key] = updated;
      state = state.copyWith(sessions: sessions);
      _dirtySessions.add(key);
      _scheduleFlush();
    }
  }

  // ─── Per-session incremental message persistence ───
  // Used by both foreground and background connections to ensure
  // every message is persisted to the correct session immediately.

  /// Append a message event to a session's cached snapshot.
  /// Works for streaming text (merge), new messages (append),
  /// and control events (status update).
  void appendSessionEvent({
    required String sessionId,
    required String eventType,
    required Map<String, dynamic> eventData,
    String? eventId,
  }) {
    // Find snapshot key for this session
    String? snapshotKey;
    for (final entry in state.sessions.entries) {
      if (entry.value.sessionId == sessionId) {
        snapshotKey = entry.key;
        break;
      }
    }
    if (snapshotKey == null) return;

    final existing = state.snapshots[snapshotKey];

    List<ChatMessage> messages = List.from(existing?.messages ?? []);
    Map<String, SubagentInfo> subagents =
        Map<String, SubagentInfo>.from(existing?.subagents ?? {});
    String agentStatus = existing?.agentStatus ?? 'idle';
    String agentStatusMessage = existing?.agentStatusMessage ?? '';
    proto.SessionInfoData? sessionInfo = existing?.sessionInfo;

    switch (eventType) {
      case 'session_info':
        final si = proto.SessionInfoData.fromJson(eventData);
        if (si.workspace.isNotEmpty) sessionInfo = si;
        break;

      case 'text':
      case 'stream_text':
        final data = proto.TextData.fromJson(eventData);
        final msgId = data.id.isNotEmpty
            ? data.id
            : 'msg-${DateTime.now().millisecondsSinceEpoch}';
        final idx = messages.indexWhere(
            (m) => m.id == msgId || ((m.sourceId?.isEmpty ?? true) && m.streaming && !m.isUser));
        if (idx >= 0) {
          messages[idx] = ChatMessage(
            id: messages[idx].id,
            sourceId: messages[idx].sourceId,
            sourceName: messages[idx].sourceName,
            kind: messages[idx].kind,
            text: messages[idx].text + data.chunk,
            streaming: data.done ? false : messages[idx].streaming,
            time: messages[idx].time,
            status: messages[idx].status,
          );
        } else {
          messages.add(ChatMessage(
            id: msgId,
            text: data.chunk,
            streaming: !data.done,
            kind: data.kind,
            time: DateTime.now(),
          ));
        }
        break;

      case 'tool_call_done':
        final data = proto.ToolCallData.fromJson(eventData);
        final toolId = data.toolId.isNotEmpty
            ? data.toolId
            : 'tool-${DateTime.now().millisecondsSinceEpoch}';
        messages.add(ChatMessage(
          id: toolId,
          toolId: data.toolId,
          toolName: data.toolName,
          toolDisplayName: data.displayName,
          text: data.detail,
          streaming: true,
          time: DateTime.now(),
        ));
        break;

      case 'tool_result':
        final data = proto.ToolResultData.fromJson(eventData);
        final idx = data.toolId.isNotEmpty
            ? messages.indexWhere((m) => m.id == data.toolId)
            : -1;
        if (idx >= 0) {
          messages[idx] = ChatMessage(
            id: messages[idx].id,
            sourceId: messages[idx].sourceId,
            sourceName: messages[idx].sourceName,
            kind: messages[idx].kind,
            text: messages[idx].text,
            toolId: messages[idx].toolId,
            toolName: messages[idx].toolName,
            toolDisplayName: messages[idx].toolDisplayName,
            toolResult: data.result,
            toolCompleted: true,
            time: messages[idx].time,
            status: messages[idx].status,
          );
        } else {
          messages.add(ChatMessage(
            id: data.toolId.isNotEmpty
                ? data.toolId
                : 'tool-${DateTime.now().millisecondsSinceEpoch}',
            toolId: data.toolId,
            toolResult: data.result,
            toolCompleted: true,
            text: '',
            time: DateTime.now(),
          ));
        }
        break;

      case 'user_message':
        final text = eventData['text'] as String? ?? '';
        final msgId = eventData['message_id'] as String? ??
            'user-${DateTime.now().millisecondsSinceEpoch}';
        messages.add(ChatMessage(
          id: msgId,
          isUser: true,
          text: text,
          time: DateTime.now(),
          status: MessageStatus.acknowledged,
        ));
        break;

      case 'agent_status':
        agentStatus = eventData['status'] as String? ?? agentStatus;
        agentStatusMessage =
            eventData['message'] as String? ?? agentStatusMessage;
        break;

      case 'done':
        messages = messages
            .map((m) => m.streaming
                ? ChatMessage(
                    id: m.id,
                    sourceId: m.sourceId,
                    sourceName: m.sourceName,
                    kind: m.kind,
                    text: m.text,
                    streaming: false,
                    isUser: m.isUser,
                    toolId: m.toolId,
                    toolName: m.toolName,
                    toolDisplayName: m.toolDisplayName,
                    toolDetail: m.toolDetail,
                    toolResult: m.toolResult,
                    toolPayload: m.toolPayload,
                    toolPayloadMode: m.toolPayloadMode,
                    toolCompleted: m.toolCompleted,
                    isToolError: m.isToolError,
                    reasoningCollapsed: m.reasoningCollapsed,
                    time: m.time,
                    status: m.status,
                  )
                : m)
            .toList();
        agentStatus = 'idle';
        agentStatusMessage = '';
        break;

      case 'subagent_text':
      case 'subagent_stream_text':
        final data = proto.SubagentTextData.fromJson(eventData);
        final agentId = data.agentId;
        subagents[agentId] ??=
            SubagentInfo(agentId: agentId, name: agentId, task: '');
        break;

      case 'subagent_tool_call_done':
        final agentId = eventData['agent_id'] as String? ?? '';
        if (agentId.isNotEmpty) {
          subagents[agentId] ??=
              SubagentInfo(agentId: agentId, name: agentId, task: '');
        }
        break;

      case 'subagent_done':
        final data = proto.SubagentCompleteData.fromJson(eventData);
        final agentId = data.agentId;
        final prev = subagents[agentId] ??
            SubagentInfo(agentId: agentId, name: data.name, task: '');
        subagents[agentId] = prev.copyWith(
          status: 'completed',
          completed: true,
          success: data.success,
        );
        break;

      case 'error':
        messages.add(ChatMessage(
          id: 'error-${DateTime.now().millisecondsSinceEpoch}',
          text: (eventData['error'] as String?) ?? 'Unknown error',
          time: DateTime.now(),
        ));
        agentStatus = 'idle';
        break;

      default:
        break;
    }

    // Build and store updated snapshot
    final newSnapshot = CachedSessionSnapshot(
      messages: messages,
      subagents: subagents,
      sessionInfo: sessionInfo,
      agentStatus: agentStatus,
      agentStatusMessage: agentStatusMessage,
      lastEventId: eventId ?? existing?.lastEventId ?? '',
      authorityEpoch: existing?.authorityEpoch ?? 0,
    );

    final newSnapshots = Map<String, CachedSessionSnapshot>.from(state.snapshots);
    newSnapshots[snapshotKey] = newSnapshot;
    state = state.copyWith(snapshots: newSnapshots);

    // Update session record's lastEventId
    if (eventId != null && eventId.isNotEmpty) {
      final newSessions = Map<String, CachedSessionRecord>.from(state.sessions);
      final sr = newSessions[snapshotKey];
      if (sr != null) {
        newSessions[snapshotKey] =
            sr.copyWith(lastEventId: eventId, lastUpdatedAt: DateTime.now());
        state = state.copyWith(sessions: newSessions);
        _dirtySessions.add(snapshotKey);
      }
    }

    _dirtySnapshots.add(snapshotKey);
    _scheduleFlush();
  }

  /// Get the cached snapshot for a specific session by sessionId.
  CachedSessionSnapshot? getSessionSnapshot(String sessionId) {
    for (final entry in state.sessions.entries) {
      if (entry.value.sessionId == sessionId) {
        return state.snapshots[entry.key];
      }
    }
    return null;
  }

  Future<bool> attachSessionToActiveWorkspace(String sessionId) async {
    if (sessionId.isEmpty) return false;
    await initialize();
    if (!ref.mounted) return false;
    final workspaceKey = state.liveWorkspaceKey ?? state.selectedWorkspaceKey;
    if (workspaceKey == null || workspaceKey.isEmpty) return false;

    await _ensureSnapshotLoaded(sessionId);
    if (!ref.mounted) return false;
    final targetRecord = state.sessions[sessionId];
    final hasTargetSnapshot = state.snapshots.containsKey(sessionId);
    if (targetRecord == null) {
      return false;
    }
    final updatedRecord = targetRecord.workspaceKey == workspaceKey
        ? targetRecord
        : targetRecord.copyWith(lastUpdatedAt: DateTime.now());
    state = state.copyWith(
      sessions: Map<String, CachedSessionRecord>.from(state.sessions)
        ..[sessionId] = CachedSessionRecord(
          workspaceKey: workspaceKey,
          sessionId: updatedRecord.sessionId,
          title: updatedRecord.title,
          model: updatedRecord.model,
          provider: updatedRecord.provider,
          mode: updatedRecord.mode,
          version: updatedRecord.version,
          lastEventId: updatedRecord.lastEventId,
          lastUpdatedAt: updatedRecord.lastUpdatedAt,
        ),
      selectedWorkspaceKey: workspaceKey,
      selectedSessionId: sessionId,
    );
    _dirtySessions.add(sessionId);
    _selectionDirty = true;
    _scheduleFlush();
    return hasTargetSnapshot;
  }

  Future<void> selectSession(String sessionId) async {
    await initialize();
    if (!ref.mounted) return;
    CachedSessionRecord? record;
    for (final s in state.sessions.values) {
      if (s.sessionId == sessionId) {
        record = s;
        break;
      }
    }
    debugPrint('[cache] selectSession: sessionId=$sessionId found=${record != null} workspaceKey=${record?.workspaceKey} oldSelected=${state.selectedSessionId} live=${state.liveSessionId}');
    state = state.copyWith(
      selectedWorkspaceKey: record?.workspaceKey.isNotEmpty == true
          ? record!.workspaceKey
          : state.selectedWorkspaceKey,
      selectedSessionId: sessionId,
    );
    await _ensureSnapshotLoaded(sessionId);
    if (!ref.mounted) return;
    _selectionDirty = true;
    _scheduleFlush();
  }

  /// Mark a session as the live session (foreground WebSocket is connected
  /// to it). Called when adopting a background service to foreground, or
  /// when a new connection's session becomes active.
  void setLive(String sessionId) {
    CachedSessionRecord? record;
    for (final s in state.sessions.values) {
      if (s.sessionId == sessionId) {
        record = s;
        break;
      }
    }
    debugPrint('[cache] setLive: sessionId=$sessionId found=${record != null} workspaceKey=${record?.workspaceKey} oldLive=${state.liveSessionId}');
    state = state.copyWith(
      liveSessionId: sessionId,
      liveWorkspaceKey: record?.workspaceKey.isNotEmpty == true
          ? record!.workspaceKey
          : state.liveWorkspaceKey,
    );
  }

  Future<void> registerLiveSession(
    String sessionId,
    proto.SessionInfoData? sessionInfo, {
    String? lastEventId,
    int? authorityEpoch,
  }) async {
    if (sessionId.isEmpty) return;
    await initialize();
    if (!ref.mounted) return;
    // Derive workspace key from the host-provided workspace path.
    // This ensures multiple host instances sharing the same workspace directory
    // map to the same key on mobile, regardless of their relay URL/token.
    final workspacePath = sessionInfo?.workspace ?? '';
    if (workspacePath.isEmpty) return; // Wait for active_session
    final workspaceKey = _workspaceKeyForPath(workspacePath);
    final now = DateTime.now();
    final previousLiveSessionId = state.liveSessionId;
    final lastKnownLiveSessionId =
        state.workspaces[workspaceKey]?.lastSessionId;
    final selectionFollowedLive = state.selectedSessionId == null ||
        state.selectedSessionId!.isEmpty ||
        state.selectedSessionId == previousLiveSessionId ||
        (previousLiveSessionId == null &&
            state.selectedSessionId == lastKnownLiveSessionId);
    debugPrint('[cache] registerLiveSession: sessionId=$sessionId prevLive=$previousLiveSessionId selected=${state.selectedSessionId} lastKnown=$lastKnownLiveSessionId followLive=$selectionFollowedLive');
    final sessionKey = sessionId;
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
                workspacePath: sessionInfo?.workspace ?? '',
                lastEventId: lastEventId ?? '',
                authorityEpoch: authorityEpoch ?? 0,
                lastUpdatedAt: now,
              ))
          .copyWith(
        title: _sessionTitle(sessionInfo, sessionId),
        model: sessionInfo?.model ?? state.sessions[sessionKey]?.model,
        provider: sessionInfo?.provider ?? state.sessions[sessionKey]?.provider,
        mode: sessionInfo?.mode ?? state.sessions[sessionKey]?.mode,
        version: sessionInfo?.version ?? state.sessions[sessionKey]?.version,
        workspacePath:
            sessionInfo?.workspace ?? state.sessions[sessionKey]?.workspacePath,
        lastEventId: lastEventId ?? state.sessions[sessionKey]?.lastEventId,
        authorityEpoch:
            authorityEpoch ?? state.sessions[sessionKey]?.authorityEpoch,
        lastUpdatedAt: now,
        url: _pendingWorkspaceUrl ?? state.sessions[sessionKey]?.url ?? '',
      );
    final workspace = state.workspaces[workspaceKey];
    final workspaces = Map<String, WorkspaceRecord>.from(state.workspaces);
    final displayName =
        _workspaceDisplayName(sessionInfo);
    if (workspace != null) {
      workspaces[workspaceKey] = workspace.copyWith(
        displayName:
            displayName.isNotEmpty ? displayName : workspace.displayName,
        lastSessionId: sessionId,
        lastOpenedAt: now,
      );
    } else if (displayName.isNotEmpty) {
      // Workspace doesn't exist yet — create it now that we have sessionInfo.
      _pendingWorkspaceUrl = null;
      workspaces[workspaceKey] = WorkspaceRecord(
        key: workspaceKey,
        displayName: displayName,
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
      await _ensureSnapshotLoaded(sessionId);
    }
  }

  Future<void> observeLiveSession(
    String sessionId, {
    required String previousSessionId,
    proto.SessionInfoData? sessionInfo,
    int? authorityEpoch,
  }) async {
    await registerLiveSession(
      sessionId,
      sessionInfo,
      lastEventId: state.sessions[sessionId]?.lastEventId,
      authorityEpoch: authorityEpoch,
    );
    if (previousSessionId.isEmpty || previousSessionId == sessionId) return;
  }

  Future<void> updateLiveCursor(String sessionId, String lastEventId,
      {int? authorityEpoch}) async {
    if (sessionId.isEmpty) return;
    await initialize();
    if (!ref.mounted) return;
    final record = state.sessions[sessionId];
    if (record == null) return;
    final updated = Map<String, CachedSessionRecord>.from(state.sessions)
      ..[sessionId] = record.copyWith(
        lastEventId: lastEventId,
        authorityEpoch: authorityEpoch ?? record.authorityEpoch,
        lastUpdatedAt: DateTime.now(),
      );
    state = state.copyWith(sessions: updated);
    _dirtySessions.add(sessionId);
    _scheduleFlush();
  }

  Future<void> captureLiveProjection({
    required List<ChatMessage> messages,
    required Map<String, SubagentInfo> subagents,
    required proto.SessionInfoData? sessionInfo,
    required String agentStatus,
    required String agentStatusMessage,
    required String lastEventId,
    int authorityEpoch = 0,
    bool authoritative = true,
    String sessionUrl = '',
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
    if (!authoritative) {
      return;
    }
    final snapshotKey = sessionId;
    final candidateSnapshot = CachedSessionSnapshot(
      messages: List<ChatMessage>.from(messages),
      subagents: Map<String, SubagentInfo>.from(subagents),
      sessionInfo: sessionInfo,
      agentStatus: _normalizedCachedAgentStatus(agentStatus),
      agentStatusMessage: agentStatusMessage,
      lastEventId: lastEventId,
      authorityEpoch: authorityEpoch,
    );
    final existingSnapshot =
        state.snapshots[snapshotKey] ?? _store?.loadSnapshot(sessionId);
    final snapshot = _coalesceCachedSnapshotForPersistence(
      existing: existingSnapshot,
      candidate: candidateSnapshot,
    );
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
                authorityEpoch: authorityEpoch,
                lastUpdatedAt: now,
                url: sessionUrl,
              ))
          .copyWith(
        title: _sessionTitle(sessionInfo, sessionId),
        model: sessionInfo?.model ?? state.sessions[snapshotKey]?.model,
        provider:
            sessionInfo?.provider ?? state.sessions[snapshotKey]?.provider,
        mode: sessionInfo?.mode ?? state.sessions[snapshotKey]?.mode,
        version: sessionInfo?.version ?? state.sessions[snapshotKey]?.version,
        lastEventId: lastEventId,
        authorityEpoch: authorityEpoch,
        lastUpdatedAt: now,
      );
    final workspaces = Map<String, WorkspaceRecord>.from(state.workspaces);
    final workspace = workspaces[workspaceKey];
    if (workspace != null) {
      workspaces[workspaceKey] = workspace.copyWith(
        displayName: _workspaceDisplayName(sessionInfo),
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

  List<CachedSessionRecord> sortedSessions() {
    final items = state.sessions.values.toList()
      ..sort((a, b) => b.lastUpdatedAt.compareTo(a.lastUpdatedAt));
    return items;
  }

  List<CachedSessionRecord> sessionsForWorkspace(String workspaceKey) {
    final items = state.sessions.values
        .where((record) => record.workspaceKey == workspaceKey)
        .toList()
      ..sort((a, b) => b.lastUpdatedAt.compareTo(a.lastUpdatedAt));
    return items;
  }

  CachedSessionSnapshot? snapshotFor(String sessionId) {
    final cached = state.snapshots[sessionId];
    if (cached != null) {
      return cached;
    }
    final snapshot = _store?.loadSnapshot(sessionId);
    if (snapshot == null) {
      return null;
    }
    state = state.copyWith(
      snapshots: Map<String, CachedSessionSnapshot>.from(state.snapshots)
        ..[sessionId] = snapshot,
    );
    return snapshot;
  }

  Future<void> _ensureSnapshotLoaded(String sessionId) async {
    await initialize();
    if (!ref.mounted) return;
    snapshotFor(sessionId);
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
      final session = state.sessions[key];
      if (session == null || session.workspaceKey.isEmpty) continue;
      pendingSnapshotWrites.add(
        _SnapshotWrite(
          workspaceKey: session.workspaceKey,
          sessionId: key,
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
      final notifyDurable = onDurableSnapshotPersisted;
      if (notifyDurable != null) {
        for (final entry in pendingSnapshotWrites) {
          final lastEventId = entry.snapshot.lastEventId;
          if (lastEventId.isEmpty) {
            continue;
          }
          await notifyDurable(entry.sessionId, lastEventId);
        }
      }
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
    final selectedSession = state.selectedSessionId == null
        ? null
        : state.sessions[_sessionCacheKey(
            state.selectedWorkspaceKey ?? '', state.selectedSessionId!)];
    final payload = {
      'selected_workspace_key': state.selectedWorkspaceKey,
      'selected_session_id': state.selectedSessionId,
      _workspaceCacheIndexSelectedWorkspaceUrlKey: selectedSession?.url,
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
    final liveChat = ref.watch(chatProvider);
    if (liveChat.isNotEmpty) return liveChat;
    // Live chat is empty (e.g. just switched sessions, waiting for replay).
    // Fall through to snapshot if available.
  }
  final sessionId = cache.selectedSessionId;
  if (sessionId == null || sessionId.isEmpty) {
    return const [];
  }
  final snapshot = cache.snapshots[sessionId];
  if (snapshot == null) {
    return const [];
  }
  return historicalSnapshotMessages(snapshot);
});

final displayedSubagentProvider = Provider<Map<String, SubagentInfo>>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return ref.watch(subagentProvider);
  }
  final sessionId = cache.selectedSessionId;
  if (sessionId == null || sessionId.isEmpty) {
    return const {};
  }
  final snapshot = cache.snapshots[sessionId];
  if (snapshot == null) {
    return const {};
  }
  return historicalSnapshotSubagents(snapshot);
});

final displayedSessionInfoProvider = Provider<proto.SessionInfoData?>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return ref.watch(sessionInfoProvider);
  }
  final sessionId = cache.selectedSessionId;
  if (sessionId == null || sessionId.isEmpty) {
    return null;
  }
  return cache.snapshots[sessionId]?.sessionInfo;
});

final displayedAgentStatusProvider = Provider<String>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return _normalizedCachedAgentStatus(ref.watch(agentStatusProvider));
  }
  final sessionId = cache.selectedSessionId;
  if (sessionId == null || sessionId.isEmpty) {
    return 'idle';
  }
  return _normalizedCachedAgentStatus(
    cache.snapshots[sessionId]?.agentStatus ?? 'idle',
  );
});

final displayedAgentStatusMessageProvider = Provider<String>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  if (_isViewingLive(cache)) {
    return ref.watch(agentStatusMessageProvider);
  }
  final sessionId = cache.selectedSessionId;
  if (sessionId == null || sessionId.isEmpty) {
    return '';
  }
  return cache.snapshots[sessionId]?.agentStatusMessage ?? '';
});

final isHistoricalViewProvider = Provider<bool>((ref) {
  final cache = ref.watch(workspaceCacheProvider);
  final selectedSessionId = cache.selectedSessionId;
  if (selectedSessionId == null || selectedSessionId.isEmpty) {
    return false;
  }
  // Not historical if viewing the live session (foreground WebSocket)
  return !_isViewingLive(cache);
});

final canSendMessagesProvider = Provider<bool>((ref) {
  final conn = ref.watch(connectionProvider);
  return conn.sessionReady && !ref.watch(isHistoricalViewProvider);
});

bool _isViewingLive(WorkspaceCacheState state) {
  final sessionId = state.selectedSessionId;
  if (sessionId == null || sessionId.isEmpty) {
    return true;
  }
  return sessionId == state.liveSessionId;
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

/// Derive a stable workspace key from the host-provided workspace path.
/// Same path on different host instances → same key on mobile.
String _workspaceKeyForPath(String path) =>
    base64UrlEncode(utf8.encode(path)).replaceAll('=', '');

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

Map<String, SubagentInfo> historicalSnapshotSubagents(
  CachedSessionSnapshot snapshot,
) {
  return const {};
}

List<ChatMessage> historicalSnapshotMessages(CachedSessionSnapshot snapshot) {
  return snapshot.messages
      .where((message) => message.sourceId == null)
      .toList();
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

/// Extract workspace display name from sessionInfo.workspace (the desktop's
/// working directory path, e.g. "/Users/zanchen/projects/my-app" → "my-app").
/// Returns empty string if sessionInfo or workspace is unavailable — the caller
/// should load from cached snapshot instead of guessing from the relay URL.
String _workspaceDisplayName(proto.SessionInfoData? sessionInfo) {
  final workspace = sessionInfo?.workspace ?? '';
  if (workspace.isNotEmpty) {
    final parts = workspace.split('/');
    return parts.isNotEmpty ? parts.last : workspace;
  }
  return '';
}

String _sessionTitle(proto.SessionInfoData? sessionInfo, String sessionId) {
  final workspace = sessionInfo?.workspace ?? '';
  final label = workspace.isNotEmpty ? workspace.split('/').last : 'Session';
  final shortId = sessionId.length > 8 ? sessionId.substring(0, 8) : sessionId;
  return '$label · $shortId';
}
