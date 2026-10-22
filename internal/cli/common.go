package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/LiZeC123/kokomi-narrator/internal/config"
	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
)

// validateFilename accepts only a plain *.txt filename in the current directory.
func validateFilename(name string) error {
	return validateLocalFilename(name, ".txt")
}

// validateEpubFilename accepts only a plain *.epub filename in the current directory.
func validateEpubFilename(name string) error {
	return validateLocalFilename(name, ".epub")
}

func validateLocalFilename(name, wantExt string) error {
	if name == "" {
		return fmt.Errorf("filename must not be empty")
	}
	if filepath.IsAbs(name) || filepath.Base(name) != name {
		return fmt.Errorf("invalid filename %q: paths are not accepted", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid filename %q: path separators are not accepted", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("invalid filename %q: %q is not accepted", name, "..")
	}
	ext := filepath.Ext(name)
	if !strings.EqualFold(ext, wantExt) {
		return fmt.Errorf("invalid filename %q: extension must be %s", name, wantExt)
	}
	if len(name) == len(ext) {
		return fmt.Errorf("invalid filename %q: empty base name", name)
	}
	return nil
}

func ensureRegularFile(name string) error {
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", name)
	}
	return nil
}

// resolveFile accepts either a numeric file ID or a registered .txt filename.
func resolveFile(a *app, arg string) (*store.File, error) {
	if arg == "" {
		return nil, fmt.Errorf("file ID or filename must not be empty")
	}

	if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
		if id <= 0 {
			return nil, fmt.Errorf("invalid file ID %d", id)
		}
		file, err := a.store.FileByID(id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("file ID %d not found (run `ttsctl files`)", id)
			}
			return nil, err
		}
		return file, nil
	}

	if err := validateFilename(arg); err != nil {
		return nil, err
	}
	file, err := a.store.FileByName(arg)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("%s is not registered (run add first)", arg)
		}
		return nil, err
	}
	return file, nil
}

func readText(name string) ([]byte, []textparse.Segment, error) {
	if err := validateFilename(name); err != nil {
		return nil, nil, err
	}
	if err := ensureRegularFile(name); err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, nil, err
	}
	segments, err := textparse.Parse(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	return data, segments, nil
}

func audioPath(audioDir, voiceKey, textHash string) string {
	return filepath.Join(audioDir, voiceKey, textHash[:2], textHash+".wav")
}

func currentVoiceKey(cfg *config.Config) string {
	return config.VoiceKey(cfg.TTS.Character)
}

func parseSeq(arg string) (int, error) {
	seq, err := strconv.Atoi(arg)
	if err != nil {
		return 0, fmt.Errorf("invalid seq %q: %w", arg, err)
	}
	if seq <= 0 {
		return 0, fmt.Errorf("seq must be greater than zero")
	}
	return seq, nil
}

func shortHash(hash string) string {
	if len(hash) <= 8 {
		return hash
	}
	return hash[:8]
}
