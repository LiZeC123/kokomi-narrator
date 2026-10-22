package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
	"github.com/LiZeC123/kokomi-narrator/internal/wavutil"
)

func newAddCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "add <file>",
		Short: "Register a text file and its segments",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runAdd(a, args[0])
			})
		},
	}
}

func runAdd(a *app, name string) error {
	data, segments, err := readText(name)
	if err != nil {
		return err
	}

	if _, err := a.store.FileByName(name); err == nil {
		return fmt.Errorf("%s is already added", name)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	now := a.timestamp()
	file := &store.File{
		Filename:    name,
		VoiceKey:    currentVoiceKey(a.cfg),
		ContentHash: textparse.HashBytes(data),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := a.store.CreateFile(file, segments); err != nil {
		return err
	}

	speech := 0
	punctuation := 0
	seen := make(map[string]bool)
	reused := 0
	for _, seg := range segments {
		if seg.Kind == textparse.KindPunctuation {
			punctuation++
			continue
		}
		speech++
		if seen[seg.Hash] {
			continue
		}
		seen[seg.Hash] = true
		if !wavutil.IsValidPCMFile(audioPath(a.cfg.AudioDir, file.VoiceKey, seg.Hash)) {
			continue
		}
		n, err := a.store.SetHashStatus(file.ID, seg.Hash, store.StatusSuccess, nil, false, now)
		if err != nil {
			return err
		}
		reused += int(n)
	}

	a.printf("added %s: %d segments (%d speech, %d punctuation skipped, %d audio reused) (id=%d)\n",
		name, len(segments), speech, punctuation, reused, file.ID)
	return nil
}
