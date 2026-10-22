package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
	"github.com/LiZeC123/kokomi-narrator/internal/tts"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func newMakeCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "make <file|id>",
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

func runMake(a *app, fileArg string) error {
	file, err := resolveFile(a, fileArg)
	if err != nil {
		return err
	}
	name := file.Filename
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

	var jobs []ttsJob
	seen := make(map[string]bool, len(pending))
	for _, seg := range pending {
		if seen[seg.TextHash] {
			continue
		}
		seen[seg.TextHash] = true
		jobs = append(jobs, ttsJob{
			Hash: seg.TextHash,
			Text: seg.Text,
			Seq:  seg.Seq,
		})
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(sigCtx)
	defer cancel()

	prog := newProgress(a.errOut, len(pending))
	defer prog.close()

	client := tts.New(a.cfg.TTS)
	workerCount := a.cfg.TTS.Concurrency
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(jobs) {
		workerCount = len(jobs)
	}

	var (
		fatalOnce sync.Once
		fatalErr  error
		wg        sync.WaitGroup
	)

	jobsCh := make(chan ttsJob)
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		defer close(jobsCh)
		for _, job := range jobs {
			select {
			case jobsCh <- job:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer wg.Done()
			for job := range jobsCh {
				if ctx.Err() != nil {
					return
				}
				if err := a.runTTSJob(ctx, client, file, name, job, prog); err != nil {
					fatalOnce.Do(func() {
						fatalErr = err
						cancel()
					})
					return
				}
			}
		}()
	}
	wg.Wait()
	<-feedDone

	if sigCtx.Err() != nil {
		a.cleanupAfterAbort(file.ID, file.VoiceKey, jobs)
		a.errorf("interrupted\n")
		return exitSilent(130)
	}
	if fatalErr != nil {
		a.cleanupAfterAbort(file.ID, file.VoiceKey, jobs)
		return fatalErr
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

type ttsJob struct {
	Hash string
	Text string
	Seq  int
}

func (a *app) runTTSJob(ctx context.Context, client *tts.Client, file *store.File, name string, job ttsJob, prog *progress) error {
	now := a.timestamp()
	affected, err := a.store.SetHashStatus(file.ID, job.Hash, store.StatusProcessing, nil, true, now)
	if err != nil {
		return err
	}
	if affected == 0 {
		return nil
	}
	prog.starting(job.Hash)

	data, ttsErr := client.Synthesize(ctx, job.Text)
	if ttsErr != nil {
		if ctx.Err() != nil {
			return nil
		}
		msg := tts.TruncateRunes(ttsErr.Error(), 500)
		if _, err := a.store.SetHashStatus(file.ID, job.Hash, store.StatusFailed, &msg, false, now); err != nil {
			return err
		}
		a.log.Error("segment synthesis failed",
			"file", name,
			"seq", job.Seq,
			"hash", shortHash(job.Hash),
			"error", tts.TruncateRunes(msg, 200),
		)
		prog.complete(job.Hash, int(affected), true, msg)
		return nil
	}

	if ctx.Err() != nil {
		return nil
	}

	target := audioPath(a.cfg.AudioDir, file.VoiceKey, job.Hash)
	if err := wavutil.WriteAtomic(target, data); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		msg := tts.TruncateRunes(err.Error(), 500)
		if _, dbErr := a.store.SetHashStatus(file.ID, job.Hash, store.StatusFailed, &msg, false, now); dbErr != nil {
			return dbErr
		}
		a.log.Error("audio write failed",
			"file", name,
			"seq", job.Seq,
			"hash", shortHash(job.Hash),
			"error", tts.TruncateRunes(msg, 200),
		)
		prog.complete(job.Hash, int(affected), true, msg)
		return nil
	}

	if ctx.Err() != nil {
		return nil
	}
	if _, err := a.store.SetHashStatus(file.ID, job.Hash, store.StatusSuccess, nil, false, now); err != nil {
		return err
	}
	prog.complete(job.Hash, int(affected), false, "")
	return nil
}

// cleanupAfterAbort returns in-flight segments to pending and removes any
// partially written temp files. Successfully completed jobs stay untouched.
func (a *app) cleanupAfterAbort(fileID int64, voiceKey string, jobs []ttsJob) {
	if _, err := a.store.RecoverProcessing(fileID, "", a.timestamp()); err != nil {
		a.log.Error("recover processing segments failed", "error", err)
	}
	for _, job := range jobs {
		_ = os.Remove(audioPath(a.cfg.AudioDir, voiceKey, job.Hash) + ".tmp")
	}
}
