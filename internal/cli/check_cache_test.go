package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LiZeC123/kokomi-narrator/internal/silencedetect"
	"github.com/LiZeC123/kokomi-narrator/internal/store"
)

func TestParseCheckCacheLine(t *testing.T) {
	hash := strings.Repeat("ab", 32)

	entry, ok := parseCheckCacheLine("1\t42\t" + hash + "\t3.03s-6.86s (3.84s); 10.00s-12.72s (2.72s)")
	if !ok {
		t.Fatal("tab-separated line was rejected")
	}
	if entry.FileID != 1 || entry.Seq != 42 || entry.TextHash != hash {
		t.Fatalf("entry = %+v", entry)
	}
	if !strings.Contains(entry.Details, "3.03s-6.86s") || !strings.Contains(entry.Details, "10.00s-12.72s") {
		t.Fatalf("details = %q", entry.Details)
	}

	entry, ok = parseCheckCacheLine("2 7 " + hash + " 1.00s-2.00s (1.00s)")
	if !ok || entry.FileID != 2 || entry.Seq != 7 || entry.Details != "1.00s-2.00s (1.00s)" {
		t.Fatalf("space-separated entry = %+v, ok = %v", entry, ok)
	}

	entry, ok = parseCheckCacheLine("3\t9\t" + hash)
	if !ok || entry.FileID != 3 || entry.Seq != 9 || entry.Details != "" {
		t.Fatalf("three-field entry = %+v, ok = %v", entry, ok)
	}

	for _, line := range []string{
		"",
		"# comment",
		"0\t1\t" + hash,
		"-1\t1\t" + hash,
		"1\t0\t" + hash,
		"1\t-2\t" + hash,
		"1\t2\tnot-a-hash",
		"1",
	} {
		if _, ok := parseCheckCacheLine(line); ok {
			t.Fatalf("accepted invalid line %q", line)
		}
	}
}

func TestReadWriteCheckCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), checkResultsFile)
	candidate := &checkCandidate{
		textHash: strings.Repeat("ab", 32),
		refs: []store.SpeechRef{
			{FileID: 1, Seq: 42, Filename: "a.txt"},
			{FileID: 2, Seq: 7, Filename: "b.txt"},
		},
		intervals: []silencedetect.Interval{
			{Start: 3 * time.Second, End: 6860 * time.Millisecond},
		},
	}

	lines, err := writeCheckCache(path, []*checkCandidate{candidate})
	if err != nil {
		t.Fatalf("writeCheckCache() error = %v", err)
	}
	if lines != 2 {
		t.Fatalf("lines = %d, want 2", lines)
	}

	entries, exists, invalid, err := readCheckCache(path)
	if err != nil {
		t.Fatalf("readCheckCache() error = %v", err)
	}
	if !exists || invalid != 0 || len(entries) != 2 {
		t.Fatalf("entries=%d exists=%v invalid=%d", len(entries), exists, invalid)
	}
	if entries[0].FileID != 1 || entries[0].Seq != 42 || entries[0].TextHash != candidate.textHash {
		t.Fatalf("entries[0] = %+v", entries[0])
	}
	if !strings.Contains(entries[0].Details, "3.00s-6.86s") {
		t.Fatalf("details = %q", entries[0].Details)
	}

	lines, err = writeCheckCache(path, nil)
	if err != nil {
		t.Fatalf("writeCheckCache(nil) error = %v", err)
	}
	if lines != 0 {
		t.Fatalf("lines = %d, want 0", lines)
	}
	entries, exists, invalid, err = readCheckCache(path)
	if err != nil || !exists || invalid != 0 || len(entries) != 0 {
		t.Fatalf("empty cache: entries=%d exists=%v invalid=%d err=%v", len(entries), exists, invalid, err)
	}
}

func TestReadCheckCacheSkipsInvalidLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), checkResultsFile)
	hash := strings.Repeat("cd", 32)
	content := checkResultsHeader +
		"1\t2\t" + hash + "\t1.00s-2.00s (1.00s)\n" +
		"broken line\n" +
		"3\t4\tbad-hash\t3.00s-4.00s\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, exists, invalid, err := readCheckCache(path)
	if err != nil {
		t.Fatalf("readCheckCache() error = %v", err)
	}
	if !exists || len(entries) != 1 || invalid != 2 {
		t.Fatalf("entries=%d exists=%v invalid=%d", len(entries), exists, invalid)
	}
}
