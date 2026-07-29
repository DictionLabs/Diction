package core

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// --- fakeDB: minimal sql.Driver for langprofile tests without a real DB ---

type fakeDriver struct {
	queryRows [][]driver.Value
	queryErr  error // if set, Query returns this error
	iterErr   error // if set, rows.Next returns this after last row (instead of EOF)
}

func (d *fakeDriver) Open(_ string) (driver.Conn, error) {
	return &fakeConn{d: d}, nil
}

type fakeConn struct{ d *fakeDriver }

func (c *fakeConn) Prepare(_ string) (driver.Stmt, error) {
	return &fakeStmt{d: c.d}, nil
}
func (c *fakeConn) Close() error              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return &fakeTx{}, nil }

type fakeTx struct{}

func (t *fakeTx) Commit() error   { return nil }
func (t *fakeTx) Rollback() error { return nil }

type fakeStmt struct{ d *fakeDriver }

func (s *fakeStmt) Close() error                                 { return nil }
func (s *fakeStmt) NumInput() int                                { return -1 }
func (s *fakeStmt) Exec(_ []driver.Value) (driver.Result, error) { return &fakeResult{}, nil }
func (s *fakeStmt) Query(_ []driver.Value) (driver.Rows, error) {
	if s.d.queryErr != nil {
		return nil, s.d.queryErr
	}
	return &fakeRows{data: s.d.queryRows, iterErr: s.d.iterErr}, nil
}

type fakeRows struct {
	data    [][]driver.Value
	iterErr error // returned when data is exhausted (instead of io.EOF when non-nil)
	idx     int
}

func (r *fakeRows) Columns() []string { return []string{"language_code", "request_count"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.data) {
		if r.iterErr != nil {
			return r.iterErr
		}
		return io.EOF
	}
	copy(dest, r.data[r.idx])
	r.idx++
	return nil
}

type fakeResult struct{}

func (r *fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r *fakeResult) RowsAffected() (int64, error) { return 1, nil }

// openFakeDB creates a *sql.DB backed by the fakeDriver with the given rows.
// Each inner slice is one row: [language_code, request_count].
func openFakeDB(t *testing.T, rows [][]driver.Value) *sql.DB {
	t.Helper()
	return openFakeDBWith(t, &fakeDriver{queryRows: rows})
}

