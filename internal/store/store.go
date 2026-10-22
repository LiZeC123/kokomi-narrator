package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
)

const schemaVersion = 1

const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusSuccess    = "success"
	StatusFailed     = "failed"
	StatusSkipped    = "skipped"
)

var ErrNotFound = errors.New("file not found")

type File struct {
	ID          int64
	Filename    string
	VoiceKey    string
	ContentHash string
	CreatedAt   string
	UpdatedAt   string
}

type Segment struct {
	ID        int64
	FileID    int64
	Seq       int
	Text      string
	TextHash  string
	Kind      textparse.Kind
	Status    string
	Error     *string
	Attempts  int
	UpdatedAt string
}

type SyncUpdate struct {
	ID     int64
	Status string
}

type UpdateStats struct {
	Added   int
	Reused  int
	Deleted int
}

type Ref struct {
	VoiceKey string
	TextHash string
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create database directory: %w", err)
			}
		}
	}

	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	if path == ":memory:" {
		dsn = "file::memory:?cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// This is a single-user CLI; one connection keeps the PRAGMAs and writes serialized.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := initSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func initSchema(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS files (
			id            INTEGER PRIMARY KEY,
			filename      TEXT NOT NULL UNIQUE,
			voice_key     TEXT NOT NULL,
			content_hash  TEXT NOT NULL,
			created_at    TEXT NOT NULL,
			updated_at    TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS segments (
			id          INTEGER PRIMARY KEY,
			file_id     INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
			seq         INTEGER NOT NULL,
			text        TEXT NOT NULL,
			text_hash   TEXT NOT NULL,
			kind        TEXT NOT NULL CHECK(kind IN ('speech', 'punctuation')),
			status      TEXT NOT NULL CHECK(status IN (
				'pending', 'processing', 'success', 'failed', 'skipped'
			)),
			error       TEXT,
			attempts    INTEGER NOT NULL DEFAULT 0,
			updated_at  TEXT NOT NULL,
			UNIQUE(file_id, seq)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_segments_file_status ON segments(file_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_segments_hash ON segments(text_hash)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("initialize database schema: %w", err)
		}
	}

	var version int
	err := db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := db.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion); err != nil {
			return fmt.Errorf("initialize schema version: %w", err)
		}
	case err != nil:
		return fmt.Errorf("read schema version: %w", err)
	case version != schemaVersion:
		return fmt.Errorf("unsupported database schema version %d", version)
	}
	return nil
}

func (s *Store) FileByName(name string) (*File, error) {
	row := s.db.QueryRow(
		`SELECT id, filename, voice_key, content_hash, created_at, updated_at
		 FROM files WHERE filename = ?`, name)

	var f File
	if err := row.Scan(&f.ID, &f.Filename, &f.VoiceKey, &f.ContentHash, &f.CreatedAt, &f.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return nil, err
	}
	return &f, nil
}

