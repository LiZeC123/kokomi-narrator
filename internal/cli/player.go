package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var errPlaybackInterrupted = errors.New("playback interrupted")

type audioPlayer interface {
	Play(ctx context.Context, path string) error
}

// newPlayer is a variable so tests can substitute a fake player.
var newPlayer = detectPlayer

type execPlayer struct {
	name string
	args []string
}

func (p execPlayer) Play(ctx context.Context, path string) error {
	args := append(append([]string{}, p.args...), path)
	cmd := exec.CommandContext(ctx, p.name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return errPlaybackInterrupted
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%s: %w: %s", p.name, err, msg)
		}
		return fmt.Errorf("%s: %w", p.name, err)
	}
	return nil
}

func detectPlayer() (audioPlayer, error) {
	if path, err := exec.LookPath("afplay"); err == nil {
		return execPlayer{name: path}, nil
	}
	if path, err := exec.LookPath("ffplay"); err == nil {
		return execPlayer{
			name: path,
			args: []string{"-nodisp", "-autoexit", "-loglevel", "error"},
		}, nil
	}
	if path, err := exec.LookPath("mpv"); err == nil {
		return execPlayer{
			name: path,
			args: []string{"--no-video", "--really-quiet"},
		}, nil
	}
	return nil, fmt.Errorf("no audio player found (tried afplay, ffplay, mpv)")
}
