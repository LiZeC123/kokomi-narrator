package bgm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Prober looks up a music file's duration.
type Prober interface {
	Probe(ctx context.Context, path string) (time.Duration, error)
}

// Tool wraps the external ffmpeg/ffprobe binaries.
type Tool struct {
	FFmpeg  string
	FFprobe string
}

// FindTools locates both ffmpeg and ffprobe.
func FindTools() (*Tool, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("bgm requires ffmpeg in PATH")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, fmt.Errorf("bgm requires ffprobe in PATH")
	}
	return &Tool{FFmpeg: ffmpeg, FFprobe: ffprobe}, nil
}

// FindFFmpegTool locates ffmpeg only, for mixing existing WAV files.
func FindFFmpegTool() (*Tool, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("mix requires ffmpeg in PATH")
	}
	return &Tool{FFmpeg: ffmpeg}, nil
}

// Probe returns the duration reported by ffprobe, rounded to milliseconds.
func (t *Tool) Probe(ctx context.Context, path string) (time.Duration, error) {
	if t.FFprobe == "" {
		return 0, fmt.Errorf("ffprobe is not configured")
	}
	cmd := exec.CommandContext(ctx, t.FFprobe,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1",
		path,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return 0, fmt.Errorf("ffprobe %s: %w: %s", path, err, msg)
		}
		return 0, fmt.Errorf("ffprobe %s: %w", path, err)
	}

	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, fmt.Errorf("ffprobe %s: invalid duration %q", path, strings.TrimSpace(string(out)))
	}
	ms := int64(math.Round(seconds * 1000))
	if ms <= 0 {
		return 0, fmt.Errorf("ffprobe %s: duration is not positive", path)
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func (t *Tool) runFFmpeg(ctx context.Context, args ...string) error {
	if t.FFmpeg == "" {
		return fmt.Errorf("ffmpeg is not configured")
	}
	full := append([]string{
		"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
	}, args...)
	cmd := exec.CommandContext(ctx, t.FFmpeg, full...)
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("ffmpeg: %w: %s", err, msg)
		}
		return fmt.Errorf("ffmpeg: %w", err)
	}
	return nil
}

// Transcode normalizes one source file to 48 kHz stereo PCM WAV.
func (t *Tool) Transcode(ctx context.Context, src, dst string) error {
	return t.runFFmpeg(ctx,
		"-i", src,
		"-vn",
		"-map", "0:a:0",
		"-ar", strconv.Itoa(OutputSampleRate),
		"-ac", strconv.Itoa(OutputChannels),
		"-c:a", "pcm_s16le",
		"-f", "wav",
		dst,
	)
}

// Silence writes a 48 kHz stereo PCM WAV of the requested duration.
func (t *Tool) Silence(ctx context.Context, duration time.Duration, dst string) error {
	return t.runFFmpeg(ctx,
		"-f", "lavfi",
		"-i", fmt.Sprintf("anullsrc=r=%d:cl=stereo", OutputSampleRate),
		"-t", formatSeconds(duration),
		"-c:a", "pcm_s16le",
		"-f", "wav",
		dst,
	)
}

// Concat joins normalized WAV segments into a single 48 kHz stereo PCM WAV.
func (t *Tool) Concat(ctx context.Context, listPath, dst string) error {
	return t.runFFmpeg(ctx,
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-vn",
		"-c:a", "pcm_s16le",
		"-ar", strconv.Itoa(OutputSampleRate),
		"-ac", strconv.Itoa(OutputChannels),
		"-f", "wav",
		dst,
	)
}

func formatSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 6, 64)
}
