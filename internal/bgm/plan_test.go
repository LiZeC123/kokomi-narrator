package bgm

import (
	"errors"
	"math/rand"
	"testing"
	"time"
)

func track(seconds int) Track {
	return Track{
		Path:     "track.mp3",
		Filename: "track.mp3",
		Duration: time.Duration(seconds) * time.Second,
	}
}

func TestBuildPlanLoopsUntilTarget(t *testing.T) {
	tracks := []Track{track(1), track(2), track(3), track(30)}
	plan, err := BuildPlan(tracks, 10*time.Second, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if plan.Sum+plan.TotalGap != plan.Target {
		t.Fatalf("sum %v + gap %v != target %v", plan.Sum, plan.TotalGap, plan.Target)
	}
	if plan.Sum > plan.Target || plan.TotalGap < 0 {
		t.Fatalf("invalid plan: sum=%v gap=%v", plan.Sum, plan.TotalGap)
	}
	if len(plan.Gaps) != len(plan.Tracks)+1 {
		t.Fatalf("gaps = %d, want %d", len(plan.Gaps), len(plan.Tracks)+1)
	}
	var sum time.Duration
	for _, item := range plan.Tracks {
		if item.Duration > plan.Target {
			t.Fatalf("selected track longer than target: %v", item.Duration)
		}
		sum += item.Duration
	}
	if sum != plan.Sum {
		t.Fatalf("selected sum = %v, plan.Sum = %v", sum, plan.Sum)
	}
	var gaps time.Duration
	for _, gap := range plan.Gaps {
		if gap < 0 {
			t.Fatalf("negative gap: %v", gap)
		}
		gaps += gap
	}
	if gaps != plan.TotalGap {
		t.Fatalf("gap sum = %v, plan.TotalGap = %v", gaps, plan.TotalGap)
	}
}

func TestBuildPlanFillsRemainderWithSilence(t *testing.T) {
	plan, err := BuildPlan([]Track{track(3)}, 5*time.Second, rand.New(rand.NewSource(2)))
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if len(plan.Tracks) != 1 || plan.Sum != 3*time.Second {
		t.Fatalf("tracks = %+v, sum = %v", plan.Tracks, plan.Sum)
	}
	if plan.TotalGap != 2*time.Second {
		t.Fatalf("TotalGap = %v, want 2s", plan.TotalGap)
	}
}

func TestBuildPlanExactTarget(t *testing.T) {
	plan, err := BuildPlan([]Track{track(2), track(3)}, 5*time.Second, rand.New(rand.NewSource(3)))
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if plan.Sum != 5*time.Second || plan.TotalGap != 0 {
		t.Fatalf("sum = %v, gap = %v, want 5s/0", plan.Sum, plan.TotalGap)
	}
}

func TestBuildPlanRejectsLongTracks(t *testing.T) {
	_, err := BuildPlan([]Track{track(10)}, 5*time.Second, rand.New(rand.NewSource(4)))
	if !errors.Is(err, ErrNoUsableTracks) {
		t.Fatalf("BuildPlan() error = %v, want ErrNoUsableTracks", err)
	}
	_, err = BuildPlan(nil, 5*time.Second, rand.New(rand.NewSource(4)))
	if !errors.Is(err, ErrNoUsableTracks) {
		t.Fatalf("BuildPlan(empty) error = %v, want ErrNoUsableTracks", err)
	}
	if _, err := BuildPlan([]Track{track(1)}, 0, rand.New(rand.NewSource(4))); err == nil {
		t.Fatal("BuildPlan() accepted a zero target")
	}
}

func TestRandomGapsUsesAtMostThreePositions(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 100; i++ {
		gaps := randomGaps(10, 10*time.Second, rng)
		nonZero := 0
		var total time.Duration
		for _, gap := range gaps {
			if gap > 0 {
				nonZero++
			}
			total += gap
		}
		if nonZero > 3 {
			t.Fatalf("non-zero gaps = %d, want at most 3", nonZero)
		}
		if total != 10*time.Second && total != 0 {
			t.Fatalf("gap total = %v, want 10s", total)
		}
	}
}
