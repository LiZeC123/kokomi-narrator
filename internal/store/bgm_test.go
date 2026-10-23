package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestBgmTrackCacheAndPrune(t *testing.T) {
	st := openTestStore(t)
	dir := t.TempDir()
	otherDir := t.TempDir()

	a := BgmTrack{
		Path:       filepath.Join(dir, "a.mp3"),
		Dir:        dir,
		Filename:   "a.mp3",
		DurationMS: 1000,
		Size:       10,
		MtimeNS:    1,
		UpdatedAt:  "2026-01-01T00:00:00Z",
	}
	b := BgmTrack{
		Path:       filepath.Join(dir, "b.mp3"),
		Dir:        dir,
		Filename:   "b.mp3",
		DurationMS: 2000,
		Size:       20,
		MtimeNS:    2,
		UpdatedAt:  "2026-01-01T00:00:00Z",
	}
	other := BgmTrack{
		Path:       filepath.Join(otherDir, "c.mp3"),
		Dir:        otherDir,
		Filename:   "c.mp3",
		DurationMS: 3000,
		Size:       30,
		MtimeNS:    3,
		UpdatedAt:  "2026-01-01T00:00:00Z",
	}
	if err := st.UpsertBgmTracks([]BgmTrack{a, b, other}); err != nil {
		t.Fatalf("UpsertBgmTracks() error = %v", err)
	}

	got, err := st.BgmTracksByDir(dir)
	if err != nil {
		t.Fatalf("BgmTracksByDir() error = %v", err)
	}
	if len(got) != 2 || got[0].DurationMS != 1000 || got[1].DurationMS != 2000 {
		t.Fatalf("BgmTracksByDir() = %+v, want a and b", got)
	}

	a.DurationMS = 1500
	a.Size = 11
	a.MtimeNS = 4
	a.UpdatedAt = "2026-01-02T00:00:00Z"
	if err := st.UpsertBgmTracks([]BgmTrack{a}); err != nil {
		t.Fatalf("UpsertBgmTracks(update) error = %v", err)
	}
	got, err = st.BgmTracksByDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].DurationMS != 1500 || got[0].Size != 11 || got[0].MtimeNS != 4 {
		t.Fatalf("updated track = %+v", got[0])
	}

	removed, err := st.PruneBgmTracks(dir, []string{a.Path})
	if err != nil {
		t.Fatalf("PruneBgmTracks() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("PruneBgmTracks() removed = %d, want 1", removed)
	}
	got, err = st.BgmTracksByDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != a.Path {
		t.Fatalf("remaining tracks = %+v, want only a", got)
	}
	otherGot, err := st.BgmTracksByDir(otherDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherGot) != 1 {
		t.Fatalf("other dir tracks = %+v, want c", otherGot)
	}
}

func TestSchemaMigrationFromV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tts.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
		`INSERT INTO schema_version(version) VALUES (1)`,
		`CREATE TABLE files (
			id INTEGER PRIMARY KEY,
			filename TEXT NOT NULL UNIQUE,
			voice_key TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE segments (
			id INTEGER PRIMARY KEY,
			file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
			seq INTEGER NOT NULL,
			text TEXT NOT NULL,
			text_hash TEXT NOT NULL,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			error TEXT,
			attempts INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL,
			UNIQUE(file_id, seq)
		)`,
		`INSERT INTO files(filename, voice_key, content_hash, created_at, updated_at)
		 VALUES ('old.txt', 'voice', 'hash', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			t.Fatalf("prepare v1 database: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open() after v1 migration error = %v", err)
	}
	defer st.Close()

	var version int
	if err := st.db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM bgm_tracks`).Scan(&count); err != nil {
		t.Fatalf("bgm_tracks table is missing: %v", err)
	}
	if _, err := st.FileByName("old.txt"); err != nil {
		t.Fatalf("existing file was not preserved: %v", err)
	}
}
