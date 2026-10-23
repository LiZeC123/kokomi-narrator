// Package bgm builds a background-music track from a local music library and
// mixes it with a merged voice track.
package bgm

import "time"

const (
	// OutputSampleRate is the sample rate used for generated BGM files.
	OutputSampleRate = 48000
	// OutputChannels is the channel count used for generated BGM files.
	OutputChannels = 2
	// minAudibleGap is the smallest gap Render inserts between tracks.
	minAudibleGap = 10 * time.Millisecond
)

// Track is one usable music library entry.
type Track struct {
	Path     string
	Filename string
	Duration time.Duration
}

// Plan is the selected BGM sequence. Gaps has len(Tracks)+1 entries: the
// silence before each track plus the trailing silence.
type Plan struct {
	Tracks   []Track
	Gaps     []time.Duration
	Target   time.Duration
	Sum      time.Duration
	TotalGap time.Duration
}
