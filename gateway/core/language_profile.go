package core

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"
)

type LangEntry struct {
	Code  string
	Count int
}

type ProfileStore struct {
	db    *atomic.Pointer[sql.DB]
	mu    sync.RWMutex
	cache map[string][]LangEntry
	ttl   time.Duration
	exp   map[string]time.Time
}

func NewProfileStore(db *atomic.Pointer[sql.DB]) *ProfileStore {
	return &ProfileStore{
		db:    db,
		cache: make(map[string][]LangEntry),
		ttl:   5 * time.Minute,
		exp:   make(map[string]time.Time),
	}
}

func (s *ProfileStore) GetProfile(ctx context.Context, deviceHash string) []LangEntry {
	if deviceHash == "" {
		return nil
	}

	s.mu.RLock()
	entries, exp := s.cache[deviceHash], s.exp[deviceHash]
	s.mu.RUnlock()
	if entries != nil && time.Now().Before(exp) {
		if len(entries) == 0 {
			return nil
		}
		return entries
	}

	db := s.db.Load()
	if db == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	rows, err := db.QueryContext(ctx,
		`SELECT language_code, request_count FROM device_languages
         WHERE device_id_hash = ? ORDER BY request_count DESC`,
		deviceHash,
	)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var result []LangEntry
	for rows.Next() {
		var e LangEntry
		if err := rows.Scan(&e.Code, &e.Count); err != nil {
			continue
		}
		result = append(result, e)
	}
	if rows.Err() != nil {
		return nil
	}

	cached := result
	if cached == nil {
		cached = []LangEntry{}
	}
	s.mu.Lock()
	s.cache[deviceHash] = cached
	s.exp[deviceHash] = time.Now().Add(s.ttl)
	s.mu.Unlock()

	if len(result) == 0 {
		return nil
	}
	return result
}

func (s *ProfileStore) RecordLanguage(deviceHash, langCode string) {
	if deviceHash == "" || langCode == "" {
		return
	}
	db := s.db.Load()
	if db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _ = db.ExecContext(ctx,
		`INSERT INTO device_languages (device_id_hash, language_code, request_count, first_seen, last_seen)
         VALUES (?, ?, 1, NOW(), NOW())
         ON DUPLICATE KEY UPDATE request_count = request_count + 1, last_seen = NOW()`,
		deviceHash, langCode,
	)

	s.mu.Lock()
	delete(s.cache, deviceHash)
	delete(s.exp, deviceHash)
	s.mu.Unlock()
}
