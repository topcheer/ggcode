package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultDBDir           = "/db"
	defaultDBFilename      = "relay.db"
	defaultCleanupAge      = 12 * time.Hour
	defaultCleanupInterval = 6 * time.Hour
)

type relayStore struct {
	db        *sql.DB
	retention time.Duration
}

type persistedRoomState struct {
	sessionID      string
	authorityEpoch uint64
	history        []roomEvent
}

func openRelayStore(dbPath string, retention time.Duration) (*relayStore, error) {
	if retention <= 0 {
		retention = defaultCleanupAge
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := initRelaySchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &relayStore{db: db, retention: retention}, nil
}

func initRelaySchema(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS relay_rooms (
  token_hash TEXT PRIMARY KEY,
  current_session_id TEXT NOT NULL DEFAULT '',
  current_authority_epoch INTEGER NOT NULL DEFAULT 1,
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS relay_sessions (
  token_hash TEXT NOT NULL,
  session_id TEXT NOT NULL,
  last_event_at TIMESTAMP NOT NULL,
  PRIMARY KEY (token_hash, session_id)
);

CREATE TABLE IF NOT EXISTS relay_global_sessions (
  session_id TEXT PRIMARY KEY,
  last_event_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS relay_events (
  token_hash TEXT NOT NULL,
  session_id TEXT NOT NULL,
  event_id TEXT NOT NULL,
  stream_id TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL,
  raw BLOB NOT NULL,
  created_at TIMESTAMP NOT NULL,
  PRIMARY KEY (token_hash, session_id, event_id)
);

CREATE INDEX IF NOT EXISTS idx_relay_events_room_session
  ON relay_events(token_hash, session_id, event_id);

CREATE TABLE IF NOT EXISTS relay_global_events (
  session_id TEXT NOT NULL,
  event_id TEXT NOT NULL,
  stream_id TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL,
  raw BLOB NOT NULL,
  created_at TIMESTAMP NOT NULL,
  PRIMARY KEY (session_id, event_id)
);

CREATE INDEX IF NOT EXISTS idx_relay_global_events_session
  ON relay_global_events(session_id, event_id);

CREATE INDEX IF NOT EXISTS idx_relay_sessions_expiry
  ON relay_sessions(last_event_at);

CREATE INDEX IF NOT EXISTS idx_relay_global_sessions_expiry
  ON relay_global_sessions(last_event_at);

CREATE TABLE IF NOT EXISTS relay_client_cursors (
  room_token_hash TEXT NOT NULL,
  client_id TEXT NOT NULL,
  session_id TEXT NOT NULL DEFAULT '',
  last_acked_event_id TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY (room_token_hash, client_id)
);

CREATE TABLE IF NOT EXISTS relay_model_catalog_sync_state (
  source_ref TEXT PRIMARY KEY,
  source_sha TEXT NOT NULL DEFAULT '',
  last_attempt_at TIMESTAMP,
  last_success_at TIMESTAMP,
  last_error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS relay_model_catalog_entries (
  provider_id TEXT NOT NULL,
  model_id TEXT NOT NULL,
  provider_name TEXT NOT NULL DEFAULT '',
  provider_type TEXT NOT NULL DEFAULT '',
  context_window INTEGER NOT NULL DEFAULT 0,
  default_max_tokens INTEGER NOT NULL DEFAULT 0,
  source_file TEXT NOT NULL DEFAULT '',
  source_sha TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY (provider_id, model_id)
);

CREATE INDEX IF NOT EXISTS idx_relay_model_catalog_model
  ON relay_model_catalog_entries(model_id);
`
	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("init relay schema: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE relay_rooms ADD COLUMN current_authority_epoch INTEGER NOT NULL DEFAULT 1`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return fmt.Errorf("add relay room authority epoch column: %w", err)
	}
	return nil
}

func (s *relayStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *relayStore) loadRoom(token string) (persistedRoomState, error) {
	if s == nil {
		return persistedRoomState{}, nil
	}
	tokenHash := hashToken(token)
	var state persistedRoomState
	err := s.db.QueryRow(
		`SELECT current_session_id, current_authority_epoch FROM relay_rooms WHERE token_hash = ?`,
		tokenHash,
	).Scan(&state.sessionID, &state.authorityEpoch)
	if err == sql.ErrNoRows {
		return persistedRoomState{}, nil
	}
	if err != nil {
		return persistedRoomState{}, fmt.Errorf("query room: %w", err)
	}
	if state.sessionID == "" {
		if state.authorityEpoch == 0 {
			state.authorityEpoch = 1
		}
		return state, nil
	}
	rows, err := s.db.Query(
		`SELECT session_id, event_id, stream_id, type, raw
		   FROM relay_events
		  WHERE token_hash = ? AND session_id = ?
		  ORDER BY event_id`,
		tokenHash, state.sessionID,
	)
	if err != nil {
		return persistedRoomState{}, fmt.Errorf("query room events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ev roomEvent
		if err := rows.Scan(&ev.sessionID, &ev.eventID, &ev.streamID, &ev.typ, &ev.raw); err != nil {
			return persistedRoomState{}, fmt.Errorf("scan room event: %w", err)
		}
		ev.raw = append([]byte(nil), ev.raw...)
		var meta struct {
			EventHash string `json:"event_hash,omitempty"`
		}
		if err := json.Unmarshal(ev.raw, &meta); err == nil {
			ev.eventHash = meta.EventHash
		}
		state.history = append(state.history, ev)
	}
	if err := rows.Err(); err != nil {
		return persistedRoomState{}, fmt.Errorf("iterate room events: %w", err)
	}
	return state, nil
}

func (s *relayStore) persistEvent(token string, msg relayMessage, raw []byte) error {
	if s == nil || msg.SessionID == "" || msg.EventID == "" {
		return nil
	}
	if msg.AuthorityEpoch == 0 {
		msg.AuthorityEpoch = 1
	}
	tokenHash := hashToken(token)
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin persist tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(
		`INSERT INTO relay_rooms(token_hash, current_session_id, current_authority_epoch, updated_at)
		 VALUES(?, ?, ?, ?)
		 ON CONFLICT(token_hash) DO UPDATE SET
		   current_session_id = excluded.current_session_id,
		   current_authority_epoch = excluded.current_authority_epoch,
		   updated_at = excluded.updated_at`,
		tokenHash, msg.SessionID, msg.AuthorityEpoch, now,
	); err != nil {
		return fmt.Errorf("upsert room: %w", err)
	}
	if _, err = tx.Exec(
		`INSERT INTO relay_sessions(token_hash, session_id, last_event_at)
		 VALUES(?, ?, ?)
		 ON CONFLICT(token_hash, session_id) DO UPDATE SET
		   last_event_at = excluded.last_event_at`,
		tokenHash, msg.SessionID, now,
	); err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}
	if _, err = tx.Exec(
		`INSERT INTO relay_events(token_hash, session_id, event_id, stream_id, type, raw, created_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(token_hash, session_id, event_id) DO UPDATE SET
		   stream_id = excluded.stream_id,
		   type = excluded.type,
		   raw = excluded.raw`,
		tokenHash, msg.SessionID, msg.EventID, msg.StreamID, msg.Type, raw, now,
	); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit persist tx: %w", err)
	}
	return nil
}

func (s *relayStore) persistActiveSession(token, sessionID string, authorityEpoch uint64) error {
	if s == nil || sessionID == "" {
		return nil
	}
	if authorityEpoch == 0 {
		authorityEpoch = 1
	}
	tokenHash := hashToken(token)
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin active session tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(
		`INSERT INTO relay_rooms(token_hash, current_session_id, current_authority_epoch, updated_at)
		 VALUES(?, ?, ?, ?)
		 ON CONFLICT(token_hash) DO UPDATE SET
		   current_session_id = excluded.current_session_id,
		   current_authority_epoch = excluded.current_authority_epoch,
		   updated_at = excluded.updated_at`,
		tokenHash, sessionID, authorityEpoch, now,
	); err != nil {
		return fmt.Errorf("upsert active room: %w", err)
	}
	if _, err = tx.Exec(
		`INSERT INTO relay_sessions(token_hash, session_id, last_event_at)
		 VALUES(?, ?, ?)
		 ON CONFLICT(token_hash, session_id) DO UPDATE SET
		   last_event_at = excluded.last_event_at`,
		tokenHash, sessionID, now,
	); err != nil {
		return fmt.Errorf("upsert active session: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit active session tx: %w", err)
	}
	return nil
}

func (s *relayStore) destroyRoom(token string) error {
	if s == nil {
		return nil
	}
	tokenHash := hashToken(token)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin destroy room tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(`DELETE FROM relay_events WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete room events: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM relay_sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete room sessions: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM relay_client_cursors WHERE room_token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete room cursors: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM relay_rooms WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete room: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit destroy room tx: %w", err)
	}
	return nil
}

func (s *relayStore) cleanupExpired(now time.Time) error {
	if s == nil {
		return nil
	}
	cutoff := now.UTC().Add(-s.retention)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin cleanup tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(
		`UPDATE relay_rooms
		    SET current_session_id = ''
		  WHERE current_session_id != ''
		    AND EXISTS (
		      SELECT 1
		        FROM relay_sessions s
		       WHERE s.token_hash = relay_rooms.token_hash
		         AND s.session_id = relay_rooms.current_session_id
		         AND s.last_event_at < ?
		    )`,
		cutoff,
	); err != nil {
		return fmt.Errorf("clear expired current sessions: %w", err)
	}
	if _, err = tx.Exec(
		`DELETE FROM relay_events
		  WHERE EXISTS (
		      SELECT 1
		        FROM relay_sessions s
		       WHERE s.token_hash = relay_events.token_hash
		         AND s.session_id = relay_events.session_id
		         AND s.last_event_at < ?
		    )`,
		cutoff,
	); err != nil {
		return fmt.Errorf("delete expired events: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM relay_sessions WHERE last_event_at < ?`, cutoff); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	if _, err = tx.Exec(
		`DELETE FROM relay_global_events
		  WHERE EXISTS (
		      SELECT 1
		        FROM relay_global_sessions s
		       WHERE s.session_id = relay_global_events.session_id
		         AND s.last_event_at < ?
		    )`,
		cutoff,
	); err != nil {
		return fmt.Errorf("delete expired global events: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM relay_global_sessions WHERE last_event_at < ?`, cutoff); err != nil {
		return fmt.Errorf("delete expired global sessions: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM relay_rooms WHERE current_session_id = '' AND updated_at < ?`, cutoff); err != nil {
		return fmt.Errorf("delete expired rooms: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit cleanup tx: %w", err)
	}
	return nil
}

func relayDBPath() string {
	if path := os.Getenv("GGCODE_RELAY_DB_PATH"); path != "" {
		return path
	}
	if info, err := os.Stat(defaultDBDir); err == nil && info.IsDir() {
		return filepath.Join(defaultDBDir, defaultDBFilename)
	}
	return defaultDBFilename
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *relayStore) nukeAll() error {
	tables := []string{
		"relay_events",
		"relay_sessions",
		"relay_rooms",
		"relay_global_events",
		"relay_global_sessions",
		"relay_client_cursors",
		"relay_model_catalog_entries",
		"relay_model_catalog_sync_state",
	}
	for _, t := range tables {
		if _, err := s.db.Exec("DELETE FROM " + t); err != nil {
			return fmt.Errorf("delete %s: %w", t, err)
		}
	}
	return nil
}

func (s *relayStore) loadModelCatalog() (modelCatalogSyncState, []modelCatalogEntry, error) {
	if s == nil {
		return modelCatalogSyncState{SourceRef: modelCatalogSourceRef}, nil, nil
	}
	state := modelCatalogSyncState{SourceRef: modelCatalogSourceRef}
	var lastAttempt sql.NullTime
	var lastSuccess sql.NullTime
	if err := s.db.QueryRow(
		`SELECT source_sha, last_attempt_at, last_success_at, last_error
		   FROM relay_model_catalog_sync_state
		  WHERE source_ref = ?`,
		modelCatalogSourceRef,
	).Scan(&state.SourceSHA, &lastAttempt, &lastSuccess, &state.LastError); err != nil && err != sql.ErrNoRows {
		return modelCatalogSyncState{}, nil, fmt.Errorf("load model catalog state: %w", err)
	}
	if lastAttempt.Valid {
		state.LastAttemptAt = lastAttempt.Time.UTC()
	}
	if lastSuccess.Valid {
		state.LastSuccessAt = lastSuccess.Time.UTC()
	}

	rows, err := s.db.Query(
		`SELECT provider_id, model_id, provider_name, provider_type, context_window, default_max_tokens, source_file, source_sha, updated_at
		   FROM relay_model_catalog_entries
		  ORDER BY provider_id, model_id`,
	)
	if err != nil {
		return modelCatalogSyncState{}, nil, fmt.Errorf("load model catalog entries: %w", err)
	}
	defer rows.Close()
	entries := make([]modelCatalogEntry, 0)
	for rows.Next() {
		var entry modelCatalogEntry
		if err := rows.Scan(
			&entry.ProviderID,
			&entry.ModelID,
			&entry.ProviderName,
			&entry.ProviderType,
			&entry.ContextWindow,
			&entry.MaxOutputTokens,
			&entry.SourceFile,
			&entry.SourceSHA,
			&entry.UpdatedAt,
		); err != nil {
			return modelCatalogSyncState{}, nil, fmt.Errorf("scan model catalog entry: %w", err)
		}
		entry.UpdatedAt = entry.UpdatedAt.UTC()
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return modelCatalogSyncState{}, nil, fmt.Errorf("iterate model catalog entries: %w", err)
	}
	state.RowCount = len(entries)
	return state, entries, nil
}

func (s *relayStore) upsertModelCatalogState(state modelCatalogSyncState) error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(
		`INSERT INTO relay_model_catalog_sync_state(source_ref, source_sha, last_attempt_at, last_success_at, last_error)
		 VALUES(?, ?, ?, ?, ?)
		 ON CONFLICT(source_ref) DO UPDATE SET
		   source_sha = CASE
		     WHEN excluded.source_sha != '' THEN excluded.source_sha
		     ELSE relay_model_catalog_sync_state.source_sha
		   END,
		   last_attempt_at = excluded.last_attempt_at,
		   last_success_at = CASE
		     WHEN excluded.last_success_at IS NOT NULL THEN excluded.last_success_at
		     ELSE relay_model_catalog_sync_state.last_success_at
		   END,
		   last_error = excluded.last_error`,
		firstNonEmptyString(state.SourceRef, modelCatalogSourceRef),
		strings.TrimSpace(state.SourceSHA),
		nullableTime(state.LastAttemptAt),
		nullableTime(state.LastSuccessAt),
		strings.TrimSpace(state.LastError),
	)
	if err != nil {
		return fmt.Errorf("upsert model catalog state: %w", err)
	}
	return nil
}

func (s *relayStore) replaceModelCatalog(state modelCatalogSyncState, entries []modelCatalogEntry) error {
	if s == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin model catalog tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(`DELETE FROM relay_model_catalog_entries`); err != nil {
		return fmt.Errorf("clear model catalog entries: %w", err)
	}
	for _, entry := range entries {
		if _, err = tx.Exec(
			`INSERT INTO relay_model_catalog_entries(provider_id, model_id, provider_name, provider_type, context_window, default_max_tokens, source_file, source_sha, updated_at)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			strings.TrimSpace(entry.ProviderID),
			strings.TrimSpace(entry.ModelID),
			strings.TrimSpace(entry.ProviderName),
			strings.TrimSpace(entry.ProviderType),
			entry.ContextWindow,
			entry.MaxOutputTokens,
			strings.TrimSpace(entry.SourceFile),
			strings.TrimSpace(entry.SourceSHA),
			entry.UpdatedAt.UTC(),
		); err != nil {
			return fmt.Errorf("insert model catalog entry %s/%s: %w", entry.ProviderID, entry.ModelID, err)
		}
	}
	if _, err = tx.Exec(
		`INSERT INTO relay_model_catalog_sync_state(source_ref, source_sha, last_attempt_at, last_success_at, last_error)
		 VALUES(?, ?, ?, ?, '')
		 ON CONFLICT(source_ref) DO UPDATE SET
		   source_sha = excluded.source_sha,
		   last_attempt_at = excluded.last_attempt_at,
		   last_success_at = excluded.last_success_at,
		   last_error = ''`,
		firstNonEmptyString(state.SourceRef, modelCatalogSourceRef),
		strings.TrimSpace(state.SourceSHA),
		nullableTime(state.LastAttemptAt),
		nullableTime(state.LastSuccessAt),
	); err != nil {
		return fmt.Errorf("upsert model catalog sync state: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit model catalog tx: %w", err)
	}
	return nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// loadClientCursor loads the last ACK'd event ID for a client in a room.
// Returns ("", nil) if no cursor exists.
func (s *relayStore) loadClientCursor(tokenHash, clientID string) (string, error) {
	if s == nil {
		return "", nil
	}
	var eventID string
	err := s.db.QueryRow(
		`SELECT last_acked_event_id FROM relay_client_cursors
		  WHERE room_token_hash = ? AND client_id = ?`,
		tokenHash, clientID,
	).Scan(&eventID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return eventID, err
}

// saveClientCursor persists the client's ACK cursor.
func (s *relayStore) saveClientCursor(tokenHash, clientID, sessionID, eventID string) error {
	if s == nil {
		return nil
	}
	now := time.Now().UTC()
	_, err := s.db.Exec(
		`INSERT INTO relay_client_cursors (room_token_hash, client_id, session_id, last_acked_event_id, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(room_token_hash, client_id)
		 DO UPDATE SET session_id = excluded.session_id,
		               last_acked_event_id = excluded.last_acked_event_id,
		               updated_at = excluded.updated_at`,
		tokenHash, clientID, sessionID, eventID, now,
	)
	return err
}
