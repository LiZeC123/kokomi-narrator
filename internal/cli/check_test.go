package cli

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LiZeC123/kokomi-narrator/internal/config"
	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func testToneWAV() []byte {
	format := wavutil.Format{
		AudioFormat:   1,
		Channels:      1,
		SampleRate:    8000,
		ByteRate:      16000,
		BlockAlign:    2,
		BitsPerSample: 16,
	}
	const frames = 4000 // 0.5s
	pcm := make([]byte, frames*2)
	for i := 0; i < frames; i++ {
		sample := int16(10000)
		if (i/80)%2 == 0 {
			sample = -10000
		}
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(sample))
	}
	return wavutil.Encode(format, pcm)
}

func TestCheckFindsAndRedoesBadAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	badSample := filepath.Join("..", "..", "testdata", "bad.wav")
	goodSample := filepath.Join("..", "..", "testdata", "good.wav")
	for _, path := range []string{badSample, goodSample} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("sample %s not available: %v", path, err)
		}
	}
	var err error
	if badSample, err = filepath.Abs(badSample); err != nil {
		t.Fatal(err)
	}
	if goodSample, err = filepath.Abs(goodSample); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int64
	tone := testToneWAV()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		requests.Add(1)
		_, _ = w.Write(tone)
	}))
	defer srv.Close()

	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	cfg := fmt.Sprintf(`database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
log_level: error
tts:
  url: "%s"
  character: "default"
  concurrency: 2
  timeout: 5s
  retries: 0
  retry_backoff: 10ms
check:
  silence_db: -45
  silence_duration: 1s
  concurrency: 2
`, srv.URL)
	if err := os.WriteFile("config.yaml", []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"bad.txt":  "坏片段",
		"good.txt": "好片段",
	} {
		if err := os.WriteFile(name, []byte(text+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	voice := config.VoiceKey("default")
	copySample := func(sample, text string) {
		t.Helper()
		data, err := os.ReadFile(sample)
		if err != nil {
			t.Fatal(err)
		}
		path := audioPath("data/audio", voice, textparse.HashText(text))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, name := range []string{"bad.txt", "good.txt"} {
		code, stdout, stderr := runCLI(t, "add", name)
		if code != 0 {
			t.Fatalf("add %s: code=%d stdout=%q stderr=%q", name, code, stdout, stderr)
		}
	}
	copySample(badSample, "坏片段")
	copySample(goodSample, "好片段")

	code, stdout, stderr := runCLI(t, "check")
	if code != 2 {
		t.Fatalf("check code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "bad.txt") || strings.Contains(stdout, "good.txt") {
		t.Fatalf("check output should list only bad.txt:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 problematic wav(s), 1 segment(s) affected") {
		t.Fatalf("check summary missing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "3.03s-6.86s") {
		t.Fatalf("check silence details missing:\n%s", stdout)
	}

	code, stdout, stderr = runCLI(t, "check", "--redo")
	if code != 0 {
		t.Fatalf("check --redo code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "fixed=1 wav(s)/1 segment(s)") || !strings.Contains(stdout, "all problematic audio fixed") {
		t.Fatalf("redo summary missing:\n%s", stdout)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("tts requests = %d, want 1", got)
	}

	code, stdout, stderr = runCLI(t, "check")
	if code != 0 || !strings.Contains(stdout, "all clean") {
		t.Fatalf("check after redo: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI(t, "ls", "bad.txt")
	if code != 0 || !strings.Contains(stdout, "success") {
		t.Fatalf("ls after redo: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCheckRedoReportsFailuresAndContinues(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	badSample := filepath.Join("..", "..", "testdata", "bad.wav")
	if _, err := os.Stat(badSample); err != nil {
		t.Skipf("sample %s not available: %v", badSample, err)
	}
	absBad, err := filepath.Abs(badSample)
	if err != nil {
		t.Fatal(err)
	}
	badSample = absBad

	var failEnabled atomic.Bool
	failEnabled.Store(true)
	var requests atomic.Int64
	tone := testToneWAV()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		text, err := url.QueryUnescape(req.Text)
		if err != nil {
			http.Error(w, "bad text", http.StatusBadRequest)
			return
		}
		requests.Add(1)
		if failEnabled.Load() && strings.Contains(text, "FAIL") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(tone)
	}))
	defer srv.Close()

	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	cfg := fmt.Sprintf(`database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
log_level: error
tts:
  url: "%s"
  character: "default"
  concurrency: 2
  timeout: 5s
  retries: 0
  retry_backoff: 10ms
check:
  silence_db: -45
  silence_duration: 1s
  concurrency: 2
`, srv.URL)
	if err := os.WriteFile("config.yaml", []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"ok.txt":   "OK坏片段",
		"fail.txt": "FAIL坏片段",
	} {
		if err := os.WriteFile(name, []byte(text+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	voice := config.VoiceKey("default")
	sample, err := os.ReadFile(badSample)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"OK坏片段", "FAIL坏片段"} {
		path := audioPath("data/audio", voice, textparse.HashText(text))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, sample, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"ok.txt", "fail.txt"} {
		if code, stdout, stderr := runCLI(t, "add", name); code != 0 {
			t.Fatalf("add %s: code=%d stdout=%q stderr=%q", name, code, stdout, stderr)
		}
	}

	code, stdout, stderr := runCLI(t, "check", "--redo")
	if code != 2 {
		t.Fatalf("check --redo code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "fixed=1 wav(s)/1 segment(s)") {
		t.Fatalf("success count missing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "failed entries:") || !strings.Contains(stdout, "file=2 seq=1 fail.txt") {
		t.Fatalf("failed entry missing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "boom") {
		t.Fatalf("failure reason missing:\n%s", stdout)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("tts requests = %d, want 2 (both entries processed)", got)
	}

	failEnabled.Store(false)
	code, stdout, stderr = runCLI(t, "check", "--redo")
	if code != 0 {
		t.Fatalf("second check --redo code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "fixed=1 wav(s)/1 segment(s)") || !strings.Contains(stdout, "all problematic audio fixed") {
		t.Fatalf("second redo summary missing:\n%s", stdout)
	}
}

func TestCheckRedoUsesAndUpdatesCache(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	badSample := filepath.Join("..", "..", "testdata", "bad.wav")
	if _, err := os.Stat(badSample); err != nil {
		t.Skipf("sample %s not available: %v", badSample, err)
	}
	absBad, err := filepath.Abs(badSample)
	if err != nil {
		t.Fatal(err)
	}
	badSample = absBad

	var requests atomic.Int64
	tone := testToneWAV()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write(tone)
	}))
	defer srv.Close()

	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	configBody := fmt.Sprintf(`database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
log_level: error
tts:
  url: "%s"
  character: "default"
  concurrency: 2
  timeout: 5s
  retries: 0
  retry_backoff: 10ms
check:
  silence_db: -45
  silence_duration: 1s
  concurrency: 2
`, srv.URL)
	if err := os.WriteFile("config.yaml", []byte(configBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("bad.txt", []byte("坏片段\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	voice := config.VoiceKey("default")
	hash := textparse.HashText("坏片段")
	sample, err := os.ReadFile(badSample)
	if err != nil {
		t.Fatal(err)
	}
	path := audioPath("data/audio", voice, hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, sample, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runCLI(t, "add", "bad.txt"); code != 0 {
		t.Fatalf("add: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr := runCLI(t, "check")
	if code != 2 {
		t.Fatalf("check code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	cache, err := os.ReadFile(checkResultsFile)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if !strings.Contains(string(cache), "1\t1\t"+hash) || !strings.Contains(string(cache), "3.03s-6.86s") {
		t.Fatalf("unexpected cache contents:\n%s", cache)
	}

	// Editing the cache to remove the entry must be honored by --redo.
	if err := os.WriteFile(checkResultsFile, []byte(checkResultsHeader), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runCLI(t, "check", "--redo")
	if code != 0 || !strings.Contains(stdout, "cached result is clean") {
		t.Fatalf("edited cache: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("tts requests = %d, want 0 for edited cache", got)
	}

	// Regenerate the cache, then make detection find nothing to prove --redo
	// reads the cache instead of running the full detection pass again.
	code, stdout, stderr = runCLI(t, "check")
	if code != 2 {
		t.Fatalf("regenerate check code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	updatedConfig := strings.Replace(configBody, "silence_duration: 1s", "silence_duration: 1h", 1)
	if err := os.WriteFile("config.yaml", []byte(updatedConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr = runCLI(t, "check", "--redo")
	if code != 0 {
		t.Fatalf("cached redo code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "using cached results") || !strings.Contains(stdout, "fixed=1 wav(s)/1 segment(s)") {
		t.Fatalf("cached redo output missing:\n%s", stdout)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("tts requests = %d, want 1", got)
	}

	entries, exists, invalid, err := readCheckCache(checkResultsFile)
	if err != nil || !exists || invalid != 0 || len(entries) != 0 {
		t.Fatalf("cache after fix: entries=%d exists=%v invalid=%d err=%v", len(entries), exists, invalid, err)
	}

	code, stdout, stderr = runCLI(t, "check", "--redo")
	if code != 0 || !strings.Contains(stdout, "cached result is clean") {
		t.Fatalf("clean cache redo: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("tts requests = %d, want 1", got)
	}
}
