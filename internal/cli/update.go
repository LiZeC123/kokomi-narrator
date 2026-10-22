package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/store"
	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
)

func newUpdateCommand(configPath *string) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "update <file>",
		Short: "Incrementally update a registered text file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runUpdate(a, args[0], force)
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "reparse even when the file content is unchanged")
	return cmd
}

func runUpdate(a *app, name string, force bool) error {
	data, segments, err := readText(name)
	if err != nil {
		return err
	}

	file, err := a.store.FileByName(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%s is not registered (run add first)", name)
		}
		return err
	}

	contentHash := textparse.HashBytes(data)
	if file.ContentHash == contentHash && !force {
		a.printf("%s: content unchanged\n", name)
		return nil
	}

	stats, err := a.store.UpdateFileSegments(file.ID, contentHash, segments, a.timestamp())
	if err != nil {
		return err
	}
	a.printf("updated %s: %d segments (%d added, %d reused, %d deleted)\n",
		name, len(segments), stats.Added, stats.Reused, stats.Deleted)
	return nil
}
