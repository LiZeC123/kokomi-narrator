package cli

import (
	"github.com/spf13/cobra"
)

func newFilesCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "files",
		Short: "List registered files and their IDs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runFiles(a)
			})
		},
	}
}

func runFiles(a *app) error {
	files, err := a.store.ListFiles()
	if err != nil {
		return err
	}
	if len(files) == 0 {
		a.printf("no files registered\n")
		return nil
	}

	a.printf("%-4s%-10s%s\n", "ID", "SEGMENTS", "FILE")
	for _, file := range files {
		a.printf("%-4d%-10d%s\n", file.ID, file.Segments, file.Filename)
	}
	return nil
}
