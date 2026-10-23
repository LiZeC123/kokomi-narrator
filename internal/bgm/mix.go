package bgm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

// Mix combines the voice track and BGM into a single WAV. The output keeps the
// voice format and uses the longest input as its duration, so neither source is
// truncated when the two are close in length.
func Mix(ctx context.Context, tool *Tool, voicePath, bgmPath, outPath string, bgmVolume float64) (time.Duration, error) {
	if bgmVolume < 0 || bgmVolume > 1 {
		return 0, fmt.Errorf("BGM volume must be between 0 and 1")
	}

	voice, err := wavutil.InspectFile(voicePath)
	if err != nil {
		return 0, fmt.Errorf("read voice track %s: %w", voicePath, err)
	}
	if _, err := wavutil.InspectFile(bgmPath); err != nil {
		return 0, fmt.Errorf("read BGM track %s: %w", bgmPath, err)
	}

	format := voice.Format
	if format.Channels != 1 && format.Channels != 2 {
		return 0, fmt.Errorf("unsupported voice channel count %d (want mono or stereo)", format.Channels)
	}
	layout := "mono"
	if format.Channels == 2 {
		layout = "stereo"
	}
	codec, filterFormat, err := pcmCodec(format.BitsPerSample)
	if err != nil {
		return 0, err
	}

	filter := fmt.Sprintf(
		"[1:a]volume=%.4f,aresample=%d,aformat=sample_fmts=%s:sample_rates=%d:channel_layouts=%s[bg];"+
			"[0:a]aformat=sample_fmts=%s:sample_rates=%d:channel_layouts=%s[voice];"+
			"[voice][bg]amix=inputs=2:duration=longest:dropout_transition=0:normalize=0,"+
			"alimiter=limit=0.95:level=false:latency=true[a]",
		bgmVolume, format.SampleRate, filterFormat, format.SampleRate, layout,
		filterFormat, format.SampleRate, layout,
	)

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return 0, err
	}
	tmpPath := outPath + ".tmp"
	if err := tool.runFFmpeg(ctx,
		"-i", voicePath,
		"-i", bgmPath,
		"-filter_complex", filter,
		"-map", "[a]",
		"-c:a", codec,
		"-ar", fmt.Sprintf("%d", format.SampleRate),
		"-ac", fmt.Sprintf("%d", format.Channels),
		"-f", "wav",
		tmpPath,
	); err != nil {
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

func pcmCodec(bits uint16) (codec, filterFormat string, err error) {
	switch bits {
	case 8:
		return "pcm_u8", "s16", nil
	case 16:
		return "pcm_s16le", "s16", nil
	case 24:
		return "pcm_s24le", "s32", nil
	case 32:
		return "pcm_s32le", "s32", nil
	default:
		return "", "", fmt.Errorf("unsupported voice bit depth %d", bits)
	}
}
