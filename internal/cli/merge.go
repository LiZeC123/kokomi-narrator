package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func newMergeCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "merge <file>",
		Short: "Merge all speech audio of a text file into one WAV",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runMerge(a, args[0])
			})
		},
	}
}

func runMerge(a *app, name string) error {
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

	base := strings.TrimSuffix(file.Filename, filepath.Ext(file.Filename))
	outputPath := filepath.Join(a.cfg.OutputDir, base+".wav")
	if !a.cfg.Merge.Overwrite {
		if _, err := os.Stat(outputPath); err == nil {
			return exitWithError(2, fmt.Errorf("merge %s: output %s already exists and merge.overwrite is false", name, outputPath))
		}
	}

	segments, err := a.store.SegmentsByFile(file.ID)
	if err != nil {
		return err
	}

	var (
		format   *wavutil.Format
		firstSeq int
		chunks   [][]byte
		badSeq   []int
	)
	for _, seg := range segments {
		if seg.Kind == textparse.KindPunctuation {
			continue
		}
		wav, err := wavutil.ReadFile(audioPath(a.cfg.AudioDir, file.VoiceKey, seg.TextHash))
		if err != nil {
			badSeq = append(badSeq, seg.Seq)
			continue
		}
		if format == nil {
			f := wav.Format
			format = &f
			firstSeq = seg.Seq
		} else if !format.Compatible(wav.Format) {
			return exitWithError(2, fmt.Errorf(
				"merge %s: audio format of seq %d differs from seq %d", name, seg.Seq, firstSeq))
		}
		chunks = append(chunks, wav.Data)
	}

	if len(badSeq) > 0 {
		return exitWithError(2, fmt.Errorf(
			"merge %s: missing or invalid audio for seq %s", name, joinInts(badSeq)))
	}
	if format == nil {
		return exitWithError(2, fmt.Errorf("merge %s: no speech segments to merge", name))
	}

	gap := wavutil.Silence(*format, a.cfg.Merge.GapBetweenSegments.Std())
	var pcm bytes.Buffer
	if a.cfg.Merge.LeadingTrailingGap && len(gap) > 0 {
		pcm.Write(gap)
	}
	for i, chunk := range chunks {
		if i > 0 && len(gap) > 0 {
			pcm.Write(gap)
		}
		pcm.Write(chunk)
	}
	if a.cfg.Merge.LeadingTrailingGap && len(gap) > 0 {
		pcm.Write(gap)
	}

	merged := wavutil.Encode(*format, pcm.Bytes())
	if err := os.MkdirAll(a.cfg.OutputDir, 0o755); err != nil {
		return err
	}
	if err := wavutil.WriteAtomic(outputPath, merged); err != nil {
		return err
	}

	duration := time.Duration(0)
	if format.ByteRate > 0 {
		duration = time.Duration(float64(len(pcm.Bytes())) / float64(format.ByteRate) * float64(time.Second))
	}
	a.printf("merged %s -> %s (%d speech segments, %s)\n",
		name, outputPath, len(chunks), duration.Round(time.Millisecond))
	return nil
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ", ")
}
