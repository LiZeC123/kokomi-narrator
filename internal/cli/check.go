package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/config"
	"github.com/LiZeC123/kokomi-narrator/internal/silencedetect"
	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/tts"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func newCheckCommand(configPath *string) *cobra.Command {
	var redo bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Detect long silences in synthesized audio and optionally redo bad segments",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runCheck(a, redo)
			})
		},
	}
	cmd.Flags().BoolVar(&redo, "redo", false, "redo problems from check_results.txt, or detect first when the file is missing")
	return cmd
}

type audioKey struct {
	voiceKey string
	textHash string
}

type checkCandidate struct {
	path         string
	voiceKey     string
	textHash     string
	refs         []store.SpeechRef
	duration     time.Duration
	intervals    []silencedetect.Interval
	detectErr    error
	cachedDetail string
}

func (c *checkCandidate) problem() bool {
	return c.detectErr != nil || len(c.intervals) > 0
}

func runCheck(a *app, redo bool) error {
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(sigCtx)
	defer cancel()

	if redo {
		return runCheckRedo(ctx, a)
	}
	return runCheckInspect(ctx, a)
}

// runCheckInspect always performs a full detection and replaces the cache file.
func runCheckInspect(ctx context.Context, a *app) error {
	candidates, detector, err := prepareCheck(a)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		if _, err := writeCheckCache(checkResultsFile, nil); err != nil {
			return err
		}
		a.printf("check: saved clean result to %s\n", checkResultsFile)
		return nil
	}

	printCheckHeader(a, len(candidates))
	detectCandidates(ctx, detector, candidates, a.cfg.Check)
	if ctx.Err() != nil {
		a.errorf("interrupted\n")
		return exitSilent(130)
	}

	problems := filterCheckProblems(candidates)
	if len(problems) == 0 {
		a.printf("check: all clean\n")
		lines, err := writeCheckCache(checkResultsFile, nil)
		if err != nil {
			return err
		}
		a.printf("check: saved clean result to %s (%d entries)\n", checkResultsFile, lines)
		return nil
	}

	printCheckProblems(a, problems)
	a.printf("check: %d problematic wav(s), %d segment(s) affected\n",
		len(problems), countCheckSegments(problems))

	lines, err := writeCheckCache(checkResultsFile, problems)
	if err != nil {
		return err
	}
	a.printf("check: saved %d problem(s) to %s\n", lines, checkResultsFile)
	return exitSilent(2)
}

// runCheckRedo reuses check_results.txt when it exists, so the expensive full
// detection pass is skipped. Without a cache it falls back to detection.
func runCheckRedo(ctx context.Context, a *app) error {
	entries, exists, invalid, err := readCheckCache(checkResultsFile)
	if err != nil {
		return err
	}
	if invalid > 0 {
		a.errorf("warning: ignored %d invalid line(s) in %s\n", invalid, checkResultsFile)
	}

	var (
		candidates []*checkCandidate
		detector   *silencedetect.Detector
	)

	if exists {
		if len(entries) == 0 {
			if invalid > 0 {
				return fmt.Errorf("%s contains no valid entries; delete it or run check again", checkResultsFile)
			}
			a.printf("check: cached result is clean (%s)\n", checkResultsFile)
			return nil
		}

		candidates, err = cachedCheckCandidates(a, entries)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			a.printf("check: cached entries are stale; nothing to redo\n")
			if _, err := writeCheckCache(checkResultsFile, nil); err != nil {
				return err
			}
			return nil
		}

		detector, err = newCheckDetector()
		if err != nil {
			return err
		}
		a.printf("check: using cached results from %s\n", checkResultsFile)
		printCheckProblems(a, candidates)
		a.printf("check: %d problematic wav(s), %d segment(s) affected\n",
			len(candidates), countCheckSegments(candidates))
	} else {
		candidates, detector, err = prepareCheck(a)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			if _, err := writeCheckCache(checkResultsFile, nil); err != nil {
				return err
			}
			a.printf("check: saved clean result to %s\n", checkResultsFile)
			return nil
		}

		printCheckHeader(a, len(candidates))
		detectCandidates(ctx, detector, candidates, a.cfg.Check)
		if ctx.Err() != nil {
			a.errorf("interrupted\n")
			return exitSilent(130)
		}

		problems := filterCheckProblems(candidates)
		if len(problems) == 0 {
			a.printf("check: all clean\n")
			if _, err := writeCheckCache(checkResultsFile, nil); err != nil {
				return err
			}
			a.printf("check: saved clean result to %s\n", checkResultsFile)
			return nil
		}
		candidates = problems
		printCheckProblems(a, problems)
		a.printf("check: %d problematic wav(s), %d segment(s) affected\n",
			len(problems), countCheckSegments(problems))
	}

	outcomes := redoProblems(ctx, a, detector, candidates)
	if ctx.Err() != nil {
		remaining := remainingCheckCandidates(candidates, outcomes)
		if _, cacheErr := writeCheckCache(checkResultsFile, remaining); cacheErr != nil {
			a.errorf("warning: update %s: %v\n", checkResultsFile, cacheErr)
		}
		a.errorf("interrupted\n")
		return exitSilent(130)
	}

	remainingCount := printRedoResults(a, outcomes)
	remaining := remainingCheckCandidates(candidates, outcomes)
	lines, cacheErr := writeCheckCache(checkResultsFile, remaining)
	if remainingCount == 0 {
		a.printf("check: all problematic audio fixed\n")
		if cacheErr == nil {
			a.printf("check: saved clean result to %s\n", checkResultsFile)
		}
	} else if cacheErr == nil {
		a.printf("check: saved %d unresolved segment(s) to %s\n", lines, checkResultsFile)
	}
	if cacheErr != nil {
		return cacheErr
	}
	if remainingCount == 0 {
		return nil
	}
	return exitSilent(2)
}

