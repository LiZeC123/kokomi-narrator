package bgm

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

// Render transcodes the plan's tracks, inserts the planned silence and writes a
// single 48 kHz stereo PCM WAV to outPath via a temporary file.
func Render(ctx context.Context, tool *Tool, plan Plan, outPath string) (time.Duration, error) {
	if len(plan.Tracks) == 0 {
		return 0, fmt.Errorf("no BGM tracks to render")
	}
	if len(plan.Gaps) != len(plan.Tracks)+1 {
		return 0, fmt.Errorf("invalid BGM plan: %d tracks but %d gaps", len(plan.Tracks), len(plan.Gaps))
	}

	workDir, err := os.MkdirTemp("", "ttsctl-bgm-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(workDir)

	var segments []string
	for i, track := range plan.Tracks {
		if gap := plan.Gaps[i]; gap > 10*time.Millisecond {
			path := filepath.Join(workDir, fmt.Sprintf("sil_%03d.wav", i))
			if err := tool.Silence(ctx, gap, path); err != nil {
				return 0, fmt.Errorf("generate silence before %s: %w", track.Filename, err)
			}
			segments = append(segments, path)
		}

		path := filepath.Join(workDir, fmt.Sprintf("seg_%03d.wav", i))
		if err := tool.Transcode(ctx, track.Path, path); err != nil {
			return 0, fmt.Errorf("transcode %s: %w", track.Filename, err)
		}
		segments = append(segments, path)
	}
	if gap := plan.Gaps[len(plan.Tracks)]; gap > 10*time.Millisecond {
		path := filepath.Join(workDir, "sil_end.wav")
		if err := tool.Silence(ctx, gap, path); err != nil {
			return 0, fmt.Errorf("generate trailing silence: %w", err)
		}
		segments = append(segments, path)
	}

	listPath := filepath.Join(workDir, "concat.txt")
	if err := writeConcatList(listPath, segments); err != nil {
		return 0, err
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return 0, err
	}
	tmpPath := outPath + ".tmp"
	if err := tool.Concat(ctx, listPath, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return 0, err
	}

	info, err := wavutil.InspectFile(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return 0, fmt.Errorf("validate %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		_ = os.Remove(tmpPath)
		return 0, err
	}
	return info.Duration, nil
}

func writeConcatList(path string, segments []string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, segment := range segments {
		escaped := strings.ReplaceAll(segment, "'", `'\''`)
		if _, err := fmt.Fprintf(w, "file '%s'\n", escaped); err != nil {
			return err
		}
	}
	return w.Flush()
}
