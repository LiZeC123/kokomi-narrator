package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LiZeC123/kokomi-narrator/internal/config"
	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

type fakeAudioPlayer struct {
	paths []string
	err   error
}

func (p *fakeAudioPlayer) Play(_ context.Context, path string) error {
	p.paths = append(p.paths, path)
	return p.err
}

func useFakePlayer(t *testing.T) *fakeAudioPlayer {
	t.Helper()
	player := &fakeAudioPlayer{}
	old := newPlayer
	newPlayer = func() (audioPlayer, error) { return player, nil }
	t.Cleanup(func() { newPlayer = old })
	return player
}

func enterTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	return dir
}

func writeTestConfig(t *testing.T, ttsURL string) {
	t.Helper()
	cfg := fmt.Sprintf(`database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
log_level: error
tts:
  url: "%s"
  character: default
  concurrency: 2
  timeout: 5s
  retries: 0
  retry_backoff: 10ms
merge:
  overwrite: true
  gap_between_segments: 100ms
  leading_trailing_gap: false
`, ttsURL)
	if err := os.WriteFile("config.yaml", []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testWAVFormat() wavutil.Format {
	return wavutil.Format{
		AudioFormat:   1,
		Channels:      1,
		SampleRate:    8000,
		ByteRate:      16000,
		BlockAlign:    2,
		BitsPerSample: 16,
	}
}

func TestFileIDCommandsPlayAndFind(t *testing.T) {
	enterTestDir(t)
	writeTestConfig(t, "http://127.0.0.1:1/tts")
	player := useFakePlayer(t)

	if err := os.WriteFile("a.txt", []byte("第一段\n，\n第二段\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-create audio for the first segment so add marks it success and play works.
	voice := config.VoiceKey("default")
	hash := textparse.HashText("第一段")
	audio := audioPath("data/audio", voice, hash)
	if err := os.MkdirAll(filepath.Dir(audio), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(audio, wavutil.Encode(testWAVFormat(), make([]byte, 64)), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI(t, "add", "a.txt")
	if code != 0 {
		t.Fatalf("add exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "(id=1)") {
		t.Fatalf("add output missing id: %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "files")
	if code != 0 {
		t.Fatalf("files exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "a.txt") || !strings.Contains(stdout, "ID") {
		t.Fatalf("files output = %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "ls", "1")
	if code != 0 {
		t.Fatalf("ls 1 exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "FILE a.txt total=3") {
		t.Fatalf("ls 1 output = %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "find", "1", "第二段")
	if code != 0 {
		t.Fatalf("find exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "matches=1") || !strings.Contains(stdout, "第二段") {
		t.Fatalf("find output = %q", stdout)
	}
	if !strings.Contains(stdout, "3 ") {
		t.Fatalf("find output missing seq 3: %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "find", "a.txt", "不存在")
	if code != 0 || !strings.Contains(stdout, "matches=0") {
		t.Fatalf("find no-match exit = %d, output = %q, stderr = %s", code, stdout, stderr)
	}
	if code, _, _ = runCLI(t, "find", "1", "   "); code != 1 {
		t.Fatalf("find empty query exit = %d, want 1", code)
	}

	code, stdout, stderr = runCLI(t, "play", "1", "1")
	if code != 0 {
		t.Fatalf("play exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "#1") {
		t.Fatalf("play output = %q", stdout)
	}
	if len(player.paths) != 1 || player.paths[0] != audio {
		t.Fatalf("played paths = %v, want [%s]", player.paths, audio)
	}

	if code, _, _ = runCLI(t, "play", "a.txt", "1"); code != 0 {
		t.Fatalf("play by filename exit = %d", code)
	}
	if code, _, _ = runCLI(t, "play", "1", "2"); code != 1 {
		t.Fatalf("play punctuation exit = %d, want 1", code)
	}
	if code, _, _ = runCLI(t, "play", "1", "3"); code != 1 {
		t.Fatalf("play missing audio exit = %d, want 1", code)
	}
	if code, _, _ = runCLI(t, "play", "999", "1"); code != 1 {
		t.Fatalf("play bad file id exit = %d, want 1", code)
	}

	if err := os.WriteFile("a.txt", []byte("第一段\n新内容\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runCLI(t, "update", "1")
	if code != 0 {
		t.Fatalf("update 1 exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "updated a.txt") {
		t.Fatalf("update output = %q", stdout)
	}
}

func TestRedoPlay(t *testing.T) {
	enterTestDir(t)
	format := testWAVFormat()
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(wavutil.Encode(format, make([]byte, 64)))
	}))
	defer srv.Close()
	writeTestConfig(t, srv.URL)
	player := useFakePlayer(t)

	if err := os.WriteFile("a.txt", []byte("需要重做\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI(t, "add", "a.txt")
	if code != 0 {
		t.Fatalf("add exit = %d, stderr = %s", code, stderr)
	}

	code, stdout, stderr = runCLI(t, "redo", "1", "1", "--play")
	if code != 0 {
		t.Fatalf("redo --play exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "redo a.txt seq 1: success") {
		t.Fatalf("redo output = %q", stdout)
	}
	expected := audioPath("data/audio", config.VoiceKey("default"), textparse.HashText("需要重做"))
	if len(player.paths) != 1 || player.paths[0] != expected {
		t.Fatalf("played paths = %v, want [%s]", player.paths, expected)
	}

	// A playback error must not change redo's successful exit code.
	player.err = errors.New("no audio device")
	code, _, stderr = runCLI(t, "redo", "1", "1", "--play")
	if code != 0 {
		t.Fatalf("redo --play with playback error exit = %d, want 0", code)
	}
	if !strings.Contains(stderr, "playback failed") {
		t.Fatalf("redo --play stderr = %q, want playback warning", stderr)
	}

	// A redo failure with valid existing audio keeps that audio and plays it.
	fail.Store(true)
	player.err = nil
	player.paths = nil
	code, _, stderr = runCLI(t, "redo", "1", "1", "--play")
	if code != 2 {
		t.Fatalf("failed redo --play exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "existing audio kept") {
		t.Fatalf("failed redo --play stderr = %q", stderr)
	}
	if len(player.paths) != 1 || player.paths[0] != expected {
		t.Fatalf("failed redo played paths = %v, want [%s]", player.paths, expected)
	}
}
