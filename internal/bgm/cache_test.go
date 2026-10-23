package bgm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
)

type fakeCache struct {
	tracks map[string]store.BgmTrack
}

func newFakeCache() *fakeCache {
	return &fakeCache{tracks: make(map[string]store.BgmTrack)}
}

func (c *fakeCache) BgmTracksByDir(dir string) ([]store.BgmTrack, error) {
	var out []store.BgmTrack
	for _, track := range c.tracks {
		if track.Dir == dir {
			out = append(out, track)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (c *fakeCache) UpsertBgmTracks(tracks []store.BgmTrack) error {
	for _, track := range tracks {
		c.tracks[track.Path] = track
	}
	return nil
}

func (c *fakeCache) PruneBgmTracks(dir string, keepPaths []string) (int, error) {
	keep := make(map[string]struct{}, len(keepPaths))
	for _, path := range keepPaths {
		keep[path] = struct{}{}
	}
	removed := 0
	for path, track := range c.tracks {
		if track.Dir != dir {
			continue
		}
		if _, ok := keep[path]; !ok {
			delete(c.tracks, path)
			removed++
		}
	}
	return removed, nil
}

type fakeProber struct {
	mu        sync.Mutex
	calls     int
	durations map[string]time.Duration
	errs      map[string]error
}

func (p *fakeProber) Probe(_ context.Context, path string) (time.Duration, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if err := p.errs[path]; err != nil {
		return 0, err
	}
	duration, ok := p.durations[path]
	if !ok {
		return 0, fmt.Errorf("unexpected probe %s", path)
	}
	return duration, nil
}

func (p *fakeProber) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestSyncLibraryUsesCache(t *testing.T) {
	dir := t.TempDir()
	paths := map[string]string{}
	for name, content := range map[string]string{
		"a.mp3": "a",
		"b.mp3": "bb",
		"c.wav": "ccc",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}

	prober := &fakeProber{
		durations: map[string]time.Duration{
			paths["a.mp3"]: time.Second,
			paths["b.mp3"]: 2 * time.Second,
			paths["c.wav"]: 3 * time.Second,
		},
		errs: map[string]error{},
	}
	cache := newFakeCache()
	ctx := context.Background()

	first, err := SyncLibrary(ctx, cache, prober, dir, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("SyncLibrary() first error = %v", err)
	}
	if len(first.Tracks) != 3 || first.Stats.Probed != 3 || first.Stats.Cached != 0 {
		t.Fatalf("first sync = %+v", first)
	}
	if got := prober.callCount(); got != 3 {
		t.Fatalf("probe calls = %d, want 3", got)
	}

	second, err := SyncLibrary(ctx, cache, prober, dir, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("SyncLibrary() second error = %v", err)
	}
	if len(second.Tracks) != 3 || second.Stats.Cached != 3 || second.Stats.Probed != 0 {
		t.Fatalf("second sync = %+v", second)
	}
	if got := prober.callCount(); got != 3 {
		t.Fatalf("probe calls after cache hit = %d, want 3", got)
	}

	// Changing a file's size invalidates only that cache entry.
	prober.durations[paths["a.mp3"]] = 4 * time.Second
	if err := os.WriteFile(paths["a.mp3"], []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := SyncLibrary(ctx, cache, prober, dir, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("SyncLibrary() changed error = %v", err)
	}
	if third.Stats.Probed != 1 || third.Stats.Cached != 2 {
		t.Fatalf("changed sync stats = %+v", third.Stats)
	}
	if got := prober.callCount(); got != 4 {
		t.Fatalf("probe calls after change = %d, want 4", got)
	}
	for _, track := range third.Tracks {
		if track.Path == paths["a.mp3"] && track.Duration != 4*time.Second {
			t.Fatalf("updated duration = %v, want 4s", track.Duration)
		}
	}

	// Removing a file prunes its cache row.
	if err := os.Remove(paths["b.mp3"]); err != nil {
		t.Fatal(err)
	}
	fourth, err := SyncLibrary(ctx, cache, prober, dir, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("SyncLibrary() removed error = %v", err)
	}
	if fourth.Stats.Removed != 1 || len(fourth.Tracks) != 2 {
		t.Fatalf("removed sync = %+v", fourth)
	}
	if _, ok := cache.tracks[paths["b.mp3"]]; ok {
		t.Fatal("removed file is still cached")
	}

	// A probe failure is reported but does not stop the usable tracks.
	badPath := filepath.Join(dir, "bad.mp3")
	if err := os.WriteFile(badPath, []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	prober.errs[badPath] = errors.New("corrupt")
	fifth, err := SyncLibrary(ctx, cache, prober, dir, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("SyncLibrary() bad file error = %v", err)
	}
	if fifth.Stats.Invalid != 1 || len(fifth.Failures) != 1 || len(fifth.Tracks) != 2 {
		t.Fatalf("bad file sync = %+v", fifth)
	}
	if _, ok := cache.tracks[badPath]; ok {
		t.Fatal("failed probe was cached")
	}
}
