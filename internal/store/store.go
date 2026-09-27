// Package store manages persistent diagnostic session storage using SQLite.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // register sqlite driver
)

// DiagSession is the complete persisted record for one diagnostic session.
type DiagSession struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	ClientIP  string    `json:"client_ip"`

	// UniFi correlation
	CorrelationStatus string    `json:"correlation_status"` // ok | not_found | ambiguous | unavailable | pending
	CorrelationAt     time.Time `json:"correlation_at,omitempty"`
	ClientMAC         string    `json:"client_mac,omitempty"`
	ClientHostname    string    `json:"client_hostname,omitempty"`
	ClientName        string    `json:"client_name,omitempty"`
	UnifiClientID     string    `json:"unifi_client_id,omitempty"`
	UAValidation      string    `json:"ua_validation,omitempty"` // match | mismatch | unknown

	// Wi-Fi info from UniFi
	SSID         string `json:"ssid,omitempty"`
	APMAC        string `json:"ap_mac,omitempty"`
	Band         string `json:"band,omitempty"`
	Channel      int    `json:"channel,omitempty"`
	ChannelWidth string `json:"channel_width,omitempty"`
	RSSI         int    `json:"rssi,omitempty"`
	TXRate       int    `json:"tx_rate,omitempty"`
	RXRate       int    `json:"rx_rate,omitempty"`
	Retries      int    `json:"retries,omitempty"`
	Satisfaction int    `json:"satisfaction,omitempty"`

	// Browser info
	UserAgent string `json:"user_agent,omitempty"`

	// Measurements — stored as JSON blobs
	LANLatency  json.RawMessage `json:"lan_latency,omitempty"`
	InternetResult json.RawMessage `json:"internet_result,omitempty"`
	Download    json.RawMessage `json:"download,omitempty"`
	Upload      json.RawMessage `json:"upload,omitempty"`
	Stability   json.RawMessage `json:"stability,omitempty"`

	// Assessment
	Assessment json.RawMessage `json:"assessment,omitempty"`

	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// Store manages the SQLite database for diagnostic sessions.
type Store struct {
	db     *sql.DB
	logger *slog.Logger
	noop   bool // when true, all writes are no-ops (no-persistence mode)
}

// Open opens (or creates) the SQLite database at the given directory.
// If noPersistence is true the store operates in-memory and no data is written.
func Open(dataDir string, noPersistence bool, logger *slog.Logger) (*Store, error) {
	if noPersistence {
		return &Store{logger: logger, noop: true}, nil
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "sessions.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal=WAL&_timeout=5000&_fk=true")
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite handles one writer at a time

	s := &Store{db: db, logger: logger}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	if s.noop || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Save upserts a diagnostic session.
func (s *Store) Save(ctx context.Context, sess *DiagSession) error {
	if s.noop {
		return nil
	}

	lanJSON, _ := json.Marshal(sess.LANLatency)
	inetJSON, _ := json.Marshal(sess.InternetResult)
	dlJSON, _ := json.Marshal(sess.Download)
	ulJSON, _ := json.Marshal(sess.Upload)
	stJSON, _ := json.Marshal(sess.Stability)
	asJSON, _ := json.Marshal(sess.Assessment)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, created_at, client_ip,
			correlation_status, correlation_at,
			client_mac, client_hostname, client_name, unifi_client_id, ua_validation,
			ssid, ap_mac, band, channel, channel_width, rssi, tx_rate, rx_rate, retries, satisfaction,
			user_agent,
			lan_latency, internet_result, download, upload, stability, assessment,
			completed_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			correlation_status=excluded.correlation_status,
			correlation_at=excluded.correlation_at,
			client_mac=excluded.client_mac,
			client_hostname=excluded.client_hostname,
			client_name=excluded.client_name,
			unifi_client_id=excluded.unifi_client_id,
			ua_validation=excluded.ua_validation,
			ssid=excluded.ssid,
			ap_mac=excluded.ap_mac,
			band=excluded.band,
			channel=excluded.channel,
			channel_width=excluded.channel_width,
			rssi=excluded.rssi,
			tx_rate=excluded.tx_rate,
			rx_rate=excluded.rx_rate,
			retries=excluded.retries,
			satisfaction=excluded.satisfaction,
			user_agent=excluded.user_agent,
			lan_latency=excluded.lan_latency,
			internet_result=excluded.internet_result,
			download=excluded.download,
			upload=excluded.upload,
			stability=excluded.stability,
			assessment=excluded.assessment,
			completed_at=excluded.completed_at
	`,
		sess.ID, sess.CreatedAt.UTC(), sess.ClientIP,
		sess.CorrelationStatus, nullTime(sess.CorrelationAt),
		sess.ClientMAC, sess.ClientHostname, sess.ClientName, sess.UnifiClientID, sess.UAValidation,
		sess.SSID, sess.APMAC, sess.Band, sess.Channel, sess.ChannelWidth, sess.RSSI,
		sess.TXRate, sess.RXRate, sess.Retries, sess.Satisfaction,
		sess.UserAgent,
		string(lanJSON), string(inetJSON), string(dlJSON), string(ulJSON), string(stJSON), string(asJSON),
		nullTime(sess.CompletedAt),
	)
	return err
}

// Get retrieves a single session by ID.
func (s *Store) Get(ctx context.Context, id string) (*DiagSession, error) {
	if s.noop {
		return nil, sql.ErrNoRows
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id=?`, id)
	return scanSession(row)
}