// prepareCheck loads speech refs, scans the audio directory and locates ffmpeg.
func prepareCheck(a *app) ([]*checkCandidate, *silencedetect.Detector, error) {
	refs, err := a.store.SpeechRefs()
	if err != nil {
		return nil, nil, err
	}
	if len(refs) == 0 {
		a.printf("check: no speech segments registered\n")
		return nil, nil, nil
	}

	byKey := make(map[audioKey][]store.SpeechRef, len(refs))
	for _, ref := range refs {
		key := audioKey{voiceKey: ref.VoiceKey, textHash: ref.TextHash}
		byKey[key] = append(byKey[key], ref)
	}

	candidates, err := scanCheckCandidates(a, byKey)
	if err != nil {
		return nil, nil, err
	}
	if len(candidates) == 0 {
		a.printf("check: no audio files found under %s\n", a.cfg.AudioDir)
		return nil, nil, nil
	}

	detector, err := newCheckDetector()
	if err != nil {
		return nil, nil, err
	}
	return candidates, detector, nil
}

func newCheckDetector() (*silencedetect.Detector, error) {
	ffmpeg, err := silencedetect.FindFFmpeg()
	if err != nil {
		return nil, err
	}
	return silencedetect.New(ffmpeg), nil
}

func filterCheckProblems(candidates []*checkCandidate) []*checkCandidate {
	var problems []*checkCandidate
	for _, candidate := range candidates {
		if candidate.problem() {
			problems = append(problems, candidate)
		}
	}
	return problems
}

func countCheckSegments(candidates []*checkCandidate) int {
	total := 0
	for _, candidate := range candidates {
		total += len(candidate.refs)
	}
	return total
}

func remainingCheckCandidates(candidates []*checkCandidate, outcomes []redoOutcome) []*checkCandidate {
	remaining := make([]*checkCandidate, 0, len(candidates))
	for i, candidate := range candidates {
		if i < len(outcomes) && outcomes[i].status == redoFixed {
			continue
		}
		remaining = append(remaining, candidate)
	}
	return remaining
}

func printCheckHeader(a *app, candidateCount int) {
	a.printf("check: %d wav file(s), silence_db=%sdB, silence_duration=%s\n",
		candidateCount, formatCheckNumber(a.cfg.Check.SilenceDB), a.cfg.Check.SilenceDuration.Std())
}

func scanCheckCandidates(a *app, byKey map[audioKey][]store.SpeechRef) ([]*checkCandidate, error) {
	var candidates []*checkCandidate
	err := filepath.WalkDir(a.cfg.AudioDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".wav") {
			return nil
		}

		rel, err := filepath.Rel(a.cfg.AudioDir, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		hash := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
		if len(parts) != 3 || !isHexHash(hash) || parts[1] != hash[:2] {
			a.errorf("warning: skipping %s (unexpected audio path)\n", path)
			return nil
		}

		refs := byKey[audioKey{voiceKey: parts[0], textHash: hash}]
		if len(refs) == 0 {
			a.errorf("warning: skipping %s (not referenced by any speech segment; run clean)\n", path)
			return nil
		}

		candidates = append(candidates, &checkCandidate{
			path:     path,
			voiceKey: parts[0],
			textHash: hash,
			refs:     refs,
		})
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].refs[0], candidates[j].refs[0]
		if a.FileID != b.FileID {
			return a.FileID < b.FileID
		}
		if a.Seq != b.Seq {
			return a.Seq < b.Seq
		}
		return candidates[i].textHash < candidates[j].textHash
	})
	return candidates, nil
}

