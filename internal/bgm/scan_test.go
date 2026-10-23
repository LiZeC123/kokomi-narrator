package bgm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanDirFiltersAndSorts(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("b.MP3", "bb")
	write("a.wav", "a")
	write("notes.txt", "ignored")
	write("c.flac", "ccc")
	if err := os.Mkdir(filepath.Join(dir, "sub.mp3"), 0o755); err != nil {
		t.Fatal(err)
	}

	absDir, files, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir() error = %v", err)
	}
	if absDir != dir {
		t.Fatalf("absDir = %q, want %q", absDir, dir)
	}
	if len(files) != 3 {
		t.Fatalf("files = %+v, want 3", files)
	}
	if files[0].Filename != "a.wav" || files[1].Filename != "b.MP3" || files[2].Filename != "c.flac" {
		t.Fatalf("files = %+v, want sorted audio files", files)
	}
	if files[0].Size != 1 || files[1].Size != 2 || files[2].Size != 3 {
		t.Fatalf("sizes = %d/%d/%d, want 1/2/3", files[0].Size, files[1].Size, files[2].Size)
	}
	if files[0].MtimeNS == 0 {
		t.Fatal("MtimeNS was not populated")
	}
}

func TestScanDirRejectsMissingDirectory(t *testing.T) {
	if _, _, err := ScanDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("ScanDir() accepted a missing directory")
	}
}
