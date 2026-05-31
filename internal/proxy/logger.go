package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/sen/dns-proxy/internal/config"

	_ "modernc.org/sqlite"
)

type queryLogger interface {
	Log(context.Context, queryLogEntry)
	List(context.Context, int) ([]queryLogEntry, error)
	Close() error
}

type queryLogEntry struct {
	ID           int64     `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	ClientAddr   string    `json:"client_addr"`
	ResolverName string    `json:"resolver_name"`
	Protocol     string    `json:"protocol"`
	ResolverAddr string    `json:"resolver_address"`
	Question     string    `json:"question"`
	QueryType    string    `json:"query_type"`
	RCode        string    `json:"rcode"`
	Answers      []string  `json:"answers"`
	CacheHit     bool      `json:"cache_hit"`
	DurationMS   int64     `json:"duration_ms"`
	Error        string    `json:"error,omitempty"`
}

type noopQueryLogger struct{}

func (noopQueryLogger) Log(context.Context, queryLogEntry)                 {}
func (noopQueryLogger) List(context.Context, int) ([]queryLogEntry, error) { return nil, nil }
func (noopQueryLogger) Close() error                                       { return nil }

type sqliteQueryLogger struct {
	db      *sql.DB
	maxRows int
	mu      sync.Mutex
}

func newQueryLogger(cfg config.LoggingConfig) (queryLogger, error) {
	if !cfg.Enabled {
		return noopQueryLogger{}, nil
	}
	db, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	l := &sqliteQueryLogger{db: db, maxRows: cfg.MaxRows}
	if err := l.init(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return l, nil
}

func (l *sqliteQueryLogger) init(ctx context.Context) error {
	if _, err := l.db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		return err
	}
	_, err := l.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS query_logs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	timestamp TEXT NOT NULL,
	client_addr TEXT NOT NULL,
	resolver_name TEXT NOT NULL,
	protocol TEXT NOT NULL,
	question TEXT NOT NULL,
	query_type TEXT NOT NULL,
	resolver_address TEXT NOT NULL DEFAULT '',
	rcode TEXT NOT NULL,
	answers TEXT NOT NULL,
	cache_hit INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL,
	error TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_query_logs_timestamp ON query_logs(timestamp DESC);
`)
	if err != nil {
		return err
	}
	_, _ = l.db.ExecContext(ctx, `ALTER TABLE query_logs ADD COLUMN resolver_address TEXT NOT NULL DEFAULT ''`)
	return nil
}

func (l *sqliteQueryLogger) Log(ctx context.Context, entry queryLogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	answers, _ := json.Marshal(entry.Answers)
	_, err := l.db.ExecContext(ctx, `
INSERT INTO query_logs (
	timestamp, client_addr, resolver_name, protocol, question, query_type,
	resolver_address, rcode, answers, cache_hit, duration_ms, error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.Timestamp.Format(time.RFC3339Nano),
		entry.ClientAddr,
		entry.ResolverName,
		entry.Protocol,
		entry.Question,
		entry.QueryType,
		entry.ResolverAddr,
		entry.RCode,
		string(answers),
		boolToInt(entry.CacheHit),
		entry.DurationMS,
		entry.Error,
	)
	if err == nil {
		_, _ = l.db.ExecContext(ctx, `
DELETE FROM query_logs
WHERE id NOT IN (
	SELECT id FROM query_logs ORDER BY timestamp DESC, id DESC LIMIT ?
)`, l.maxRows)
	}
}

func (l *sqliteQueryLogger) List(ctx context.Context, limit int) ([]queryLogEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := l.db.QueryContext(ctx, `
SELECT id, timestamp, client_addr, resolver_name, protocol, resolver_address, question, query_type,
	rcode, answers, cache_hit, duration_ms, error
FROM query_logs
ORDER BY timestamp DESC, id DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []queryLogEntry
	for rows.Next() {
		var entry queryLogEntry
		var ts string
		var answers string
		var cacheHit int
		if err := rows.Scan(&entry.ID, &ts, &entry.ClientAddr, &entry.ResolverName, &entry.Protocol, &entry.ResolverAddr, &entry.Question, &entry.QueryType, &entry.RCode, &answers, &cacheHit, &entry.DurationMS, &entry.Error); err != nil {
			return nil, err
		}
		entry.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		_ = json.Unmarshal([]byte(answers), &entry.Answers)
		entry.CacheHit = cacheHit == 1
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (l *sqliteQueryLogger) Close() error {
	return l.db.Close()
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