func (s *Store) SegmentsByFile(fileID int64) ([]Segment, error) {
	rows, err := s.db.Query(
		`SELECT id, file_id, seq, text, text_hash, kind, status, error, attempts, updated_at
		 FROM segments WHERE file_id = ? ORDER BY seq`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var segments []Segment
	for rows.Next() {
		seg, err := scanSegment(rows)
		if err != nil {
			return nil, err
		}
		segments = append(segments, seg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return segments, nil
}

func (s *Store) SegmentBySeq(fileID int64, seq int) (*Segment, error) {
	row := s.db.QueryRow(
		`SELECT id, file_id, seq, text, text_hash, kind, status, error, attempts, updated_at
		 FROM segments WHERE file_id = ? AND seq = ?`, fileID, seq)

	seg, err := scanSegment(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, err
	}
	return &seg, nil
}

func scanSegment(scanner interface{ Scan(dest ...any) error }) (Segment, error) {
	var (
		seg     Segment
		kind    string
		errText sql.NullString
	)
	if err := scanner.Scan(
		&seg.ID, &seg.FileID, &seg.Seq, &seg.Text, &seg.TextHash,
		&kind, &seg.Status, &errText, &seg.Attempts, &seg.UpdatedAt,
	); err != nil {
		return Segment{}, err
	}
	seg.Kind = textparse.Kind(kind)
	if errText.Valid {
		msg := errText.String
		seg.Error = &msg
	}
	return seg, nil
}

func (s *Store) CreateFile(f *File, segments []textparse.Segment) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO files(filename, voice_key, content_hash, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		f.Filename, f.VoiceKey, f.ContentHash, f.CreatedAt, f.UpdatedAt)
	if err != nil {
		return err
	}
	f.ID, err = res.LastInsertId()
	if err != nil {
		return err
	}

	for _, seg := range segments {
		status := StatusPending
		if seg.Kind == textparse.KindPunctuation {
			status = StatusSkipped
		}
		if _, err := tx.Exec(
			`INSERT INTO segments(file_id, seq, text, text_hash, kind, status, error, attempts, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, NULL, 0, ?)`,
			f.ID, seg.Seq, seg.Text, seg.Hash, string(seg.Kind), status, f.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpdateFileSegments(fileID int64, contentHash string, segments []textparse.Segment, now string) (UpdateStats, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return UpdateStats{}, err
	}
	defer tx.Rollback()

	type oldSegment struct {
		ID       int64
		Seq      int
		Hash     string
		Kind     textparse.Kind
		Status   string
		Error    *string
		Attempts int
	}

	rows, err := tx.Query(
		`SELECT id, seq, text_hash, kind, status, error, attempts
		 FROM segments WHERE file_id = ? ORDER BY seq`, fileID)
	if err != nil {
		return UpdateStats{}, err
	}
	var old []oldSegment
	for rows.Next() {
		var (
			seg     oldSegment
			kind    string
			errText sql.NullString
		)
		if err := rows.Scan(&seg.ID, &seg.Seq, &seg.Hash, &kind, &seg.Status, &errText, &seg.Attempts); err != nil {
			rows.Close()
			return UpdateStats{}, err
		}
		seg.Kind = textparse.Kind(kind)
		if errText.Valid {
			msg := errText.String
			seg.Error = &msg
		}
		old = append(old, seg)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return UpdateStats{}, err
	}
	rows.Close()

	// Move every old row out of the way so new seq values can be assigned freely.
	for i := range old {
		if _, err := tx.Exec(`UPDATE segments SET seq = ? WHERE id = ?`, -(i + 1), old[i].ID); err != nil {
			return UpdateStats{}, err
		}
	}

	used := make([]bool, len(old))
	var stats UpdateStats

	for _, seg := range segments {
		match := -1
		for i := range old {
			if !used[i] && old[i].Hash == seg.Hash && old[i].Seq == seg.Seq {
				match = i
				break
			}
		}
		if match == -1 {
			for i := range old {
				if !used[i] && old[i].Hash == seg.Hash {
					match = i
					break
				}
			}
		}

		if match >= 0 {
			used[match] = true
			o := old[match]
			status := o.Status
			errText := o.Error
			if seg.Kind == textparse.KindPunctuation {
				status = StatusSkipped
				errText = nil
			} else if o.Kind == textparse.KindPunctuation {
				status = StatusPending
				errText = nil
			}

			if _, err := tx.Exec(
				`UPDATE segments
				 SET seq = ?, text = ?, text_hash = ?, kind = ?, status = ?, error = ?, updated_at = ?
				 WHERE id = ?`,
				seg.Seq, seg.Text, seg.Hash, string(seg.Kind), status, nullableString(errText), now, o.ID); err != nil {
				return UpdateStats{}, err
			}
			stats.Reused++
			continue
		}

		status := StatusPending
		if seg.Kind == textparse.KindPunctuation {
			status = StatusSkipped
		}
		if _, err := tx.Exec(
			`INSERT INTO segments(file_id, seq, text, text_hash, kind, status, error, attempts, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, NULL, 0, ?)`,
			fileID, seg.Seq, seg.Text, seg.Hash, string(seg.Kind), status, now); err != nil {
			return UpdateStats{}, err
		}
		stats.Added++
	}

	res, err := tx.Exec(`DELETE FROM segments WHERE file_id = ? AND seq < 0`, fileID)
	if err != nil {
		return UpdateStats{}, err
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return UpdateStats{}, err
	}
	stats.Deleted = int(deleted)

	res, err = tx.Exec(`UPDATE files SET content_hash = ?, updated_at = ? WHERE id = ?`, contentHash, now, fileID)
	if err != nil {
		return UpdateStats{}, err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return UpdateStats{}, fmt.Errorf("file id %d no longer exists", fileID)
	}

	return stats, tx.Commit()
}

func (s *Store) SetHashStatus(fileID int64, textHash, status string, errorMessage *string, incrementAttempts bool, now string) (int64, error) {
	attempts := 0
	if incrementAttempts {
		attempts = 1
	}
	res, err := s.db.Exec(
		`UPDATE segments
		 SET status = ?, error = ?, updated_at = ?, attempts = attempts + ?
		 WHERE file_id = ? AND text_hash = ? AND kind = 'speech'`,
		status, nullableString(errorMessage), now, attempts, fileID, textHash)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RecoverProcessing turns processing segments back into pending. An empty hash
// resets every processing segment of the file.
func (s *Store) RecoverProcessing(fileID int64, textHash, now string) (int64, error) {
	query := `UPDATE segments SET status = ?, error = NULL, updated_at = ?
	          WHERE file_id = ? AND status = ?`
	args := []any{StatusPending, now, fileID, StatusProcessing}
	if textHash != "" {
		query += ` AND text_hash = ?`
		args = append(args, textHash)
	}
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) SwitchVoice(fileID int64, voiceKey, now string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE files SET voice_key = ?, updated_at = ? WHERE id = ?`, voiceKey, now, fileID); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE segments
		 SET status = ?, error = NULL, attempts = 0, updated_at = ?
		 WHERE file_id = ? AND kind = 'speech'`,
		StatusPending, now, fileID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ApplySync(fileID int64, updates []SyncUpdate, now string) error {
	if len(updates) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`UPDATE segments SET status = ?, error = NULL, updated_at = ? WHERE id = ? AND file_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, update := range updates {
		if _, err := stmt.Exec(update.Status, now, update.ID, fileID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) References() (map[Ref]struct{}, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT f.voice_key, s.text_hash
		 FROM segments s
		 JOIN files f ON f.id = s.file_id
		 WHERE s.kind = 'speech'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	refs := make(map[Ref]struct{})
	for rows.Next() {
		var ref Ref
		if err := rows.Scan(&ref.VoiceKey, &ref.TextHash); err != nil {
			return nil, err
		}
		refs[ref] = struct{}{}
	}
	return refs, rows.Err()
}

func nullableString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
