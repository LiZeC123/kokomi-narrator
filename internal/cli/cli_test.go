package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LiZeC123/kokomi-narrator/internal/config"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := NewRoot()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)

	err := root.Execute()
	code := 0
	if err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			code = ee.code
		} else {
			code = 1
		}
	}
	return code, stdout.String(), stderr.String()
}

func TestCLIEndToEnd(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	var requests atomic.Int64
	format := wavutil.Format{
		AudioFormat:   1,
		Channels:      1,
		SampleRate:    8000,
		ByteRate:      16000,
		BlockAlign:    2,
		BitsPerSample: 16,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Character string `json:"character"`
			Text      string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Character != "default" {
			http.Error(w, "bad character", http.StatusBadRequest)
			return
		}
		decodedText, err := url.QueryUnescape(req.Text)
		if err != nil {
			http.Error(w, "bad text encoding", http.StatusBadRequest)
			return
		}
		requests.Add(1)
		if strings.Contains(decodedText, "FAIL") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(wavutil.Encode(format, make([]byte, 800)))
	}))
	defer srv.Close()

	cfg := fmt.Sprintf(`database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
log_level: error
tts:
  url: "%s"
  character: "default"
  timeout: 5s
  retries: 0
  retry_backoff: 10ms
merge:
  overwrite: true
  gap_between_segments: 100ms
  leading_trailing_gap: false
`, srv.URL)
	if err := os.WriteFile("config.yaml", []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	writeText := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	assertCode := func(code, want int, stdout, stderr string) {
		t.Helper()
		if code != want {
			t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, want, stdout, stderr)
		}
	}

	writeText("a.txt", "第一段\n，\n第二段\n")
	code, stdout, stderr := runCLI(t, "add", "a.txt")
	assertCode(code, 0, stdout, stderr)
	if !strings.Contains(stdout, "3 segments") || !strings.Contains(stdout, "1 punctuation skipped") {
		t.Fatalf("add output = %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "make", "a.txt")
	assertCode(code, 0, stdout, stderr)
	if !strings.Contains(stdout, "success=2 failed=0 skipped=1 reused=0") {
		t.Fatalf("make output = %q", stdout)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}

	code, stdout, stderr = runCLI(t, "make", "a.txt")
	assertCode(code, 0, stdout, stderr)
	if got := requests.Load(); !strings.Contains(stdout, "reused=2") || got != 2 {
		t.Fatalf("second make output = %q, requests = %d", stdout, got)
	}

	code, stdout, stderr = runCLI(t, "ls", "a.txt")
	assertCode(code, 0, stdout, stderr)
	if !strings.Contains(stdout, "FILE a.txt total=3 voice="+config.VoiceKey("default")) ||
		!strings.Contains(stdout, "success") || !strings.Contains(stdout, "skipped") {
		t.Fatalf("ls output = %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "merge", "a.txt")
	assertCode(code, 0, stdout, stderr)
	merged, err := wavutil.ReadFile(filepath.Join("data", "output", "a_voice.wav"))
	if err != nil {
		t.Fatalf("merged file: %v", err)
	}
	gap := wavutil.Silence(format, 100*time.Millisecond)
	if want := 800*2 + len(gap); len(merged.Data) != want {
		t.Fatalf("merged data = %d bytes, want %d", len(merged.Data), want)
	}

	writeText("a.txt", "第二段\n，\n新的一段\n")
	code, stdout, stderr = runCLI(t, "update", "a.txt")
	assertCode(code, 0, stdout, stderr)
	if !strings.Contains(stdout, "1 added, 2 reused, 1 deleted") {
		t.Fatalf("update output = %q", stdout)
	}
	code, stdout, stderr = runCLI(t, "make", "a.txt")
	assertCode(code, 0, stdout, stderr)
	if got := requests.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
	code, stdout, stderr = runCLI(t, "ls", "a.txt")
	assertCode(code, 0, stdout, stderr)
	if !strings.Contains(stdout, "新的一段") {
		t.Fatalf("ls after update = %q", stdout)
	}

	voice := config.VoiceKey("default")
	orphanHash := strings.Repeat("ab", 32)
	orphanDir := filepath.Join("data", "audio", voice, "ab")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(orphanDir, orphanHash+".wav")
	if err := os.WriteFile(orphan, []byte("not referenced"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join("data", "audio", voice, "deadbeef.wav.tmp")
	if err := os.WriteFile(tmp, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr = runCLI(t, "clean", "--dry-run")
	assertCode(code, 2, stdout, stderr)
	if !strings.Contains(stdout, orphan) {
		t.Fatalf("clean dry-run output = %q", stdout)
	}
	code, stdout, stderr = runCLI(t, "clean")
	assertCode(code, 0, stdout, stderr)
	for _, path := range []string{orphan, tmp} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s still exists", path)
		}
	}

	writeText("c.txt", "FAIL line\n")
	code, stdout, stderr = runCLI(t, "add", "c.txt")
	assertCode(code, 0, stdout, stderr)
	code, stdout, stderr = runCLI(t, "make", "c.txt")
	assertCode(code, 2, stdout, stderr)
	if !strings.Contains(stdout, "failed=1") {
		t.Fatalf("failed make output = %q", stdout)
	}
	if !strings.Contains(stderr, "segment synthesis failed") || !strings.Contains(stderr, "boom") {
		t.Fatalf("failure log missing from stderr = %q", stderr)
	}
}
