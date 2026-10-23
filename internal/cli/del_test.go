package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LiZeC123/kokomi-narrator/internal/config"
	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func TestDelKeepsAudioForClean(t *testing.T) {
	enterTestDir(t)
	writeTestConfig(t, "http://127.0.0.1:1/tts")

	if err := os.WriteFile("a.txt", []byte("第一段\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runCLI(t, "add", "a.txt"); code != 0 {
		t.Fatalf("add exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	voice := config.VoiceKey("default")
	audio := audioPath("data/audio", voice, textparse.HashText("第一段"))
	if err := os.MkdirAll(filepath.Dir(audio), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(audio, wavutil.Encode(testWAVFormat(), make([]byte, 1600)), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI(t, "del", "a.txt")
	if code != 0 {
		t.Fatalf("del exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "deleted a.txt") || !strings.Contains(stdout, "1 segment(s)") ||
		!strings.Contains(stdout, "ttsctl clean") {
		t.Fatalf("del output = %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "files")
	if code != 0 || strings.Contains(stdout, "a.txt") {
		t.Fatalf("files after del: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(audio); err != nil {
		t.Fatalf("audio should remain after del: %v", err)
	}

	code, stdout, stderr = runCLI(t, "clean", "--dry-run")
	if code != 2 || !strings.Contains(stdout, audio) {
		t.Fatalf("clean --dry-run: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(t, "clean")
	if code != 0 {
		t.Fatalf("clean: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(audio); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("audio still exists after clean: %v", err)
	}

	code, _, _ = runCLI(t, "del", "a.txt")
	if code != 1 {
		t.Fatalf("del deleted file exit = %d, want 1", code)
	}
}
