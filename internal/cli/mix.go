package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/bgm"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func newMixCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "mix <file|id>",
		Short: "Mix the voice track and BGM into the final WAV",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runMix(a, args[0])
			})
		},
	}
}

func runMix(a *app, fileArg string) error {
	file, err := resolveFile(a, fileArg)
	if err != nil {
		return err
	}
	name := file.Filename
	base := strings.TrimSuffix(file.Filename, filepath.Ext(file.Filename))
	voicePath := filepath.Join(a.cfg.OutputDir, base+"_voice.wav")
	bgmPath := filepath.Join(a.cfg.OutputDir, base+"_bgm.wav")
	outputPath := filepath.Join(a.cfg.OutputDir, base+".wav")

	voice, err := wavutil.InspectFile(voicePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return exitWithError(2, fmt.Errorf(
				"mix %s: voice track %s not found (run `ttsctl merge %s` first)", name, voicePath, fileArg))
		}
		return exitWithError(2, fmt.Errorf("mix %s: invalid voice track %s: %w", name, voicePath, err))
	}
	bgmTrack, err := wavutil.InspectFile(bgmPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return exitWithError(2, fmt.Errorf(
				"mix %s: BGM track %s not found (run `ttsctl bgm %s` first)", name, bgmPath, fileArg))
		}
		return exitWithError(2, fmt.Errorf("mix %s: invalid BGM track %s: %w", name, bgmPath, err))
	}

	if delta := voice.Duration - bgmTrack.Duration; delta > time.Second || delta < -time.Second {
		a.errorf("warning: voice and BGM durations differ by %s\n", delta.Round(time.Millisecond))
	}

	tool, err := bgm.FindFFmpegTool()
	if err != nil {
		return err
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	actual, err := bgm.Mix(sigCtx, tool, voicePath, bgmPath, outputPath, a.cfg.Mix.BGMVolume)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			a.errorf("interrupted\n")
			return exitSilent(130)
		}
		return exitWithError(2, fmt.Errorf("mix %s: %w", name, err))
	}

	a.printf("mix %s: wrote %s (voice=%s bgm=%s volume=%.0f%%, output=%s)\n",
		name, outputPath, voice.Duration.Round(time.Millisecond),
		bgmTrack.Duration.Round(time.Millisecond), a.cfg.Mix.BGMVolume*100,
		actual.Round(time.Millisecond))
	return nil
}
