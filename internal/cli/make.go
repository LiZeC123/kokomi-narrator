package cli

import (
	"context"
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

func newMakeCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "make <file>",
		Short: "Synthesize all pending speech segments",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runMake(a, args[0])
			})
		},
	}
}

type syncStats struct {
	missing    int
	found      int
	invalid    int
	processing int
}

func buildSync(audioDir, voiceKey string, segments []store.Segment) (syncStats, []store.SyncUpdate) {
	type audioState int
	const (
		stateFound audioState = iota
		stateMissing
		stateInvalid
	)

	states := make(map[string]audioState)
	var stats syncStats
	var updates []store.SyncUpdate

	for _, seg := range segments {
		if seg.Kind == textparse.KindPunctuation {
			if seg.Status != store.StatusSkipped || seg.Error != nil {
				updates = append(updates, store.SyncUpdate{ID: seg.ID, Status: store.StatusSkipped})
			}
			continue
		}

		state, ok := states[seg.TextHash]
		if !ok {
			_, err := wavutil.ReadFile(audioPath(audioDir, voiceKey, seg.TextHash))
			switch {
			case err == nil:
				state = stateFound
			case errors.Is(err, os.ErrNotExist):
				state = stateMissing
			default:
				state = stateInvalid
			}
			states[seg.TextHash] = state
		}

		newStatus := store.StatusPending
		switch state {
		case stateFound:
			stats.found++
			newStatus = store.StatusSuccess
		case stateMissing:
			stats.missing++
		case stateInvalid:
			stats.invalid++
		}
		if seg.Status == store.StatusProcessing && newStatus == store.StatusPending {
			stats.processing++
		}
		if seg.Status != newStatus || seg.Error != nil {
			updates = append(updates, store.SyncUpdate{ID: seg.ID, Status: newStatus})
		}
	}
	return stats, updates
}

func runMake(a *app, name string) error {
	if err := validateFilename(name); err != nil {
		return err
	}
	file, err := a.store.FileByName(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%s is not registered (run add first)", name)
		}
		return err
	}
	segments, err := a.store.SegmentsByFile(file.ID)
	if err != nil {
		return err
	}

	voiceKey := currentVoiceKey(a.cfg)
	if file.VoiceKey != voiceKey {
		oldVoice := file.VoiceKey
		if err := a.store.SwitchVoice(file.ID, voiceKey, a.timestamp()); err != nil {
			return err
		}
		a.printf("voice changed: %s -> %s, speech segments reset to pending\n", oldVoice, voiceKey)
		file.VoiceKey = voiceKey
		segments, err = a.store.SegmentsByFile(file.ID)
		if err != nil {
			return err
		}
	}

	stats, updates := buildSync(a.cfg.AudioDir, file.VoiceKey, segments)
	if err := a.store.ApplySync(file.ID, updates, a.timestamp()); err != nil {
		return err
	}
	segments, err = a.store.SegmentsByFile(file.ID)
	if err != nil {
		return err
	}

	syncLine := fmt.Sprintf("sync: %d missing -> pending, %d found -> success, %d processing -> pending",
		stats.missing, stats.found, stats.processing)
	if stats.invalid > 0 {
		syncLine += fmt.Sprintf(", %d invalid -> pending", stats.invalid)
	}
	a.printf("%s\n", syncLine)

	var pending []store.Segment
	initialSuccess := 0
	skipped := 0
	for _, seg := range segments {
		if seg.Kind == textparse.KindPunctuation {
			skipped++
			continue
		}
		switch seg.Status {
		case store.StatusPending:
			pending = append(pending, seg)
		case store.StatusSuccess:
			initialSuccess++
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	prog := newProgress(a.errOut, len(pending))
	defer prog.close()

	client := tts.New(a.cfg.TTS)
	processed := make(map[string]bool)
	currentHash := ""

	for _, seg := range pending {
		if ctx.Err() != nil {
			return a.interruptMake(file.ID, currentHash, file.VoiceKey)
		}
		if processed[seg.TextHash] {
			continue
		}
		processed[seg.TextHash] = true
		currentHash = seg.TextHash
		now := a.timestamp()

		affected, err := a.store.SetHashStatus(file.ID, seg.TextHash, store.StatusProcessing, nil, true, now)
		if err != nil {
			return err
		}
		if affected == 0 {
			currentHash = ""
			continue
		}
		prog.starting(seg.TextHash)

		data, ttsErr := client.Synthesize(ctx, seg.Text)
		if ttsErr != nil {
			if ctx.Err() != nil {
				return a.interruptMake(file.ID, currentHash, file.VoiceKey)
			}
			msg := tts.TruncateRunes(ttsErr.Error(), 500)
			if _, err := a.store.SetHashStatus(file.ID, seg.TextHash, store.StatusFailed, &msg, false, now); err != nil {
				return err
			}
			a.log.Error("segment synthesis failed",
				"file", name,
				"seq", seg.Seq,
				"hash", shortHash(seg.TextHash),
				"error", tts.TruncateRunes(msg, 200),
			)
			prog.complete(seg.TextHash, int(affected), true, msg)
			currentHash = ""
			continue
		}

		if ctx.Err() != nil {
			return a.interruptMake(file.ID, currentHash, file.VoiceKey)
		}

		target := audioPath(a.cfg.AudioDir, file.VoiceKey, seg.TextHash)
		if err := wavutil.WriteAtomic(target, data); err != nil {
			msg := tts.TruncateRunes(err.Error(), 500)
			if _, dbErr := a.store.SetHashStatus(file.ID, seg.TextHash, store.StatusFailed, &msg, false, now); dbErr != nil {
				return dbErr
			}
			a.log.Error("audio write failed",
				"file", name,
				"seq", seg.Seq,
				"hash", shortHash(seg.TextHash),
				"error", tts.TruncateRunes(msg, 200),
			)
			prog.complete(seg.TextHash, int(affected), true, msg)
			currentHash = ""
			continue
		}

		if _, err := a.store.SetHashStatus(file.ID, seg.TextHash, store.StatusSuccess, nil, false, now); err != nil {
			return err
		}
		prog.complete(seg.TextHash, int(affected), false, "")
		currentHash = ""
	}

	finalSegments, err := a.store.SegmentsByFile(file.ID)
	if err != nil {
		return err
	}
	success, failed := 0, 0
	for _, seg := range finalSegments {
		switch seg.Status {
		case store.StatusSuccess:
			success++
		case store.StatusFailed:
			failed++
		}
	}
	reused := initialSuccess

	a.printf("make %s: success=%d failed=%d skipped=%d reused=%d\n",
		name, success, failed, skipped, reused)
	if failed > 0 {
		a.log.Error("make finished with failures",
			"file", name,
			"failed", failed,
			"hint", fmt.Sprintf("run `ttsctl ls %s` to inspect segment errors", name),
		)
		return exitSilent(2)
	}
	return nil
}

func (a *app) interruptMake(fileID int64, hash, voiceKey string) error {
	now := a.timestamp()
	if hash != "" {
		_, _ = a.store.SetHashStatus(fileID, hash, store.StatusPending, nil, false, now)
		_ = os.Remove(audioPath(a.cfg.AudioDir, voiceKey, hash) + ".tmp")
	} else {
		_, _ = a.store.RecoverProcessing(fileID, "", now)
	}
	a.errorf("interrupted\n")
	return exitSilent(130)
}
