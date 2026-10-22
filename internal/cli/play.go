package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
	"github.com/LiZeC123/kokomi-narrator/internal/tts"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func newPlayCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "play <file|id> <seq>",
		Short: "Play the synthesized audio for one segment",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runPlay(a, args[0], args[1])
			})
		},
	}
}

func runPlay(a *app, fileArg, seqArg string) error {
	file, err := resolveFile(a, fileArg)
	if err != nil {
		return err
	}
	seq, err := parseSeq(seqArg)
	if err != nil {
		return err
	}
	segment, err := a.store.SegmentBySeq(file.ID, seq)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%s has no segment with seq %d", file.Filename, seq)
		}
		return err
	}
	if segment.Kind != textparse.KindSpeech {
		return fmt.Errorf("%s seq %d is a punctuation segment", file.Filename, seq)
	}

	if err := playSegment(a, file, segment); err != nil {
		if errors.Is(err, errPlaybackInterrupted) {
			a.errorf("interrupted\n")
			return exitSilent(130)
		}
		return err
	}
	return nil
}

func playSegment(a *app, file *store.File, segment *store.Segment) error {
	target := audioPath(a.cfg.AudioDir, file.VoiceKey, segment.TextHash)
	if !wavutil.IsValidPCMFile(target) {
		return fmt.Errorf("audio for %s seq %d is missing or invalid (run `ttsctl make %d`)",
			file.Filename, segment.Seq, file.ID)
	}

	player, err := newPlayer()
	if err != nil {
		return fmt.Errorf("%w; audio file: %s", err, target)
	}

	a.printf("#%d [%s] %s\n", segment.Seq, segment.Status,
		tts.TruncateRunes(segment.Text, 60))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := player.Play(ctx, target); err != nil {
		if errors.Is(err, errPlaybackInterrupted) || ctx.Err() != nil {
			return errPlaybackInterrupted
		}
		return fmt.Errorf("play %s: %w", target, err)
	}
	return nil
}
