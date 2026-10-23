package cli

import (
	"github.com/spf13/cobra"
)

func newDelCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "del <file|id>",
		Short: "Delete a registered file and its segment records",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd, *configPath, func(a *app) error {
				return runDel(a, args[0])
			})
		},
	}
}

func runDel(a *app, fileArg string) error {
	file, err := resolveFile(a, fileArg)
	if err != nil {
		return err
	}
	segments, err := a.store.DeleteFile(file.ID)
	if err != nil {
		return err
	}
	a.printf("deleted %s (id=%d, %d segment(s)); audio files are kept, run `ttsctl clean` to remove unreferenced audio\n",
		file.Filename, file.ID, segments)
	return nil
}
