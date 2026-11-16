package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// CachedPage stores extracted webpage content and metadata.
type CachedPage struct {
	URL          string `json:"url"`
	Title        string `json:"title"`
	Markdown     string `json:"markdown"`
	HeadingsJSON string `json:"headings_json"`
	MetadataJSON string `json:"metadata_json"`
	SourceMode   string `json:"source_mode"`
	UpdatedAt    int64  `json:"updated_at"`
}

// LocalMatch represents an FTS5 hit from previously fetched pages.
type LocalMatch struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// Store provides persistent SQLite TTL caching and FTS5 local recall.
type Store struct {
	db *sql.DB
}

// Open opens or initializes the SQLite database at dbPath (supports ":memory:").
func Open(dbPath string) (*Store, error) {
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create db dir: %w", err)
			}
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying SQLite connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) initSchema() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA synchronous=NORMAL;`,
		`PRAGMA busy_timeout=5000;`,
		`CREATE TABLE IF NOT EXISTS search_cache (
			cache_key TEXT PRIMARY KEY,
			payload_json TEXT NOT NULL,
			expires_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_search_expires ON search_cache(expires_at);`,
		`CREATE TABLE IF NOT EXISTS page_cache (
			url TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			markdown TEXT NOT NULL,
			headings_json TEXT NOT NULL,
			metadata_json TEXT NOT NULL,
			source_mode TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_page_expires ON page_cache(expires_at);`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS page_fts USING fts5(
			url UNINDEXED,
			title,
			markdown,
			tokenize='unicode61'
		);`,
		`CREATE TABLE IF NOT EXISTS meta_kv (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("init schema (%s): %w", stmt, err)
		}
	}
	return nil
}

// GetSearch retrieves cached search JSON if present and unexpired.
func (s *Store) GetSearch(ctx context.Context, key string) (string, bool, error) {
	now := time.Now().Unix()
	var payload string
	var expiresAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT payload_json, expires_at FROM search_cache WHERE cache_key = ?`, key,
	).Scan(&payload, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if expiresAt <= now {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM search_cache WHERE cache_key = ?`, key)
		return "", false, nil
	}
	return payload, true, nil
}

// PutSearch caches serialized search results with the given TTL.
func (s *Store) PutSearch(ctx context.Context, key, payloadJSON string, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl).Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO search_cache(cache_key, payload_json, expires_at)
		VALUES (?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET
			payload_json = excluded.payload_json,
			expires_at = excluded.expires_at
	`, key, payloadJSON, expiresAt)
	return err
}

// GetPage retrieves a cached webpage if unexpired.
func (s *Store) GetPage(ctx context.Context, pageURL string) (CachedPage, bool, error) {
	now := time.Now().Unix()
	var p CachedPage
	var expiresAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT url, title, markdown, headings_json, metadata_json, source_mode, expires_at, updated_at
		FROM page_cache WHERE url = ?
	`, pageURL).Scan(&p.URL, &p.Title, &p.Markdown, &p.HeadingsJSON, &p.MetadataJSON, &p.SourceMode, &expiresAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CachedPage{}, false, nil
	}
	if err != nil {
		return CachedPage{}, false, err
	}
	if expiresAt <= now {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM page_cache WHERE url = ?`, pageURL)
		_, _ = s.db.ExecContext(ctx, `DELETE FROM page_fts WHERE url = ?`, pageURL)
		return CachedPage{}, false, nil
	}
	return p, true, nil
}

// PutPage stores an extracted webpage and updates its FTS5 index entry.
func (s *Store) PutPage(ctx context.Context, p CachedPage, ttl time.Duration) error {
	now := time.Now().Unix()
	expiresAt := time.Now().Add(ttl).Unix()
	if p.UpdatedAt == 0 {
		p.UpdatedAt = now
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO page_cache(url, title, markdown, headings_json, metadata_json, source_mode, expires_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(url) DO UPDATE SET
			title = excluded.title,
			markdown = excluded.markdown,
			headings_json = excluded.headings_json,
			metadata_json = excluded.metadata_json,
			source_mode = excluded.source_mode,
			expires_at = excluded.expires_at,
			updated_at = excluded.updated_at
	`, p.URL, p.Title, p.Markdown, p.HeadingsJSON, p.MetadataJSON, p.SourceMode, expiresAt, p.UpdatedAt)
	if err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM page_fts WHERE url = ?`, p.URL); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO page_fts(url, title, markdown) VALUES (?, ?, ?)`, p.URL, p.Title, p.Markdown); err != nil {
		return err
	}
	return tx.Commit()
}

// SearchLocalPages queries FTS5 over previously fetched pages using safe quoted tokens.
func (s *Store) SearchLocalPages(ctx context.Context, query string, limit int) ([]LocalMatch, error) {
	if limit <= 0 {
		limit = 5
	}
	ftsQuery := buildSafeFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT url, title, snippet(page_fts, 2, '', '', '...', 24)
		FROM page_fts
		WHERE page_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, ftsQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LocalMatch
	for rows.Next() {
		var m LocalMatch
		if scanErr := rows.Scan(&m.URL, &m.Title, &m.Snippet); scanErr != nil {
			return nil, scanErr
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PurgeExpired removes expired search and page cache entries along with orphaned FTS5 rows.
func (s *Store) PurgeExpired(ctx context.Context) error {
	now := time.Now().Unix()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM search_cache WHERE expires_at <= ?`, now); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM page_cache WHERE expires_at <= ?`, now); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM page_fts WHERE url NOT IN (SELECT url FROM page_cache)`)
	return err
}

// GetMeta retrieves a metadata value from meta_kv.
func (s *Store) GetMeta(ctx context.Context, key string) (string, bool, error) {
	var val string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta_kv WHERE key = ?`, key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

// PutMeta stores or updates a metadata key-value entry in meta_kv.
func (s *Store) PutMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meta_kv(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

func buildSafeFTSQuery(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	var terms []string
	for _, f := range fields {
		clean := strings.Map(func(r rune) rune {
			if r == '"' || r == '*' || r == '(' || r == ')' || r == ':' {
				return -1
			}
			return r
		}, f)
		if clean != "" {
			terms = append(terms, `"`+clean+`"`)
		}
		if len(terms) >= 8 {
			break
		}
	}
	return strings.Join(terms, " AND ")
}
