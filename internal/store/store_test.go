package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "tts.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func makeFile(t *testing.T, name, text string) (*Store, *File, []textparse.Segment) {
	t.Helper()
	st := openTestStore(t)
	segments, err := textparse.Parse([]byte(text))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	now := "2026-01-01T00:00:00Z"
	f := &File{
		Filename:    name,
		VoiceKey:    "0123456789abcdef",
		ContentHash: textparse.HashBytes([]byte(text)),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := st.CreateFile(f, segments); err != nil {
		t.Fatalf("CreateFile() error = %v", err)
	}
	return st, f, segments
}

func TestCreateFileAndSegments(t *testing.T) {
	st, f, _ := makeFile(t, "a.txt", "子1\n，\n子2\n")

	got, err := st.FileByName("a.txt")
	if err != nil {
		t.Fatalf("FileByName() error = %v", err)
	}
	if got.ID != f.ID || got.VoiceKey != f.VoiceKey {
		t.Fatalf("FileByName() = %+v, want %+v", got, f)
	}
	if _, err := st.FileByName("missing.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FileByName(missing) error = %v, want ErrNotFound", err)
	}

	segments, err := st.SegmentsByFile(f.ID)
	if err != nil {
		t.Fatalf("SegmentsByFile() error = %v", err)
	}
	if len(segments) != 3 {
		t.Fatalf("segments = %d, want 3", len(segments))
	}
	if segments[0].Status != StatusPending || segments[1].Status != StatusSkipped || segments[0].Kind != textparse.KindSpeech {
		t.Fatalf("unexpected initial statuses: %+v", segments)
	}
}

func TestUpdateFileSegmentsPreservesMatchingState(t *testing.T) {
	st, f, _ := makeFile(t, "a.txt", "子1\n子2\n，\n子3\n")
	now := "2026-01-02T00:00:00Z"

	if _, err := st.SetHashStatus(f.ID, textparse.HashText("子2"), StatusSuccess, nil, true, now); err != nil {
		t.Fatalf("SetHashStatus() error = %v", err)
	}

	updated, err := textparse.Parse([]byte("子3\n，\n子2\n子4\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	stats, err := st.UpdateFileSegments(f.ID, "new-content-hash", updated, now)
	if err != nil {
		t.Fatalf("UpdateFileSegments() error = %v", err)
	}
	want := UpdateStats{Added: 1, Reused: 3, Deleted: 1}
	if stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}

	segments, err := st.SegmentsByFile(f.ID)
	if err != nil {
		t.Fatalf("SegmentsByFile() error = %v", err)
	}
	if len(segments) != 4 {
		t.Fatalf("segments = %d, want 4", len(segments))
	}
	for i, seg := range segments {
		if seg.Seq != i+1 {
			t.Errorf("segments[%d].Seq = %d, want %d", i, seg.Seq, i+1)
		}
	}
	if segments[2].Text != "子2" || segments[2].Status != StatusSuccess {
		t.Fatalf("reused success segment = %+v, want 子2/success at seq 3", segments[2])
	}
	if segments[1].Status != StatusSkipped || segments[1].Error != nil {
		t.Fatalf("punctuation segment = %+v, want skipped without error", segments[1])
	}

	file, err := st.FileByName("a.txt")
	if err != nil {
		t.Fatalf("FileByName() error = %v", err)
	}
	if file.ContentHash != "new-content-hash" || file.UpdatedAt != now {
		t.Fatalf("file metadata = %+v", file)
	}

	refs, err := st.References()
	if err != nil {
		t.Fatalf("References() error = %v", err)
	}
	for _, seg := range segments {
		if seg.Kind != textparse.KindSpeech {
			continue
		}
		key := Ref{VoiceKey: f.VoiceKey, TextHash: seg.TextHash}
		if _, ok := refs[key]; !ok {
			t.Errorf("missing reference %+v", key)
		}
	}
}

func TestConnectionPragmas(t *testing.T) {
	st := openTestStore(t)

	var foreignKeys int
	if err := st.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}

	var busyTimeout int
	if err := st.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busyTimeout)
	}
}
