package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultDBDir           = "/db"
	defaultDBFilename      = "relay.db"
	defaultCleanupAge      = 72 * time.Hour
	defaultCleanupInterval = 6 * time.Hour
)

type relayStore struct {
	db        *sql.DB
	retention time.Duration
}

type persistedRoomState struct {
	sessionID string
	history   []roomEvent
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
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS relay_sessions (
  token_hash TEXT NOT NULL,
  session_id TEXT NOT NULL,
  last_event_at TIMESTAMP NOT NULL,
  PRIMARY KEY (token_hash, session_id)
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

CREATE INDEX IF NOT EXISTS idx_relay_sessions_expiry
  ON relay_sessions(last_event_at);
`
	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("init relay schema: %w", err)
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
		`SELECT current_session_id FROM relay_rooms WHERE token_hash = ?`,
		tokenHash,
	).Scan(&state.sessionID)
	if err == sql.ErrNoRows {
		return persistedRoomState{}, nil
	}
	if err != nil {
		return persistedRoomState{}, fmt.Errorf("query room: %w", err)
	}
	if state.sessionID == "" {
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
		`INSERT INTO relay_rooms(token_hash, current_session_id, updated_at)
		 VALUES(?, ?, ?)
		 ON CONFLICT(token_hash) DO UPDATE SET
		   current_session_id = excluded.current_session_id,
		   updated_at = excluded.updated_at`,
		tokenHash, msg.SessionID, now,
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

func (s *relayStore) persistActiveSession(token, sessionID string) error {
	if s == nil || sessionID == "" {
		return nil
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
		`INSERT INTO relay_rooms(token_hash, current_session_id, updated_at)
		 VALUES(?, ?, ?)
		 ON CONFLICT(token_hash) DO UPDATE SET
		   current_session_id = excluded.current_session_id,
		   updated_at = excluded.updated_at`,
		tokenHash, sessionID, now,
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
