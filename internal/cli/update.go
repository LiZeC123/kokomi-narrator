package cli

import (
	"github.com/spf13/cobra"

	"github.com/LiZeC123/kokomi-narrator/internal/textparse"
)

func newUpdateCommand(configPath *string) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "update <file|id>",
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

func runUpdate(a *app, fileArg string, force bool) error {
	file, err := resolveFile(a, fileArg)
	if err != nil {
		return err
	}
	data, segments, err := readText(file.Filename)
	if err != nil {
		return err
	}

	contentHash := textparse.HashBytes(data)
	if file.ContentHash == contentHash && !force {
		a.printf("%s: content unchanged\n", file.Filename)
		return nil
	}

	stats, err := a.store.UpdateFileSegments(file.ID, contentHash, segments, a.timestamp())
	if err != nil {
		return err
	}
	a.printf("updated %s: %d segments (%d added, %d reused, %d deleted)\n",
		file.Filename, len(segments), stats.Added, stats.Reused, stats.Deleted)
	return nil
}
