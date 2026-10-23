package silencedetect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestParseOutput(t *testing.T) {
	output := `
  Duration: 00:00:31.66, bitrate: 512 kb/s
[Parsed_silencedetect_0 @ 0x1] silence_start: 3.025844
[Parsed_silencedetect_0 @ 0x1] silence_end: 6.862906 | silence_duration: 3.837063
[Parsed_silencedetect_0 @ 0x1] silence_start: 9.999969
[Parsed_silencedetect_0 @ 0x1] silence_end: 12.717344 | silence_duration: 2.717375
`
	got := parseOutput(output)
	if len(got) != 2 {
		t.Fatalf("intervals = %d, want 2: %+v", len(got), got)
	}
	if diff := got[0].Start - 3025844*time.Microsecond; diff < -time.Microsecond || diff > time.Microsecond {
		t.Fatalf("first start = %v, want ~3.025844s", got[0].Start)
	}
	if diff := got[0].End - 6862906*time.Microsecond; diff < -time.Microsecond || diff > time.Microsecond {
		t.Fatalf("first end = %v, want ~6.862906s", got[0].End)
	}
	if diff := got[1].Duration() - 2717375*time.Microsecond; diff < -time.Microsecond || diff > time.Microsecond {
		t.Fatalf("second duration = %v, want ~2.717375s", got[1].Duration())
	}
}

func TestParseOutputTrailingSilenceWithoutEnd(t *testing.T) {
	output := `
  Duration: 00:00:02.00, bitrate: 512 kb/s
[Parsed_silencedetect_0 @ 0x1] silence_start: 1.2
`
	got := parseOutput(output)
	if len(got) != 1 {
		t.Fatalf("intervals = %d, want 1: %+v", len(got), got)
	}
	if got[0].Start != 1200*time.Millisecond || got[0].End != 2*time.Second {
		t.Fatalf("interval = %v-%v", got[0].Start, got[0].End)
	}
}

func TestDetectSamples(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	good := filepath.Join("..", "..", "testdata", "good.wav")
	bad := filepath.Join("..", "..", "testdata", "bad.wav")
	for _, path := range []string{good, bad} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("sample %s not available: %v", path, err)
		}
	}

	detector := New(ffmpeg)
	ctx := context.Background()

	goodIntervals, err := detector.Detect(ctx, good, -45, time.Second)
	if err != nil {
		t.Fatalf("detect good.wav: %v", err)
	}
	if len(goodIntervals) != 0 {
		t.Fatalf("good.wav intervals = %+v, want none", goodIntervals)
	}

	badIntervals, err := detector.Detect(ctx, bad, -45, time.Second)
	if err != nil {
		t.Fatalf("detect bad.wav: %v", err)
	}
	if len(badIntervals) != 4 {
		t.Fatalf("bad.wav intervals = %d, want 4: %+v", len(badIntervals), badIntervals)
	}
	wantStarts := []time.Duration{
		3025*time.Millisecond + 844*time.Microsecond,
		9999*time.Millisecond + 969*time.Microsecond,
		20*time.Second + 86*time.Millisecond + 156*time.Microsecond,
		24*time.Second + 373*time.Millisecond + 406*time.Microsecond,
	}
	for i, want := range wantStarts {
		if diff := badIntervals[i].Start - want; diff < -10*time.Millisecond || diff > 10*time.Millisecond {
			t.Fatalf("interval %d start = %v, want ~%v", i, badIntervals[i].Start, want)
		}
	}
}
