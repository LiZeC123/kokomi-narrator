// Package silencedetect locates silent regions in audio files by driving the
// ffmpeg silencedetect filter.
package silencedetect

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Interval is one continuous silent region.
type Interval struct {
	Start time.Duration
	End   time.Duration
}

// Duration returns the length of the interval.
func (i Interval) Duration() time.Duration {
	return i.End - i.Start
}

// Detector runs ffmpeg silencedetect on audio files.
type Detector struct {
	FFmpeg string
}

// New returns a Detector using the given ffmpeg binary path.
func New(ffmpeg string) *Detector {
	return &Detector{FFmpeg: ffmpeg}
}

// FindFFmpeg locates ffmpeg in PATH.
func FindFFmpeg() (string, error) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("check requires ffmpeg in PATH")
	}
	return path, nil
}

var (
	silenceStartRE = regexp.MustCompile(`silence_start:\s*(-?[0-9.]+)`)
	silenceEndRE   = regexp.MustCompile(`silence_end:\s*(-?[0-9.]+)\s*\|\s*silence_duration:\s*([0-9.]+)`)
	durationRE     = regexp.MustCompile(`Duration:\s*(\d+):(\d+):(\d+(?:\.\d+)?)`)
)

// Detect returns every silence interval whose duration is at least minSilence.
// The whole file is scanned, not only its beginning.
func (d *Detector) Detect(ctx context.Context, path string, thresholdDB float64, minSilence time.Duration) ([]Interval, error) {
	if d.FFmpeg == "" {
		return nil, fmt.Errorf("ffmpeg is not configured")
	}
	if minSilence <= 0 {
		return nil, fmt.Errorf("minimum silence must be greater than zero")
	}
	if math.IsNaN(thresholdDB) || math.IsInf(thresholdDB, 0) {
		return nil, fmt.Errorf("silence threshold must be a finite number")
	}

	args := []string{
		"-hide_banner",
		"-nostdin",
		"-nostats",
		"-loglevel", "info",
		"-i", path,
		"-af", fmt.Sprintf("silencedetect=n=%sdB:d=%s", formatNumber(thresholdDB), formatSeconds(minSilence)),
		"-f", "null",
		"-",
	}
	cmd := exec.CommandContext(ctx, d.FFmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("ffmpeg %s: %w: %s", path, err, truncate(msg, 500))
		}
		return nil, fmt.Errorf("ffmpeg %s: %w", path, err)
	}
	return parseOutput(stderr.String()), nil
}

// parseOutput extracts silence intervals from ffmpeg's stderr. FFmpeg normally
// prints a start line and a matching end line; a trailing silence without an
// end line is closed with the file duration that ffmpeg reported.
func parseOutput(output string) []Interval {
	var (
		intervals []Interval
		start     time.Duration
		hasStart  bool
		total     time.Duration
		hasTotal  bool
	)

	for _, line := range strings.Split(output, "\n") {
		if m := durationRE.FindStringSubmatch(line); m != nil {
			if d, ok := parseDurationParts(m); ok {
				total, hasTotal = d, true
			}
			continue
		}
		if m := silenceStartRE.FindStringSubmatch(line); m != nil {
			if seconds, ok := parseSeconds(m[1]); ok {
				start, hasStart = time.Duration(seconds*float64(time.Second)), true
			}
		}
		if m := silenceEndRE.FindStringSubmatch(line); m != nil {
			end, okEnd := parseSeconds(m[1])
			if !okEnd {
				continue
			}
			endDur := time.Duration(end * float64(time.Second))
			if hasStart && endDur > start {
				intervals = append(intervals, Interval{Start: start, End: endDur})
			} else if seconds, ok := parseSeconds(m[2]); ok {
				dur := time.Duration(seconds * float64(time.Second))
				if dur > 0 {
					intervals = append(intervals, Interval{Start: endDur - dur, End: endDur})
				}
			}
			hasStart = false
		}
	}

	if hasStart && hasTotal && total > start {
		intervals = append(intervals, Interval{Start: start, End: total})
	}
	return intervals
}

func parseDurationParts(m []string) (time.Duration, bool) {
	hours, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	minutes, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(m[3], 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds*float64(time.Second)), true
}

func parseSeconds(s string) (float64, bool) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func formatSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
