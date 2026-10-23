package bgm

import (
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// ErrNoUsableTracks means every library track is longer than the target.
var ErrNoUsableTracks = errors.New("no BGM tracks fit the target duration")

// BuildPlan selects whole tracks until the target is reached. Tracks that are
// longer than the target are ignored. If the library is exhausted, it is
// reshuffled and reused; when nothing fits the remaining time, the remainder
// becomes silence.
func BuildPlan(tracks []Track, target time.Duration, rng *rand.Rand) (Plan, error) {
	if target <= 0 {
		return Plan{}, fmt.Errorf("target duration must be greater than zero")
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	usable := make([]Track, 0, len(tracks))
	for _, track := range tracks {
		if track.Duration > 0 && track.Duration <= target {
			usable = append(usable, track)
		}
	}
	if len(usable) == 0 {
		return Plan{}, ErrNoUsableTracks
	}

	var selected []Track
	remaining := target
	for remaining > 0 {
		progressed := false
		for _, idx := range rng.Perm(len(usable)) {
			duration := usable[idx].Duration
			if duration > remaining {
				continue
			}
			selected = append(selected, usable[idx])
			remaining -= duration
			progressed = true
			if remaining == 0 {
				break
			}
		}
		if !progressed {
			break
		}
	}
	if len(selected) == 0 {
		return Plan{}, ErrNoUsableTracks
	}

	return Plan{
		Tracks:   selected,
		Gaps:     randomGaps(len(selected), remaining, rng),
		Target:   target,
		Sum:      target - remaining,
		TotalGap: remaining,
	}, nil
}

// randomGaps distributes total across the m+1 gap positions. It follows the
// original bgm_tmp.go behaviour: at most three positions receive silence.
func randomGaps(m int, total time.Duration, rng *rand.Rand) []time.Duration {
	gaps := make([]time.Duration, m+1)
	if m == 0 || total <= 0 {
		return gaps
	}

	k := 1 + rng.Intn(min(3, m+1))
	positions := rng.Perm(m + 1)[:k]

	weights := make([]float64, k)
	var weightSum float64
	for i := range weights {
		weights[i] = rng.Float64() + 0.1
		weightSum += weights[i]
	}

	remaining := total
	for i, pos := range positions {
		if i == k-1 {
			gaps[pos] = remaining
			continue
		}
		gap := time.Duration(float64(total) * weights[i] / weightSum)
		gaps[pos] = gap
		remaining -= gap
	}
	return gaps
}
