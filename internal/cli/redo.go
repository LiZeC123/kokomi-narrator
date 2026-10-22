package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
	"github.com/LiZeC123/kokomi-narrator/internal/tts"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func newRedoCommand(configPath *string) *cobra.Command {
	var play bool
	cmd := &cobra.Command{
		Use:   "redo <file|id> <seq>",
		Short: "Regenerate the audio for one speech segment",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runRedo(a, args[0], args[1], play)
			})
		},
	}
	cmd.Flags().BoolVar(&play, "play", false, "play the audio after redo")
	return cmd
}

func runRedo(a *app, fileArg, seqArg string, play bool) error {
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

	currentVoice := currentVoiceKey(a.cfg)
	if file.VoiceKey != currentVoice {
		return fmt.Errorf("voice changed (%s -> %s); run make %s first", file.VoiceKey, currentVoice, file.Filename)
	}

	target := audioPath(a.cfg.AudioDir, file.VoiceKey, segment.TextHash)
	client := tts.New(a.cfg.TTS)
	data, ttsErr := client.Synthesize(context.Background(), segment.Text)
	if ttsErr == nil {
		ttsErr = wavutil.WriteAtomic(target, data)
	}

	if ttsErr == nil {
		n, err := a.store.SetHashStatus(file.ID, segment.TextHash, store.StatusSuccess, nil, false, a.timestamp())
		if err != nil {
			return err
		}
		segment.Status = store.StatusSuccess
		a.printf("redo %s seq %d: success (%d segment(s))\n", file.Filename, seq, n)
		if play {
			playAfterRedo(a, file, segment)
		}
		return nil
	}

	msg := tts.TruncateRunes(ttsErr.Error(), 500)
	displayMsg := tts.TruncateRunes(strings.Join(strings.Fields(ttsErr.Error()), " "), 200)
	if wavutil.IsValidPCMFile(target) {
		if _, err := a.store.SetHashStatus(file.ID, segment.TextHash, store.StatusSuccess, nil, false, a.timestamp()); err != nil {
			return err
		}
		segment.Status = store.StatusSuccess
		a.errorf("redo %s seq %d failed: %s (existing audio kept)\n", file.Filename, seq, displayMsg)
		if play {
			playAfterRedo(a, file, segment)
		}
		return exitSilent(2)
	}

	if _, err := a.store.SetHashStatus(file.ID, segment.TextHash, store.StatusFailed, &msg, false, a.timestamp()); err != nil {
		return err
	}
	a.errorf("redo %s seq %d failed: %s\n", file.Filename, seq, displayMsg)
	return exitSilent(2)
}

// playAfterRedo plays the resulting audio without changing redo's exit status.
func playAfterRedo(a *app, file *store.File, segment *store.Segment) {
	if err := playSegment(a, file, segment); err != nil {
		if errors.Is(err, errPlaybackInterrupted) {
			a.errorf("playback interrupted\n")
			return
		}
		a.errorf("warning: playback failed: %v\n", err)
	}
}
