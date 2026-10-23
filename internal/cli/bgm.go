package cli

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
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

func newBgmCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "bgm <file|id>",
		Short: "Generate BGM matching a merged voice track",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runBgm(a, args[0])
			})
		},
	}
}

func runBgm(a *app, fileArg string) error {
	file, err := resolveFile(a, fileArg)
	if err != nil {
		return err
	}
	name := file.Filename
	base := strings.TrimSuffix(file.Filename, filepath.Ext(file.Filename))
	voicePath := filepath.Join(a.cfg.OutputDir, base+"_voice.wav")

	voice, err := wavutil.InspectFile(voicePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return exitWithError(2, fmt.Errorf(
				"bgm %s: voice track %s not found (run `ttsctl merge %s` first)", name, voicePath, fileArg))
		}
		return exitWithError(2, fmt.Errorf("bgm %s: invalid voice track %s: %w", name, voicePath, err))
	}
	target := voice.Duration
	if target <= 0 {
		return exitWithError(2, fmt.Errorf("bgm %s: voice track %s is empty", name, voicePath))
	}

	tool, err := bgm.FindTools()
	if err != nil {
		return err
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := bgm.SyncLibrary(sigCtx, a.store, tool, a.cfg.BGM.Dir, a.timestamp())
	if err != nil {
		if errors.Is(err, context.Canceled) {
			a.errorf("interrupted\n")
			return exitSilent(130)
		}
		return err
	}
	for _, failure := range result.Failures {
		a.errorf("warning: skipping %s: %v\n", failure.Path, failure.Err)
	}
	a.printf("bgm %s: library=%s files=%d cached=%d probed=%d removed=%d invalid=%d target=%s\n",
		name, a.cfg.BGM.Dir, result.Stats.Files, result.Stats.Cached,
		result.Stats.Probed, result.Stats.Removed, result.Stats.Invalid,
		target.Round(time.Millisecond))
	if len(result.Tracks) == 0 {
		return exitWithError(2, fmt.Errorf("bgm %s: no usable audio files in %s", name, a.cfg.BGM.Dir))
	}

	plan, err := bgm.BuildPlan(result.Tracks, target, rand.New(rand.NewSource(time.Now().UnixNano())))
	if err != nil {
		if errors.Is(err, bgm.ErrNoUsableTracks) {
			return exitWithError(2, fmt.Errorf(
				"bgm %s: no track is short enough for target %s", name, target.Round(time.Millisecond)))
		}
		return exitWithError(2, fmt.Errorf("bgm %s: %w", name, err))
	}

	outputPath := filepath.Join(a.cfg.OutputDir, base+"_bgm.wav")
	actual, err := bgm.Render(sigCtx, tool, plan, outputPath)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			a.errorf("interrupted\n")
			return exitSilent(130)
		}
		return exitWithError(2, fmt.Errorf("bgm %s: %w", name, err))
	}

	a.printf("bgm %s: wrote %s (%d tracks, %s silence, %s)\n",
		name, outputPath, len(plan.Tracks), plan.TotalGap.Round(time.Millisecond),
		actual.Round(time.Millisecond))
	if delta := actual - target; delta > 100*time.Millisecond || delta < -100*time.Millisecond {
		a.errorf("warning: BGM duration differs from voice by %s\n", delta.Round(time.Millisecond))
	}
	return nil
}
