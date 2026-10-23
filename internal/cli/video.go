package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/pathutil"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func newVideoCommand(configPath *string) *cobra.Command {
	var image string
	cmd := &cobra.Command{
		Use:   "video <file|id>",
		Short: "Render the final audio and a cover image into an MP4",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runVideo(a, args[0], image)
			})
		},
	}
	cmd.Flags().StringVarP(&image, "image", "i", "", "cover image (overrides video.image)")
	return cmd
}

func runVideo(a *app, fileArg, imageFlag string) error {
	file, err := resolveFile(a, fileArg)
	if err != nil {
		return err
	}
	name := file.Filename
	base := strings.TrimSuffix(file.Filename, filepath.Ext(file.Filename))

	audioPath := filepath.Join(a.cfg.OutputDir, base+".wav")
	if _, err := wavutil.InspectFile(audioPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return exitWithError(2, fmt.Errorf(
				"video %s: final audio %s not found (run `ttsctl mix %s` first)", name, audioPath, fileArg))
		}
		return exitWithError(2, fmt.Errorf("video %s: invalid final audio %s: %w", name, audioPath, err))
	}

	image := strings.TrimSpace(imageFlag)
	if image == "" {
		image = a.cfg.Video.Image
	}
	if strings.TrimSpace(image) == "" {
		return exitWithError(2, fmt.Errorf("video %s: cover image is not configured (set video.image or pass --image)", name))
	}
	imagePath, err := pathutil.ExpandHome(image)
	if err != nil {
		return err
	}
	imagePath = filepath.Clean(imagePath)
	imageInfo, err := os.Stat(imagePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return exitWithError(2, fmt.Errorf(
				"video %s: cover image %s not found (set video.image or pass --image)", name, imagePath))
		}
		return err
	}
	if !imageInfo.Mode().IsRegular() {
		return exitWithError(2, fmt.Errorf("video %s: cover image %s is not a regular file", name, imagePath))
	}

	bin, err := exec.LookPath("audio2video")
	if err != nil {
		return fmt.Errorf("video requires audio2video in PATH")
	}

	outputPath := filepath.Join(a.cfg.OutputDir, base+".mp4")
	if err := os.MkdirAll(a.cfg.OutputDir, 0o755); err != nil {
		return err
	}
	tmpPath := filepath.Join(a.cfg.OutputDir, base+".tmp.mp4")
	_ = os.Remove(tmpPath)

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := exec.CommandContext(sigCtx, bin, "-a", audioPath, "-i", imagePath, "-o", tmpPath)
	var stderr bytes.Buffer
	cmd.Stdout = a.out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpPath)
		if sigCtx.Err() != nil {
			a.errorf("interrupted\n")
			return exitSilent(130)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return exitWithError(2, fmt.Errorf("video %s: audio2video: %w: %s", name, err, msg))
		}
		return exitWithError(2, fmt.Errorf("video %s: audio2video: %w", name, err))
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return exitWithError(2, fmt.Errorf("video %s: audio2video produced no output", name))
	}
	if info.Size() == 0 {
		_ = os.Remove(tmpPath)
		return exitWithError(2, fmt.Errorf("video %s: audio2video produced an empty output", name))
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	a.printf("video %s: wrote %s (audio=%s image=%s)\n", name, outputPath, audioPath, imagePath)
	return nil
}
