package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("exit status %d", e.code)
}

func (e *exitError) Unwrap() error {
	return e.err
}

func exitWithError(code int, err error) error {
	return &exitError{code: code, err: err}
}

func exitSilent(code int) error {
	return &exitError{code: code}
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	if err := NewRoot().Execute(); err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			if ee.err != nil {
				fmt.Fprintln(os.Stderr, "error:", ee.err)
			}
			return ee.code
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newRootCommand() *cobra.Command {
	var configPath string

	root := &cobra.Command{
		Use:           "ttsctl",
		Short:         "Turn text files into TTS audio",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.PersistentFlags().StringVar(&configPath, "config", "config.yaml", "path to config.yaml")

	root.AddCommand(
		newAddCommand(&configPath),
		newUpdateCommand(&configPath),
		newMakeCommand(&configPath),
		newLsCommand(&configPath),
		newRedoCommand(&configPath),
		newMergeCommand(&configPath),
		newCleanCommand(&configPath),
		newFilesCommand(&configPath),
		newFindCommand(&configPath),
		newPlayCommand(&configPath),
		newExtractCommand(),
	)
	return root
}

// NewRoot builds the command tree; exported for tests.
func NewRoot() *cobra.Command {
	return newRootCommand()
}
