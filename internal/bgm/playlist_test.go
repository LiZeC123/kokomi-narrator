package bgm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrackStarts(t *testing.T) {
	plan := Plan{
		Tracks: []Track{
			{Filename: "a.mp3", Duration: 3 * time.Second},
			{Filename: "b.mp3", Duration: 2 * time.Second},
		},
		Gaps: []time.Duration{
			500 * time.Millisecond,
			5 * time.Millisecond, // not written by Render
			2 * time.Second,
		},
	}

	starts := TrackStarts(plan)
	if len(starts) != 2 {
		t.Fatalf("starts = %d, want 2", len(starts))
	}
	if starts[0].Start != 500*time.Millisecond || starts[0].Filename != "a.mp3" {
		t.Fatalf("starts[0] = %+v", starts[0])
	}
	if starts[1].Start != 3500*time.Millisecond || starts[1].Filename != "b.mp3" {
		t.Fatalf("starts[1] = %+v", starts[1])
	}
}

func TestTrackStartsIgnoresTinyGap(t *testing.T) {
	plan := Plan{
		Tracks: []Track{{Filename: "a.mp3", Duration: time.Second}},
		Gaps:   []time.Duration{10 * time.Millisecond, 0},
	}
	starts := TrackStarts(plan)
	if len(starts) != 1 || starts[0].Start != 0 {
		t.Fatalf("starts = %+v, want start 0", starts)
	}
}

func TestWriteTrackList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a_bgm.txt")
	plan := Plan{
		Tracks: []Track{
			{Filename: "a.mp3", Duration: 3 * time.Second},
			{Filename: "b.mp3", Duration: 2 * time.Second},
		},
		Gaps: []time.Duration{500 * time.Millisecond, 5 * time.Millisecond, 2 * time.Second},
	}
	if err := WriteTrackList(path, plan); err != nil {
		t.Fatalf("WriteTrackList() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# start\tfile\n00:00:00.500\ta.mp3\n00:00:03.500\tb.mp3\n"
	if string(data) != want {
		t.Fatalf("track list =\n%s\nwant:\n%s", data, want)
	}
}

func TestWriteTrackListRejectsInvalidPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a_bgm.txt")
	if err := WriteTrackList(path, Plan{}); err == nil {
		t.Fatal("WriteTrackList() accepted an empty plan")
	}
	plan := Plan{
		Tracks: []Track{{Filename: "a.mp3", Duration: time.Second}},
		Gaps:   []time.Duration{0},
	}
	if err := WriteTrackList(path, plan); err == nil {
		t.Fatal("WriteTrackList() accepted a plan with the wrong gap count")
	}
}

func TestFormatTrackStart(t *testing.T) {
	got := formatTrackStart(time.Hour + 2*time.Minute + 3*time.Second + 4*time.Millisecond)
	if got != "01:02:03.004" {
		t.Fatalf("formatTrackStart() = %q", got)
	}
}