// List returns recent sessions, newest first, up to limit.
func (s *Store) List(ctx context.Context, limit int) ([]*DiagSession, error) {
	if s.noop {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*DiagSession
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// Purge removes sessions older than the retention window and trims to maxSessions.
func (s *Store) Purge(ctx context.Context, retention time.Duration, maxSessions int) error {
	if s.noop {
		return nil
	}

	if retention > 0 {
		cutoff := time.Now().UTC().Add(-retention)
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE created_at < ?`, cutoff)
		if err != nil {
			return err
		}
	}

	if maxSessions > 0 {
		_, err := s.db.ExecContext(ctx, `
			DELETE FROM sessions WHERE id NOT IN (
				SELECT id FROM sessions ORDER BY created_at DESC LIMIT ?
			)`, maxSessions)
		if err != nil {
			return err
		}
	}
	return nil
}

const sessionColumns = `
	id, created_at, client_ip,
	correlation_status, correlation_at,
	client_mac, client_hostname, client_name, unifi_client_id, ua_validation,
	ssid, ap_mac, band, channel, channel_width, rssi, tx_rate, rx_rate, retries, satisfaction,
	user_agent,
	lan_latency, internet_result, download, upload, stability, assessment,
	completed_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanSession(row scanner) (*DiagSession, error) {
	var s DiagSession
	var correlationAt, completedAt sql.NullString
	var lanJSON, inetJSON, dlJSON, ulJSON, stJSON, asJSON sql.NullString

	err := row.Scan(
		&s.ID, &s.CreatedAt, &s.ClientIP,
		&s.CorrelationStatus, &correlationAt,
		&s.ClientMAC, &s.ClientHostname, &s.ClientName, &s.UnifiClientID, &s.UAValidation,
		&s.SSID, &s.APMAC, &s.Band, &s.Channel, &s.ChannelWidth, &s.RSSI,
		&s.TXRate, &s.RXRate, &s.Retries, &s.Satisfaction,
		&s.UserAgent,
		&lanJSON, &inetJSON, &dlJSON, &ulJSON, &stJSON, &asJSON,
		&completedAt,
	)
	if err != nil {
		return nil, err
	}

	if correlationAt.Valid {
		s.CorrelationAt, _ = time.Parse(time.RFC3339Nano, correlationAt.String)
	}
	if completedAt.Valid {
		s.CompletedAt, _ = time.Parse(time.RFC3339Nano, completedAt.String)
	}
	if lanJSON.Valid {
		s.LANLatency = json.RawMessage(lanJSON.String)
	}
	if inetJSON.Valid {
		s.InternetResult = json.RawMessage(inetJSON.String)
	}
	if dlJSON.Valid {
		s.Download = json.RawMessage(dlJSON.String)
	}
	if ulJSON.Valid {
		s.Upload = json.RawMessage(ulJSON.String)
	}
	if stJSON.Valid {
		s.Stability = json.RawMessage(stJSON.String)
	}
	if asJSON.Valid {
		s.Assessment = json.RawMessage(asJSON.String)
	}
	return &s, nil
}

func nullTime(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{Valid: true, String: t.UTC().Format(time.RFC3339Nano)}
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS sessions (
		id                 TEXT PRIMARY KEY,
		created_at         DATETIME NOT NULL,
		client_ip          TEXT NOT NULL,
		correlation_status TEXT NOT NULL DEFAULT 'pending',
		correlation_at     DATETIME,
		client_mac         TEXT,
		client_hostname    TEXT,
		client_name        TEXT,
		unifi_client_id    TEXT,
		ua_validation      TEXT,
		ssid               TEXT,
		ap_mac             TEXT,
		band               TEXT,
		channel            INTEGER,
		channel_width      TEXT,
		rssi               INTEGER,
		tx_rate            INTEGER,
		rx_rate            INTEGER,
		retries            INTEGER,
		satisfaction       INTEGER,
		user_agent         TEXT,
		lan_latency        TEXT,
		internet_result    TEXT,
		download           TEXT,
		upload             TEXT,
		stability          TEXT,
		assessment         TEXT,
		completed_at       DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_created_at ON sessions(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_sessions_client_ip  ON sessions(client_ip);
	`)
	return err
}
