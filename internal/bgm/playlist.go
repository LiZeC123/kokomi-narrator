package bgm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TrackStart is one selected track's offset in the rendered BGM.
type TrackStart struct {
	Start    time.Duration
	Filename string
}

// TrackStarts computes where each selected track begins in the rendered BGM.
// It mirrors Render: gaps of at most 10ms are not written to the output.
func TrackStarts(plan Plan) []TrackStart {
	starts := make([]TrackStart, 0, len(plan.Tracks))
	var elapsed time.Duration
	for i, track := range plan.Tracks {
		if i < len(plan.Gaps) && plan.Gaps[i] > minAudibleGap {
			elapsed += plan.Gaps[i]
		}
		starts = append(starts, TrackStart{Start: elapsed, Filename: track.Filename})
		elapsed += track.Duration
	}
	return starts
}

// WriteTrackList writes the selected track names and their start times to path.
// The file is plain text with one track per line and is written atomically.
func WriteTrackList(path string, plan Plan) error {
	if len(plan.Tracks) == 0 {
		return fmt.Errorf("no BGM tracks to list")
	}
	if len(plan.Gaps) != len(plan.Tracks)+1 {
		return fmt.Errorf("invalid BGM plan: %d tracks but %d gaps", len(plan.Tracks), len(plan.Gaps))
	}

	var b strings.Builder
	b.WriteString("# start\tfile\n")
	for _, item := range TrackStarts(plan) {
		name := strings.ReplaceAll(item.Filename, "\t", " ")
		name = strings.ReplaceAll(name, "\n", " ")
		fmt.Fprintf(&b, "%s\t%s\n", formatTrackStart(item.Start), name)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func formatTrackStart(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	d -= minutes * time.Minute
	seconds := d / time.Second
	d -= seconds * time.Second
	milliseconds := d / time.Millisecond
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, milliseconds)
}
