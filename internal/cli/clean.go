package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
)

func newCleanCommand(configPath *string) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Delete audio files that no database record references",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runClean(a, dryRun)
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "only list files that would be deleted")
	return cmd
}

func runClean(a *app, dryRun bool) error {
	refs, err := a.store.References()
	if err != nil {
		return err
	}

	var toDelete []string
	walkErr := filepath.WalkDir(a.cfg.AudioDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		base := d.Name()
		lower := strings.ToLower(base)
		switch {
		case strings.HasSuffix(lower, ".tmp"):
			toDelete = append(toDelete, path)
		case strings.HasSuffix(lower, ".wav"):
			rel, err := filepath.Rel(a.cfg.AudioDir, path)
			if err != nil {
				return err
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			hash := strings.TrimSuffix(base, filepath.Ext(base))
			if len(parts) != 3 || !isHexHash(hash) || parts[1] != hash[:2] {
				a.errorf("warning: skipping %s (unexpected audio path)\n", path)
				return nil
			}
			ref := store.Ref{VoiceKey: parts[0], TextHash: hash}
			if _, ok := refs[ref]; !ok {
				toDelete = append(toDelete, path)
			}
		default:
			a.errorf("warning: skipping %s (not a .wav or .tmp file)\n", path)
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
		return walkErr
	}

	if dryRun {
		for _, path := range toDelete {
			a.printf("would delete %s\n", path)
		}
		a.printf("clean: %d file(s) would be deleted\n", len(toDelete))
		if len(toDelete) > 0 {
			return exitSilent(2)
		}
		return nil
	}

	deleted := 0
	for _, path := range toDelete {
		if err := os.Remove(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		deleted++
	}
	removedDirs := removeEmptyDirs(a.cfg.AudioDir)
	a.printf("clean: deleted %d file(s), removed %d empty dir(s)\n", deleted, removedDirs)
	return nil
}

func isHexHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func removeEmptyDirs(root string) int {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], string(os.PathSeparator)) > strings.Count(dirs[j], string(os.PathSeparator))
	})

	removed := 0
	for _, dir := range dirs {
		if err := os.Remove(dir); err == nil {
			removed++
		}
	}
	return removed
}