func detectCandidates(ctx context.Context, detector *silencedetect.Detector, candidates []*checkCandidate, cfg config.Check) {
	workers := cfg.Concurrency
	if workers < 1 {
		workers = 1
	}
	if workers > len(candidates) {
		workers = len(candidates)
	}

	jobs := make(chan int, len(candidates))
	for i := range candidates {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if ctx.Err() != nil {
					return
				}
				candidate := candidates[idx]
				if info, err := wavutil.InspectFile(candidate.path); err == nil {
					candidate.duration = info.Duration
				}
				intervals, err := detector.Detect(ctx, candidate.path, cfg.SilenceDB, cfg.SilenceDuration.Std())
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					candidate.detectErr = err
					continue
				}
				candidate.intervals = intervals
			}
		}()
	}
	wg.Wait()
}

func printCheckProblems(a *app, candidates []*checkCandidate) {
	a.printf("%-8s%-5s%-18s%-10s%-10s%s\n", "FILE_ID", "SEQ", "FILE", "HASH", "DURATION", "SILENCES")
	for _, candidate := range candidates {
		details := checkDetails(candidate)
		for i, ref := range candidate.refs {
			file := tts.TruncateRunes(ref.Filename, 18)
			duration := formatCheckDuration(candidate.duration)
			if i == 0 {
				detail := ""
				if len(details) > 0 {
					detail = details[0]
				}
				a.printf("%-8d%-5d%-18s%-10s%-10s%s\n",
					ref.FileID, ref.Seq, file, shortHash(candidate.textHash), duration, detail)
				for _, extra := range details[1:] {
					a.printf("%-8s%-5s%-18s%-10s%-10s%s\n", "", "", "", "", "", extra)
				}
				continue
			}
			a.printf("%-8d%-5d%-18s%-10s%-10s%s\n",
				ref.FileID, ref.Seq, file, shortHash(candidate.textHash), duration, "(same wav)")
		}
	}
}

func checkDetails(candidate *checkCandidate) []string {
	if candidate.cachedDetail != "" {
		parts := strings.Split(candidate.cachedDetail, "; ")
		details := make([]string, 0, len(parts))
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				details = append(details, part)
			}
		}
		return details
	}
	if candidate.detectErr != nil {
		msg := strings.Join(strings.Fields(candidate.detectErr.Error()), " ")
		return []string{"[ffmpeg error: " + tts.TruncateRunes(msg, 120) + "]"}
	}
	details := make([]string, 0, len(candidate.intervals))
	for _, interval := range candidate.intervals {
		details = append(details, fmt.Sprintf("%s-%s (%s)",
			formatCheckSeconds(interval.Start),
			formatCheckSeconds(interval.End),
			formatCheckSeconds(interval.Duration())))
	}
	return details
}

func formatCheckDuration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func formatCheckSeconds(d time.Duration) string {
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func formatCheckNumber(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", v), "0"), ".")
}

type redoStatus int

const (
	redoPending redoStatus = iota
	redoFixed
	redoFailed
	redoStillBad
)

type redoOutcome struct {
	candidate *checkCandidate
	status    redoStatus
	reason    string
}

func redoProblems(ctx context.Context, a *app, detector *silencedetect.Detector, candidates []*checkCandidate) []redoOutcome {
	outcomes := make([]redoOutcome, len(candidates))
	workers := a.cfg.TTS.Concurrency
	if workers < 1 {
		workers = 1
	}
	if workers > len(candidates) {
		workers = len(candidates)
	}

	client := tts.New(a.cfg.TTS)
	currentVoice := currentVoiceKey(a.cfg)

	jobs := make(chan int, len(candidates))
	for i := range candidates {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if ctx.Err() != nil {
					return
				}
				outcomes[idx] = a.redoProblem(ctx, client, detector, candidates[idx], currentVoice)
			}
		}()
	}
	wg.Wait()
	return outcomes
}

