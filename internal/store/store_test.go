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

func TestFileByIDAndListFiles(t *testing.T) {
	st := openTestStore(t)
	now := "2026-01-03T00:00:00Z"

	create := func(name, text string) *File {
		t.Helper()
		segments, err := textparse.Parse([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		f := &File{
			Filename:    name,
			VoiceKey:    "0123456789abcdef",
			ContentHash: textparse.HashBytes([]byte(text)),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := st.CreateFile(f, segments); err != nil {
			t.Fatal(err)
		}
		return f
	}

	first := create("a.txt", "一\n二\n三\n")
	second := create("b.txt", "四\n")

	got, err := st.FileByID(second.ID)
	if err != nil {
		t.Fatalf("FileByID() error = %v", err)
	}
	if got.Filename != second.Filename || got.ID != second.ID {
		t.Fatalf("FileByID() = %+v, want %+v", got, second)
	}
	if _, err := st.FileByID(9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FileByID(9999) error = %v, want ErrNotFound", err)
	}

	files, err := st.ListFiles()
	if err != nil {
		t.Fatalf("ListFiles() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("ListFiles() returned %d files, want 2", len(files))
	}
	if files[0].ID != first.ID || files[0].Segments != 3 {
		t.Fatalf("files[0] = %+v, want id=%d segments=3", files[0], first.ID)
	}
	if files[1].ID != second.ID || files[1].Segments != 1 {
		t.Fatalf("files[1] = %+v, want id=%d segments=1", files[1], second.ID)
	}
}

func TestSpeechRefsSkipsPunctuation(t *testing.T) {
	st := openTestStore(t)
	now := "2026-01-01T00:00:00Z"
	segments, err := textparse.Parse([]byte("子1\n，\n子2\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	f := &File{
		Filename:    "a.txt",
		VoiceKey:    "0123456789abcdef",
		ContentHash: textparse.HashBytes([]byte("子1\n，\n子2\n")),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := st.CreateFile(f, segments); err != nil {
		t.Fatalf("CreateFile() error = %v", err)
	}

	refs, err := st.SpeechRefs()
	if err != nil {
		t.Fatalf("SpeechRefs() error = %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("refs = %d, want 2: %+v", len(refs), refs)
	}
	if refs[0].FileID != f.ID || refs[0].Filename != "a.txt" || refs[0].VoiceKey != f.VoiceKey {
		t.Fatalf("refs[0] = %+v", refs[0])
	}
	if refs[0].Seq != 1 || refs[1].Seq != 3 {
		t.Fatalf("seqs = %d/%d, want 1/3", refs[0].Seq, refs[1].Seq)
	}
	if refs[0].TextHash != textparse.HashText("子1") || refs[1].TextHash != textparse.HashText("子2") {
		t.Fatalf("hashes = %s/%s", refs[0].TextHash, refs[1].TextHash)
	}
}