func openFakeDBWith(t *testing.T, d *fakeDriver) *sql.DB {
	t.Helper()
	name := "fakedb-" + t.Name()
	sql.Register(name, d)
	db, err := sql.Open(name, "test")
	if err != nil {
		t.Fatalf("open fakedb: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// --- ProfileStore nil-guard paths (no DB required) ---

func TestNewProfileStore_Basic(t *testing.T) {
	var dbPtr atomic.Pointer[sql.DB]
	store := NewProfileStore(&dbPtr)
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestGetProfile_EmptyDeviceHash(t *testing.T) {
	var dbPtr atomic.Pointer[sql.DB]
	store := NewProfileStore(&dbPtr)
	if got := store.GetProfile(context.Background(), ""); got != nil {
		t.Errorf("expected nil for empty deviceHash, got %v", got)
	}
}

func TestGetProfile_DBUnavailable(t *testing.T) {
	var dbPtr atomic.Pointer[sql.DB]
	store := NewProfileStore(&dbPtr)
	if got := store.GetProfile(context.Background(), "abc123"); got != nil {
		t.Errorf("expected nil when DB unavailable, got %v", got)
	}
}

func TestRecordLanguage_EmptyDeviceHash(t *testing.T) {
	var dbPtr atomic.Pointer[sql.DB]
	store := NewProfileStore(&dbPtr)
	store.RecordLanguage("", "en")
}

func TestRecordLanguage_EmptyLangCode(t *testing.T) {
	var dbPtr atomic.Pointer[sql.DB]
	store := NewProfileStore(&dbPtr)
	store.RecordLanguage("hash123", "")
}

func TestRecordLanguage_DBUnavailable(t *testing.T) {
	var dbPtr atomic.Pointer[sql.DB]
	store := NewProfileStore(&dbPtr)
	store.RecordLanguage("hash123", "cs")
}

func TestGetProfile_CacheHitNonEmpty(t *testing.T) {
	var dbPtr atomic.Pointer[sql.DB]
	store := NewProfileStore(&dbPtr)
	store.mu.Lock()
	store.cache["h1"] = []langEntry{{Code: "cs", Count: 3}}
	store.exp["h1"] = time.Now().Add(time.Minute)
	store.mu.Unlock()

	got := store.GetProfile(context.Background(), "h1")
	if len(got) != 1 || got[0].Code != "cs" {
		t.Errorf("expected cache hit with cs, got %v", got)
	}
}

func TestGetProfile_CacheHitEmptySentinel(t *testing.T) {
	var dbPtr atomic.Pointer[sql.DB]
	store := NewProfileStore(&dbPtr)
	store.mu.Lock()
	store.cache["h2"] = []langEntry{} // empty sentinel = no history
	store.exp["h2"] = time.Now().Add(time.Minute)
	store.mu.Unlock()

	if got := store.GetProfile(context.Background(), "h2"); got != nil {
		t.Errorf("expected nil for empty sentinel, got %v", got)
	}
}

func TestGetProfile_CacheExpiredFallsThrough(t *testing.T) {
	var dbPtr atomic.Pointer[sql.DB]
	store := NewProfileStore(&dbPtr)
	store.mu.Lock()
	store.cache["h3"] = []langEntry{{Code: "cs", Count: 3}}
	store.exp["h3"] = time.Now().Add(-time.Minute) // already expired
	store.mu.Unlock()

	// DB nil → returns nil after cache miss
	if got := store.GetProfile(context.Background(), "h3"); got != nil {
		t.Errorf("expected nil when cache expired and DB unavailable, got %v", got)
	}
}

// --- ProfileStore with fake DB ---

func TestGetProfile_WithFakeDB_ReturnsRows(t *testing.T) {
	rows := [][]driver.Value{{"cs", int64(5)}, {"sk", int64(2)}}
	db := openFakeDB(t, rows)
	var dbPtr atomic.Pointer[sql.DB]
	dbPtr.Store(db)
	store := NewProfileStore(&dbPtr)

	got := store.GetProfile(context.Background(), "devicehash1")
	if len(got) != 2 || got[0].Code != "cs" || got[0].Count != 5 {
		t.Errorf("expected [{cs 5} {sk 2}], got %v", got)
	}
}

func TestGetProfile_WithFakeDB_EmptyResult(t *testing.T) {
	db := openFakeDB(t, nil) // no rows
	var dbPtr atomic.Pointer[sql.DB]
	dbPtr.Store(db)
	store := NewProfileStore(&dbPtr)

	// First call: DB query returns empty → nil
	if got := store.GetProfile(context.Background(), "devicehash2"); got != nil {
		t.Errorf("expected nil for empty DB result, got %v", got)
	}
	// Second call: served from cache (empty sentinel)
	if got := store.GetProfile(context.Background(), "devicehash2"); got != nil {
		t.Errorf("expected nil from cache on second call, got %v", got)
	}
}

func TestGetProfile_CacheWriteThrough(t *testing.T) {
	rows := [][]driver.Value{{"de", int64(7)}}
	db := openFakeDB(t, rows)
	var dbPtr atomic.Pointer[sql.DB]
	dbPtr.Store(db)
	store := NewProfileStore(&dbPtr)

	_ = store.GetProfile(context.Background(), "devicehash3")

	// Verify cache was written
	store.mu.RLock()
	cached, ok := store.cache["devicehash3"]
	store.mu.RUnlock()
	if !ok || len(cached) != 1 || cached[0].Code != "de" {
		t.Errorf("expected cache to contain de entry, got %v", cached)
	}
}

func TestGetProfile_QueryError(t *testing.T) {
	db := openFakeDBWith(t, &fakeDriver{queryErr: fmt.Errorf("simulated query error")})
	var dbPtr atomic.Pointer[sql.DB]
	dbPtr.Store(db)
	store := NewProfileStore(&dbPtr)

	if got := store.GetProfile(context.Background(), "device-qerr"); got != nil {
		t.Errorf("expected nil on query error, got %v", got)
	}
}

func TestGetProfile_ScanError(t *testing.T) {
	// Return string "bad" for int count → scan conversion fails → entry skipped → empty result
	rows := [][]driver.Value{{"cs", "bad-count"}}
	db := openFakeDB(t, rows)
	var dbPtr atomic.Pointer[sql.DB]
	dbPtr.Store(db)
	store := NewProfileStore(&dbPtr)

	if got := store.GetProfile(context.Background(), "device-scan-err"); got != nil {
		t.Errorf("expected nil when all scans fail, got %v", got)
	}
}

func TestGetProfile_IterationError(t *testing.T) {
	// iterErr is returned after all rows, causing rows.Err() != nil
	db := openFakeDBWith(t, &fakeDriver{
		queryRows: [][]driver.Value{{"cs", int64(3)}},
		iterErr:   fmt.Errorf("simulated iteration error"),
	})
	var dbPtr atomic.Pointer[sql.DB]
	dbPtr.Store(db)
	store := NewProfileStore(&dbPtr)

	if got := store.GetProfile(context.Background(), "device-iter-err"); got != nil {
		t.Errorf("expected nil on rows iteration error, got %v", got)
	}
}

func TestRecordLanguage_WithFakeDB(t *testing.T) {
	db := openFakeDB(t, nil)
	var dbPtr atomic.Pointer[sql.DB]
	dbPtr.Store(db)
	store := NewProfileStore(&dbPtr)

	// Pre-populate cache to verify it gets invalidated
	store.mu.Lock()
	store.cache["device1"] = []langEntry{{Code: "cs", Count: 3}}
	store.exp["device1"] = time.Now().Add(time.Minute)
	store.mu.Unlock()

	store.RecordLanguage("device1", "sk")

	// Cache entry should be invalidated
	store.mu.RLock()
	_, ok := store.cache["device1"]
	store.mu.RUnlock()
	if ok {
		t.Error("expected cache entry to be invalidated after RecordLanguage")
	}
}

// --- dominantLang ---

func TestDominantLang_ConfidentSingleLanguage(t *testing.T) {
	// ≥5 obs, 100% → confident
	entries := []langEntry{{Code: "cs", Count: 5}}
	if got := dominantLang(entries, 5, 0.90); got != "cs" {
		t.Errorf("got %q, want cs", got)
	}
}

func TestDominantLang_BelowCountThreshold(t *testing.T) {
	// 4 obs < minCount 5 → ""
	entries := []langEntry{{Code: "cs", Count: 4}}
	if got := dominantLang(entries, 5, 0.90); got != "" {
		t.Errorf("got %q, want \"\"", got)
	}
}

func TestDominantLang_BelowPctThreshold(t *testing.T) {
	// 5 obs but split 70/30 → below 90% → ""
	entries := []langEntry{{Code: "cs", Count: 7}, {Code: "sk", Count: 3}}
	if got := dominantLang(entries, 5, 0.90); got != "" {
		t.Errorf("got %q, want \"\"", got)
	}
}

func TestDominantLang_ExactlyAtThresholds(t *testing.T) {
	// Exactly 5 obs, exactly 90% → just passes
	entries := []langEntry{{Code: "de", Count: 9}, {Code: "fr", Count: 1}}
	if got := dominantLang(entries, 5, 0.90); got != "de" {
		t.Errorf("got %q, want de", got)
	}
}

func TestDominantLang_Empty(t *testing.T) {
	if got := dominantLang(nil, 5, 0.90); got != "" {
		t.Errorf("got %q, want \"\"", got)
	}
}

func TestDominantLang_ZeroTotal(t *testing.T) {
	// minCount=0 so top.Count check passes, but total sums to 0 → ""
	entries := []langEntry{{Code: "cs", Count: 0}}
	if got := dominantLang(entries, 0, 0.90); got != "" {
		t.Errorf("got %q, want \"\"", got)
	}
}

// --- euProfile ---

func TestEuProfile_AllEU(t *testing.T) {
	entries := []langEntry{{Code: "cs", Count: 3}, {Code: "sk", Count: 2}}
	allEU, has := euProfile(entries)
	if !allEU || !has {
		t.Errorf("got (%v,%v), want (true,true)", allEU, has)
	}
}

func TestEuProfile_MixedWithNonEU(t *testing.T) {
	entries := []langEntry{{Code: "cs", Count: 3}, {Code: "zh", Count: 2}}
	allEU, has := euProfile(entries)
	if allEU || !has {
		t.Errorf("got (%v,%v), want (false,true)", allEU, has)
	}
}

func TestEuProfile_Empty(t *testing.T) {
	allEU, has := euProfile(nil)
	if allEU || has {
		t.Errorf("got (%v,%v), want (false,false)", allEU, has)
	}
}

func TestEuProfile_SingleNonEU(t *testing.T) {
	entries := []langEntry{{Code: "ja", Count: 5}}
	allEU, has := euProfile(entries)
	if allEU || !has {
		t.Errorf("got (%v,%v), want (false,true)", allEU, has)
	}
}