func (a *app) redoProblem(ctx context.Context, client *tts.Client, detector *silencedetect.Detector, candidate *checkCandidate, currentVoice string) redoOutcome {
	if candidate.voiceKey != currentVoice {
		return redoOutcome{
			candidate: candidate,
			status:    redoFailed,
			reason: fmt.Sprintf("voice changed (%s -> %s); run make %s first",
				candidate.voiceKey, currentVoice, candidate.refs[0].Filename),
		}
	}

	text := candidate.refs[0].Text
	data, err := client.Synthesize(ctx, text)
	if err == nil {
		err = wavutil.WriteAtomic(candidate.path, data)
	}
	if err != nil {
		if ctx.Err() != nil {
			return redoOutcome{candidate: candidate, status: redoFailed, reason: "interrupted"}
		}
		msg := tts.TruncateRunes(strings.Join(strings.Fields(err.Error()), " "), 160)
		if wavutil.IsValidPCMFile(candidate.path) {
			if dbErr := a.markHash(candidate.refs, candidate.textHash, store.StatusSuccess, nil); dbErr != nil {
				return redoOutcome{candidate: candidate, status: redoFailed, reason: "update database: " + dbErr.Error()}
			}
		} else {
			full := tts.TruncateRunes(err.Error(), 500)
			if dbErr := a.markHash(candidate.refs, candidate.textHash, store.StatusFailed, &full); dbErr != nil {
				return redoOutcome{candidate: candidate, status: redoFailed, reason: "update database: " + dbErr.Error()}
			}
		}
		return redoOutcome{candidate: candidate, status: redoFailed, reason: msg}
	}

	if dbErr := a.markHash(candidate.refs, candidate.textHash, store.StatusSuccess, nil); dbErr != nil {
		return redoOutcome{candidate: candidate, status: redoFailed, reason: "update database: " + dbErr.Error()}
	}

	if info, inspectErr := wavutil.InspectFile(candidate.path); inspectErr == nil {
		candidate.duration = info.Duration
	}
	intervals, detectErr := detector.Detect(ctx, candidate.path, a.cfg.Check.SilenceDB, a.cfg.Check.SilenceDuration.Std())
	candidate.cachedDetail = ""
	candidate.intervals = intervals
	candidate.detectErr = detectErr
	if ctx.Err() != nil {
		return redoOutcome{candidate: candidate, status: redoFailed, reason: "interrupted"}
	}
	if detectErr != nil {
		msg := tts.TruncateRunes(strings.Join(strings.Fields(detectErr.Error()), " "), 160)
		return redoOutcome{candidate: candidate, status: redoStillBad, reason: "re-check failed: " + msg}
	}
	if len(intervals) > 0 {
		return redoOutcome{candidate: candidate, status: redoStillBad, reason: "silence remains after redo"}
	}
	return redoOutcome{candidate: candidate, status: redoFixed}
}

func (a *app) markHash(refs []store.SpeechRef, textHash, status string, errorMessage *string) error {
	now := a.timestamp()
	seen := make(map[int64]struct{}, len(refs))
	for _, ref := range refs {
		if _, ok := seen[ref.FileID]; ok {
			continue
		}
		seen[ref.FileID] = struct{}{}
		if _, err := a.store.SetHashStatus(ref.FileID, textHash, status, errorMessage, false, now); err != nil {
			return err
		}
	}
	return nil
}

func printRedoResults(a *app, outcomes []redoOutcome) int {
	var fixedWavs, failedWavs, stillWavs int
	var fixedSegments, failedSegments int
	for _, outcome := range outcomes {
		switch outcome.status {
		case redoFixed:
			fixedWavs++
			fixedSegments += len(outcome.candidate.refs)
		case redoStillBad:
			stillWavs++
			failedSegments += len(outcome.candidate.refs)
		case redoFailed:
			failedWavs++
			failedSegments += len(outcome.candidate.refs)
		}
	}

	a.printf("redo: fixed=%d wav(s)/%d segment(s), failed=%d wav(s), still_bad=%d wav(s), unresolved_segments=%d\n",
		fixedWavs, fixedSegments, failedWavs, stillWavs, failedSegments)
	printRedoEntries(a, outcomes, redoFailed, "failed")
	printRedoEntries(a, outcomes, redoStillBad, "still bad")
	return failedWavs + stillWavs
}

func printRedoEntries(a *app, outcomes []redoOutcome, status redoStatus, label string) {
	printed := false
	for _, outcome := range outcomes {
		if outcome.status != status {
			continue
		}
		if !printed {
			a.printf("%s entries:\n", label)
			printed = true
		}
		detail := outcome.reason
		if status == redoStillBad {
			if details := checkDetails(outcome.candidate); len(details) > 0 {
				detail = strings.Join(details, "; ")
			}
		}
		for _, ref := range outcome.candidate.refs {
			a.printf("  file=%d seq=%d %s hash=%s: %s\n",
				ref.FileID, ref.Seq, ref.Filename, shortHash(outcome.candidate.textHash), detail)
		}
	}
}
