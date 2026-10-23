package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LiZeC123/kokomi-narrator/internal/bgm"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func TestBgmAndMixEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}

	enterTestDir(t)
	writeTestConfig(t, "http://127.0.0.1:1/tts")

	if err := os.WriteFile("a.txt", []byte("第一段\n，\n第二段\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI(t, "add", "a.txt")
	if code != 0 {
		t.Fatalf("add exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	format := testWAVFormat()
	if err := os.MkdirAll(filepath.Join("data", "output"), 0o755); err != nil {
		t.Fatal(err)
	}
	voiceData := make([]byte, int(format.ByteRate)*5/2) // 2.5 seconds
	if err := os.WriteFile(filepath.Join("data", "output", "a_voice.wav"),
		wavutil.Encode(format, voiceData), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join("data", "bgm"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, seconds := range map[string]int{
		"one.wav":   1,
		"two.wav":   1,
		"three.wav": 1,
		"long.wav":  10,
	} {
		data := make([]byte, int(format.ByteRate)*seconds)
		if err := os.WriteFile(filepath.Join("data", "bgm", name),
			wavutil.Encode(format, data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	code, stdout, stderr = runCLI(t, "bgm", "a.txt")
	if code != 0 {
		t.Fatalf("bgm exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	bgmPath := filepath.Join("data", "output", "a_bgm.wav")
	bgmInfo, err := wavutil.InspectFile(bgmPath)
	if err != nil {
		t.Fatalf("inspect bgm: %v", err)
	}
	if bgmInfo.Format.SampleRate != bgm.OutputSampleRate || bgmInfo.Format.Channels != bgm.OutputChannels {
		t.Fatalf("bgm format = %d Hz/%d ch, want %d Hz/%d ch",
			bgmInfo.Format.SampleRate, bgmInfo.Format.Channels, bgm.OutputSampleRate, bgm.OutputChannels)
	}
	if diff := bgmInfo.Duration - 2500*time.Millisecond; diff > 100*time.Millisecond || diff < -100*time.Millisecond {
		t.Fatalf("bgm duration = %v, want 2.5s (diff %v)", bgmInfo.Duration, diff)
	}

	// The second run must reuse cached durations instead of probing again.
	code, stdout, stderr = runCLI(t, "bgm", "a.txt")
	if code != 0 {
		t.Fatalf("second bgm exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "cached=4 probed=0") {
		t.Fatalf("second bgm output = %q, want cache hit for all tracks", stdout)
	}

	code, stdout, stderr = runCLI(t, "mix", "a.txt")
	if code != 0 {
		t.Fatalf("mix exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	mixedPath := filepath.Join("data", "output", "a.wav")
	mixed, err := wavutil.InspectFile(mixedPath)
	if err != nil {
		t.Fatalf("inspect mixed: %v", err)
	}
	if mixed.Format.SampleRate != format.SampleRate || mixed.Format.Channels != format.Channels ||
		mixed.Format.BitsPerSample != format.BitsPerSample {
		t.Fatalf("mixed format = %+v, want voice format %+v", mixed.Format, format)
	}
	if diff := mixed.Duration - 2500*time.Millisecond; diff > 100*time.Millisecond || diff < -100*time.Millisecond {
		t.Fatalf("mixed duration = %v, want 2.5s (diff %v)", mixed.Duration, diff)
	}

	// mix uses the longest input, so a longer BGM must not be truncated.
	longerBGM := make([]byte, int(format.ByteRate)*4)
	if err := os.WriteFile(bgmPath, wavutil.Encode(format, longerBGM), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runCLI(t, "mix", "a.txt")
	if code != 0 {
		t.Fatalf("mix with longer BGM exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	longest, err := wavutil.InspectFile(mixedPath)
	if err != nil {
		t.Fatalf("inspect longest mix: %v", err)
	}
	if diff := longest.Duration - 4*time.Second; diff > 100*time.Millisecond || diff < -100*time.Millisecond {
		t.Fatalf("longest mix duration = %v, want 4s (diff %v)", longest.Duration, diff)
	}

	if err := os.Remove(bgmPath); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runCLI(t, "mix", "a.txt")
	if code != 2 {
		t.Fatalf("mix without BGM exit = %d, want 2", code)
	}

	if err := os.Remove(filepath.Join("data", "output", "a_voice.wav")); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runCLI(t, "bgm", "a.txt")
	if code != 2 {
		t.Fatalf("bgm without voice exit = %d, want 2", code)
	}
}
