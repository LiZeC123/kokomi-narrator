package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
	"github.com/LiZeC123/kokomi-narrator/internal/tts"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func newRedoCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "redo <file> <seq>",
		Short: "Regenerate the audio for one speech segment",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runRedo(a, args[0], args[1])
			})
		},
	}
}

func runRedo(a *app, name, seqArg string) error {
	if err := validateFilename(name); err != nil {
		return err
	}
	seq, err := strconv.Atoi(seqArg)
	if err != nil {
		return fmt.Errorf("invalid seq %q: %w", seqArg, err)
	}
	if seq <= 0 {
		return fmt.Errorf("seq must be greater than zero")
	}

	file, err := a.store.FileByName(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%s is not registered (run add first)", name)
		}
		return err
	}
	segment, err := a.store.SegmentBySeq(file.ID, seq)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%s has no segment with seq %d", name, seq)
		}
		return err
	}
	if segment.Kind != textparse.KindSpeech {
		return fmt.Errorf("%s seq %d is a punctuation segment", name, seq)
	}

	currentVoice := currentVoiceKey(a.cfg)
	if file.VoiceKey != currentVoice {
		return fmt.Errorf("voice changed (%s -> %s); run make %s first", file.VoiceKey, currentVoice, name)
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
		a.printf("redo %s seq %d: success (%d segment(s))\n", name, seq, n)
		return nil
	}

	msg := tts.TruncateRunes(ttsErr.Error(), 500)
	displayMsg := tts.TruncateRunes(strings.Join(strings.Fields(ttsErr.Error()), " "), 200)
	if wavutil.IsValidPCMFile(target) {
		if _, err := a.store.SetHashStatus(file.ID, segment.TextHash, store.StatusSuccess, nil, false, a.timestamp()); err != nil {
			return err
		}
		a.errorf("redo %s seq %d failed: %s (existing audio kept)\n", name, seq, displayMsg)
		return exitSilent(2)
	}

	if _, err := a.store.SetHashStatus(file.ID, segment.TextHash, store.StatusFailed, &msg, false, a.timestamp()); err != nil {
		return err
	}
	a.errorf("redo %s seq %d failed: %s\n", name, seq, displayMsg)
	return exitSilent(2)
}
