package core

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"
)

// langEntry is one (language_code, request_count) row from device_languages.
type langEntry struct {
	Code  string
	Count int
}

// ProfileStore caches per-device language history from the device_languages table.
// Community builds without MariaDB always return nil profiles (graceful degradation).
type ProfileStore struct {
	db    *atomic.Pointer[sql.DB]
	mu    sync.RWMutex
	cache map[string][]langEntry
	ttl   time.Duration
	exp   map[string]time.Time
}

// NewProfileStore creates a ProfileStore backed by the given DB pointer.
// db may point to nil (DB unavailable) — all methods degrade gracefully.
func NewProfileStore(db *atomic.Pointer[sql.DB]) *ProfileStore {
	return &ProfileStore{
		db:    db,
		cache: make(map[string][]langEntry),
		ttl:   5 * time.Minute,
		exp:   make(map[string]time.Time),
	}
}

// GetProfile returns the device's language history, sorted by request_count desc.
// Returns nil (not an error) if no history exists or DB is unavailable.
// Uses a 100ms internal deadline — if the DB is slow, returns nil and lets the
// request fall through to whisper_safe. Never blocks the transcription path.
func (s *ProfileStore) GetProfile(ctx context.Context, deviceHash string) []langEntry {
	if deviceHash == "" {
		return nil
	}

	// Cache check FIRST — warm cache is served even when DB is temporarily unavailable.
	// This prevents a DB hiccup from downgrading all cached EU devices to whisper_safe.
	s.mu.RLock()
	entries, exp := s.cache[deviceHash], s.exp[deviceHash]
	s.mu.RUnlock()
	if entries != nil && time.Now().Before(exp) {
		if len(entries) == 0 {
			return nil
		}
		return entries
	}

	// Cache miss — need DB. If unavailable, return nil (safe Tier 1 fallback).
	db := s.db.Load()
	if db == nil {
		return nil
	}

	// Fetch from DB with a tight deadline.
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
	defer rows.Close()

	var result []langEntry
	for rows.Next() {
		var e langEntry
		if err := rows.Scan(&e.Code, &e.Count); err != nil {
			continue
		}
		result = append(result, e)
	}
	if rows.Err() != nil {
		return nil
	}

	// Write through to cache — nil result (no history) is also cached to avoid
	// repeated DB hits for brand-new devices. A nil slice stays nil in the map,
	// so we store an empty non-nil slice as the "no history" sentinel.
	cached := result
	if cached == nil {
		cached = []langEntry{}
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

// RecordLanguage upserts (deviceHash, langCode) into device_languages.
// Safe to call as a goroutine — uses a 2s internal deadline.
// Silently no-ops if DB is unavailable.
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

	// Invalidate cache entry so the next GetProfile reflects the new count.
	s.mu.Lock()
	delete(s.cache, deviceHash)
	delete(s.exp, deviceHash)
	s.mu.Unlock()
}
