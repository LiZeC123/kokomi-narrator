package bgm

import (
	"context"
	"sync"
	"time"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
)

const probeWorkers = 4

// Cache is the subset of the store used by the BGM library cache.
type Cache interface {
	BgmTracksByDir(dir string) ([]store.BgmTrack, error)
	UpsertBgmTracks(tracks []store.BgmTrack) error
	PruneBgmTracks(dir string, keepPaths []string) (int, error)
}

// ProbeFailure describes a music file whose duration could not be read.
type ProbeFailure struct {
	Path string
	Err  error
}

// SyncStats summarizes one library scan.
type SyncStats struct {
	Files   int
	Cached  int
	Probed  int
	Removed int
	Invalid int
}

// SyncResult is the usable library plus cache statistics.
type SyncResult struct {
	Tracks   []Track
	Stats    SyncStats
	Failures []ProbeFailure
}

// SyncLibrary scans dir and refreshes the duration cache. Files whose size and
// mtime are unchanged reuse the cached duration; only new or modified files
// are passed to ffprobe.
func SyncLibrary(ctx context.Context, cache Cache, prober Prober, dir, now string) (*SyncResult, error) {
	absDir, files, err := ScanDir(dir)
	if err != nil {
		return nil, err
	}

	cached, err := cache.BgmTracksByDir(absDir)
	if err != nil {
		return nil, err
	}
	cachedByPath := make(map[string]store.BgmTrack, len(cached))
	for _, track := range cached {
		cachedByPath[track.Path] = track
	}

	type probeTask struct {
		index int
		file  ScannedFile
	}
	type probeOutcome struct {
		duration time.Duration
		err      error
	}

	entries := make([]Track, len(files))
	valid := make([]bool, len(files))
	result := &SyncResult{Stats: SyncStats{Files: len(files)}}
	var tasks []probeTask

	for i, file := range files {
		row, ok := cachedByPath[file.Path]
		if ok && row.Size == file.Size && row.MtimeNS == file.MtimeNS && row.DurationMS > 0 {
			entries[i] = Track{
				Path:     file.Path,
				Filename: file.Filename,
				Duration: time.Duration(row.DurationMS) * time.Millisecond,
			}
			valid[i] = true
			result.Stats.Cached++
			continue
		}
		tasks = append(tasks, probeTask{index: i, file: file})
	}

	outcomes := make([]probeOutcome, len(tasks))
	sem := make(chan struct{}, probeWorkers)
	var wg sync.WaitGroup
	for i, task := range tasks {
		wg.Add(1)
		go func(i int, task probeTask) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				outcomes[i].err = ctx.Err()
				return
			}
			duration, err := prober.Probe(ctx, task.file.Path)
			outcomes[i] = probeOutcome{duration: duration, err: err}
		}(i, task)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	upserts := make([]store.BgmTrack, 0, len(tasks))
	for i, task := range tasks {
		outcome := outcomes[i]
		if outcome.err != nil {
			result.Stats.Invalid++
			result.Failures = append(result.Failures, ProbeFailure{Path: task.file.Path, Err: outcome.err})
			continue
		}
		entries[task.index] = Track{
			Path:     task.file.Path,
			Filename: task.file.Filename,
			Duration: outcome.duration,
		}
		valid[task.index] = true
		result.Stats.Probed++
		upserts = append(upserts, store.BgmTrack{
			Path:       task.file.Path,
			Dir:        absDir,
			Filename:   task.file.Filename,
			DurationMS: outcome.duration.Milliseconds(),
			Size:       task.file.Size,
			MtimeNS:    task.file.MtimeNS,
			UpdatedAt:  now,
		})
	}

	if err := cache.UpsertBgmTracks(upserts); err != nil {
		return nil, err
	}

	keep := make([]string, len(files))
	for i, file := range files {
		keep[i] = file.Path
	}
	removed, err := cache.PruneBgmTracks(absDir, keep)
	if err != nil {
		return nil, err
	}
	result.Stats.Removed = removed

	for i := range entries {
		if valid[i] {
			result.Tracks = append(result.Tracks, entries[i])
		}
	}
	return result, nil
}

var _ Cache = (*store.Store)(nil)
